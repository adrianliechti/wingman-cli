package fs

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/adrianliechti/wingman-agent/internal/pathutil"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/sergi/go-diff/diffmatchpatch"
)

const (
	DefaultMaxLines = 2000
	DefaultMaxBytes = 48 * 1024

	MaxReadFileBytes = 10 * 1024 * 1024
	MaxEditFileBytes = 10 * 1024 * 1024
)

func expandHome(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

// A URI-style drive path may retain its leading slash after being copied into
// a tool argument. Strip it only from absolute drive paths, before containment
// checks. VolumeName is empty on Unix; on Windows the length check excludes
// UNC and device paths, while IsAbs excludes drive-relative paths.
func normalizePathArg(path string) string {
	if native, ok := strings.CutPrefix(path, "/"); ok && len(filepath.VolumeName(native)) == 2 && filepath.IsAbs(native) {
		return native
	}
	return path
}

func matchAllowedRoot(absPath string, allowedRoots []string) (rootClean, sub string, ok bool) {
	absPath = filepath.Clean(absPath)
	if slices.Contains(allowedRoots, "*") {
		return absPath, "", true
	}

	if rootClean, sub, ok = matchAllowedRootLiteral(absPath, allowedRoots); ok {
		return rootClean, sub, true
	}

	resolved := resolveForCompare(absPath)
	resolvedRoots := make([]string, len(allowedRoots))
	changed := resolved != absPath
	for i, allowed := range allowedRoots {
		if allowed == "" {
			continue
		}
		allowed = filepath.Clean(allowed)
		resolvedRoots[i] = resolveForCompare(allowed)
		if resolvedRoots[i] != allowed {
			changed = true
		}
	}
	if !changed {
		return "", "", false
	}

	return matchAllowedRootLiteral(resolved, resolvedRoots)
}

func matchAllowedRootLiteral(cleaned string, allowedRoots []string) (rootClean, sub string, ok bool) {
	for _, allowed := range allowedRoots {
		if allowed == "" {
			continue
		}
		allowed = filepath.Clean(allowed)
		rel, matched := relPathLiteral(cleaned, allowed)
		if !matched {
			continue
		}
		if rel == "." {
			rel = ""
		}
		// A skill directory may itself be linked outside the broader skills
		// root. Prefer its explicit grant so os.Root opens at that boundary.
		if !ok || len(rel) < len(sub) {
			rootClean, sub, ok = allowed, rel, true
		}
	}
	return rootClean, sub, ok
}

// Explicit roots take precedence over the workspace: an approved skills root
// can be a link located inside the workspace whose target is outside it.
func matchExplicitRoot(pathArg, workingDir string, allowedRoots []string) (string, string, bool) {
	if len(allowedRoots) == 0 || slices.Contains(allowedRoots, "*") {
		return "", "", false
	}
	if !filepath.IsAbs(pathArg) {
		pathArg = filepath.Join(workingDir, pathArg)
	}
	// Keep ordinary workspace files on the workspace handle, including their
	// original spelling in freshness notifications. Only an external target
	// needs to be opened through a separate access grant.
	if _, inside := relPathWithinWorkspace(pathArg, workingDir); inside {
		if _, inside := relPathLiteral(resolveForCompare(pathArg), resolveForCompare(workingDir)); inside {
			return "", "", false
		}
	}
	return matchAllowedRoot(pathArg, allowedRoots)
}

// pathPrefixLen reports how many bytes of path are consumed by prefix when
// compared with the platform's path case sensitivity. Byte lengths cannot be
// compared across normalized forms: lowercasing changes the encoded length of
// some runes, so the offset must come from the string being sliced.
func pathPrefixLen(path, prefix string) (int, bool) {
	i, j := 0, 0
	for j < len(prefix) {
		if i >= len(path) {
			return 0, false
		}
		pathRune, pathSize := utf8.DecodeRuneInString(path[i:])
		prefixRune, prefixSize := utf8.DecodeRuneInString(prefix[j:])
		if !equalPathRune(pathRune, prefixRune) {
			return 0, false
		}
		i += pathSize
		j += prefixSize
	}
	return i, true
}

func equalPathRune(a, b rune) bool {
	if a == b {
		return true
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return unicode.ToLower(a) == unicode.ToLower(b)
	}
	return false
}

// resolveForCompare resolves symlinks (and Windows junctions) for containment
// comparisons, falling back to resolving the parent when the leaf does not
// exist yet (e.g. a file about to be created).
func resolveForCompare(path string) string {
	if resolved, err := pathutil.Resolve(path); err == nil {
		return resolved
	}

	dir := filepath.Dir(path)
	if dir == path {
		return path
	}
	if resolvedDir, err := pathutil.Resolve(dir); err == nil {
		return filepath.Join(resolvedDir, filepath.Base(path))
	}

	return path
}

// resolveInWorkspace reports where an in-workspace relative path actually
// lives after resolving links, and whether that location is still inside the
// resolved workspace root. os.Root rejects absolute link targets — which
// Windows junctions always are — so in-root aliases fall back to direct
// access through this check.
func resolveInWorkspace(workingDir, rel string) (string, bool) {
	resolved := resolveForCompare(filepath.Join(workingDir, rel))

	resolvedRoot, err := pathutil.Resolve(workingDir)
	if err != nil {
		return "", false
	}

	if _, ok := relPathLiteral(resolved, resolvedRoot); !ok {
		return "", false
	}

	return resolved, true
}

// fallbackRoot serves in-workspace paths whose literal spelling os.Root
// refused (absolute in-root symlink, Windows junction): it re-anchors an
// os.Root at the resolved workspace root and returns the resolved relative
// path. Going back through os.Root — instead of plain os calls on the
// resolved path — keeps containment kernel-enforced, so a link retargeted
// after resolution fails closed rather than escaping.
func fallbackRoot(workingDir, rel string) (*os.Root, string, bool) {
	abs := filepath.Join(workingDir, rel)
	resolved := resolveForCompare(abs)

	resolvedRoot, err := pathutil.Resolve(workingDir)
	if err != nil {
		return nil, "", false
	}

	// No alias involved — the original failure was not link-related and a
	// retry through the same bytes would just repeat it.
	if resolved == filepath.Clean(abs) && resolvedRoot == filepath.Clean(workingDir) {
		return nil, "", false
	}

	sub, ok := relPathLiteral(resolved, resolvedRoot)
	if !ok || sub == "" {
		return nil, "", false
	}

	r, err := os.OpenRoot(resolvedRoot)
	if err != nil {
		return nil, "", false
	}

	return r, sub, true
}

type fileTarget struct {
	InWorkspace bool
	RootPath    string
	RelPath     string
	AbsPath     string
}

func fileTargetRoot(workspaceRoot *os.Root, target fileTarget) (*os.Root, string, func(), bool, error) {
	if target.InWorkspace {
		return workspaceRoot, target.RelPath, nil, true, nil
	}
	if target.RootPath == "" {
		return nil, "", nil, false, nil
	}
	r, err := os.OpenRoot(target.RootPath)
	if err != nil {
		return nil, "", nil, false, err
	}
	return r, target.RelPath, func() { _ = r.Close() }, true, nil
}

func statFileTarget(root *os.Root, target fileTarget) (os.FileInfo, error) {
	targetRoot, rel, closeRoot, rooted, err := fileTargetRoot(root, target)
	if err != nil {
		return nil, err
	}
	if closeRoot != nil {
		defer closeRoot()
	}
	if rooted {
		info, err := targetRoot.Stat(rel)
		if err != nil {
			if fr, sub, ok := fallbackRoot(targetRoot.Name(), rel); ok {
				defer fr.Close()
				return fr.Stat(sub)
			}
		}
		return info, err
	}
	return os.Stat(target.AbsPath)
}

func readFileTarget(root *os.Root, target fileTarget) ([]byte, error) {
	targetRoot, rel, closeRoot, rooted, err := fileTargetRoot(root, target)
	if err != nil {
		return nil, err
	}
	if closeRoot != nil {
		defer closeRoot()
	}
	if rooted {
		content, err := targetRoot.ReadFile(rel)
		if err != nil {
			if fr, sub, ok := fallbackRoot(targetRoot.Name(), rel); ok {
				defer fr.Close()
				return fr.ReadFile(sub)
			}
		}
		return content, err
	}
	return os.ReadFile(target.AbsPath)
}

func openFileTarget(root *os.Root, target fileTarget) (*os.File, error) {
	targetRoot, rel, closeRoot, rooted, err := fileTargetRoot(root, target)
	if err != nil {
		return nil, err
	}
	if closeRoot != nil {
		defer closeRoot()
	}
	if rooted {
		f, err := targetRoot.Open(rel)
		if err != nil {
			if fr, sub, ok := fallbackRoot(targetRoot.Name(), rel); ok {
				defer fr.Close()
				return fr.Open(sub)
			}
		}
		return f, err
	}
	return os.Open(target.AbsPath)
}

func writeFileTarget(root *os.Root, target fileTarget, content string) error {
	targetRoot, rel, closeRoot, rooted, err := fileTargetRoot(root, target)
	if err != nil {
		return err
	}
	if closeRoot != nil {
		defer closeRoot()
	}
	if rooted {
		err := writeRootFile(targetRoot, rel, content)
		if err != nil {
			if fr, sub, ok := fallbackRoot(targetRoot.Name(), rel); ok {
				defer fr.Close()
				return writeRootFile(fr, sub, content)
			}
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target.AbsPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(target.AbsPath, []byte(content), 0644)
}

func resolveFileTarget(pathArg, workingDir string, allowedRoots []string, action string) (fileTarget, error) {
	pathArg = expandHome(normalizePathArg(pathArg))

	rootClean, sub, allowed := matchExplicitRoot(pathArg, workingDir, allowedRoots)
	if !allowed {
		if rel, ok := relPathWithinWorkspace(pathArg, workingDir); ok {
			return fileTarget{InWorkspace: true, RelPath: rel}, nil
		}
		if slices.Contains(allowedRoots, "*") {
			return fileTarget{AbsPath: filepath.Clean(pathArg)}, nil
		}
		return fileTarget{}, fmt.Errorf("cannot %s: outside workspace and allowed roots: path %q (workspace %q)", action, pathArg, workingDir)
	}

	rootClean = resolveForCompare(rootClean)
	rel := "."
	if sub != "" {
		rel = sub
	}
	return fileTarget{RootPath: rootClean, RelPath: rel, AbsPath: filepath.Join(rootClean, rel)}, nil
}

type searchTarget struct {
	Root        *os.Root
	SearchDirFS string
	close       func()

	reportPrefix string
}

func (st *searchTarget) Close() {
	if st.close != nil {
		st.close()
	}
}

func (st *searchTarget) ReportPath(fsPath string) string {
	if st.reportPrefix == "" {
		return filepath.FromSlash(fsPath)
	}
	return filepath.Join(st.reportPrefix, filepath.FromSlash(fsPath))
}

func resolveSearchTarget(pathArg, workingDir string, workspaceRoot *os.Root, allowedReadRoots []string, action string) (*searchTarget, error) {
	pathArg = expandHome(normalizePathArg(pathArg))

	rootClean, sub, allowed := matchExplicitRoot(pathArg, workingDir, allowedReadRoots)
	if !allowed {
		if rel, ok := relPathWithinWorkspace(pathArg, workingDir); ok {
			searchDirFS := pathpkg.Clean(filepath.ToSlash(rel))
			if searchDirFS != "." {
				if _, statErr := workspaceRoot.Stat(searchDirFS); statErr != nil {
					if target, ok := linkedSearchTarget(workingDir, searchDirFS); ok {
						return target, nil
					}
				}
			}
			return &searchTarget{Root: workspaceRoot, SearchDirFS: searchDirFS}, nil
		}
		rootClean, sub, allowed = matchAllowedRoot(pathArg, allowedReadRoots)
		if !allowed {
			return nil, fmt.Errorf("cannot %s: outside workspace and allowed read roots: path %q (workspace %q)", action, pathArg, workingDir)
		}
	}

	// With a wildcard, the matcher returns the requested path itself. A
	// single-file search must anchor its os.Root at the parent directory.
	if slices.Contains(allowedReadRoots, "*") {
		info, err := os.Stat(rootClean)
		if err != nil {
			return nil, fmt.Errorf("cannot %s: stat path %q: %w", action, rootClean, err)
		}
		if !info.IsDir() {
			rootClean, sub = filepath.Dir(rootClean), filepath.Base(rootClean)
		}
	}

	r, err := os.OpenRoot(rootClean)
	if err != nil {
		return nil, fmt.Errorf("cannot %s: open allowed root %q: %w", action, rootClean, err)
	}

	searchDirFS := "."
	if sub != "" {
		searchDirFS = filepath.ToSlash(sub)
	}

	reportPrefix := rootClean
	if rel, ok := relPathLiteral(rootClean, workingDir); ok {
		reportPrefix = rel
		if rel == "." {
			reportPrefix = ""
		}
	}
	return &searchTarget{
		Root:         r,
		SearchDirFS:  searchDirFS,
		reportPrefix: reportPrefix,
		close:        func() { r.Close() },
	}, nil
}

// linkedSearchTarget serves search paths that traverse an in-root alias
// (absolute symlink, Windows junction) that os.Root refuses, by opening a
// dedicated root at the resolved location while reporting the caller's
// spelling.
func linkedSearchTarget(workingDir, searchDirFS string) (*searchTarget, bool) {
	resolved, ok := resolveInWorkspace(workingDir, filepath.FromSlash(searchDirFS))
	if !ok {
		return nil, false
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return nil, false
	}

	prefix := filepath.FromSlash(searchDirFS)
	dir, sub := resolved, "."
	if !info.IsDir() {
		dir, sub = filepath.Dir(resolved), filepath.Base(resolved)
		prefix = filepath.Dir(prefix)
	}

	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, false
	}

	return &searchTarget{
		Root:         r,
		SearchDirFS:  filepath.ToSlash(sub),
		reportPrefix: prefix,
		close:        func() { r.Close() },
	}, true
}

func relPathWithinWorkspace(absPath, workingDir string) (string, bool) {
	if rel, ok := relPathLiteral(absPath, workingDir); ok {
		return rel, true
	}

	// The path or the workspace may be spelled through an alias (symlink,
	// macOS /tmp, a Windows junction like C:\dev); retry on resolved paths.
	absPath = filepath.Clean(absPath)
	workingDir = filepath.Clean(workingDir)
	resolvedPath := resolveForCompare(absPath)
	resolvedDir := resolveForCompare(workingDir)
	if resolvedPath == absPath && resolvedDir == workingDir {
		return "", false
	}

	return relPathLiteral(resolvedPath, resolvedDir)
}

func relPathLiteral(absPath, workingDir string) (string, bool) {
	if !filepath.IsAbs(absPath) {
		return filepath.FromSlash(absPath), true
	}

	absPath = filepath.Clean(absPath)
	workingDir = filepath.Clean(workingDir)
	if rel, err := filepath.Rel(workingDir, absPath); err == nil && filepath.IsLocal(rel) {
		return rel, true
	}

	// filepath.Rel is case-sensitive on macOS. Preserve the existing
	// platform case-folding policy without losing the target's original bytes.
	if consumed, matched := pathPrefixLen(absPath, workingDir); matched {
		if consumed == len(absPath) {
			return ".", true
		}
		if strings.HasSuffix(workingDir, string(filepath.Separator)) {
			return absPath[consumed:], true
		}
		if absPath[consumed] == filepath.Separator {
			return absPath[consumed+1:], true
		}
	}
	return "", false
}

func normalizePathForComparison(path string) string {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.ToLower(path)
	}
	return path
}

func detectLineEnding(content string) string {
	if strings.Contains(content, "\r\n") {
		return "\r\n"
	}
	return "\n"
}

func normalizeToLF(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	return text
}

func restoreLineEndings(text, ending string) string {
	if ending == "\r\n" {
		return strings.ReplaceAll(text, "\n", "\r\n")
	}

	return text
}

func stripBom(content string) (bom string, text string) {
	if strings.HasPrefix(content, "\uFEFF") {
		return "\uFEFF", content[len("\uFEFF"):]
	}

	return "", content
}

func normalizeForFuzzyMatch(text string) string {
	normalized, _ := normalizeForFuzzyMatchWithMap(text)
	return normalized
}

func normalizeForFuzzyMatchWithMap(text string) (string, []int) {
	var b strings.Builder
	offsetMap := []int{0}

	for lineStart := 0; lineStart <= len(text); {
		lineEnd := strings.IndexByte(text[lineStart:], '\n')
		hasNewline := lineEnd != -1
		if hasNewline {
			lineEnd += lineStart
		} else {
			lineEnd = len(text)
		}

		trimmedEnd := lineEnd
		for trimmedEnd > lineStart && (text[trimmedEnd-1] == ' ' || text[trimmedEnd-1] == '\t') {
			trimmedEnd--
		}

		for pos := lineStart; pos < trimmedEnd; {
			r, size := utf8.DecodeRuneInString(text[pos:trimmedEnd])
			replacement := normalizeFuzzyRune(r)
			b.WriteString(replacement)

			originalBytes := text[pos : pos+size]
			if replacement == originalBytes {
				for i := 1; i <= size; i++ {
					offsetMap = append(offsetMap, pos+i)
				}
			} else {
				for i := 0; i < len(replacement); i++ {
					offsetMap = append(offsetMap, pos+size)
				}
			}

			pos += size
		}

		if !hasNewline {
			break
		}

		b.WriteByte('\n')
		offsetMap = append(offsetMap, lineEnd+1)
		lineStart = lineEnd + 1
	}

	return b.String(), offsetMap
}

func normalizeFuzzyRune(r rune) string {
	switch r {
	case '\u2018', '\u2019', '\u201A', '\u201B':
		return "'"
	case '\u201C', '\u201D', '\u201E', '\u201F':
		return "\""
	case '\u2010', '\u2011', '\u2012', '\u2013', '\u2014', '\u2015', '\u2212':
		return "-"
	case '\u00A0', '\u2002', '\u2003', '\u2004', '\u2005', '\u2006', '\u2007', '\u2008',
		'\u2009', '\u200A', '\u202F', '\u205F', '\u3000':
		return " "
	default:
		return string(r)
	}
}

type fuzzyMatchResult struct {
	found          bool
	index          int
	matchLength    int
	usedFuzzyMatch bool
}

func fuzzyFindText(content, oldText string) fuzzyMatchResult {
	if i := strings.Index(content, oldText); i != -1 {
		return fuzzyMatchResult{found: true, index: i, matchLength: len(oldText)}
	}

	fuzzyOldText := normalizeForFuzzyMatch(oldText)
	if fuzzyOldText == "" {
		return fuzzyMatchResult{index: -1}
	}

	fuzzyContent, fuzzyToOriginal := normalizeForFuzzyMatchWithMap(content)
	fuzzyIndex := strings.Index(fuzzyContent, fuzzyOldText)
	if fuzzyIndex == -1 {
		return fuzzyMatchResult{index: -1}
	}

	originalIndex := fuzzyToOriginal[fuzzyIndex]
	originalEnd := fuzzyToOriginal[fuzzyIndex+len(fuzzyOldText)]

	return fuzzyMatchResult{
		found:          true,
		index:          originalIndex,
		matchLength:    originalEnd - originalIndex,
		usedFuzzyMatch: true,
	}
}

// The model just produced edited content, so echo only a bounded diff. Both
// dimensions matter: a line cap alone still permits a minified or encoded line
// to flood the context and UI.
const (
	maxDiffLines     = 200
	maxDiffLineBytes = 4 * 1024
)

func generateDiffString(oldContent, newContent string) string {
	dmp := diffmatchpatch.New()

	oldLines, newLines, lineArray := dmp.DiffLinesToChars(oldContent, newContent)
	diffs := dmp.DiffMain(oldLines, newLines, false)
	diffs = dmp.DiffCharsToLines(diffs, lineArray)
	diffs = dmp.DiffCleanupSemantic(diffs)

	var output strings.Builder
	oldLineNum := 1
	newLineNum := 1

	for _, diff := range diffs {
		lines := strings.Split(diff.Text, "\n")

		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}

		switch diff.Type {
		case diffmatchpatch.DiffEqual:
			oldLineNum += len(lines)
			newLineNum += len(lines)
		case diffmatchpatch.DiffDelete:
			for _, line := range lines {
				fmt.Fprintf(&output, "-%d %s\n", oldLineNum, truncateDiffLine(line))
				oldLineNum++
			}
		case diffmatchpatch.DiffInsert:
			for _, line := range lines {
				fmt.Fprintf(&output, "+%d %s\n", newLineNum, truncateDiffLine(line))
				newLineNum++
			}
		}
	}

	return capDiffLines(output.String())
}

func truncateDiffLine(line string) string {
	if len(line) <= maxDiffLineBytes {
		return line
	}
	prefix := line[:maxDiffLineBytes]
	for !utf8.ValidString(prefix) {
		prefix = prefix[:len(prefix)-1]
	}
	return prefix + fmt.Sprintf("… [diff line truncated: %d bytes omitted]", len(line)-len(prefix))
}

func capDiffLines(diff string) string {
	trimmed := strings.TrimRight(diff, "\n")
	if trimmed == "" {
		return diff
	}

	lines := strings.Split(trimmed, "\n")
	if len(lines) <= maxDiffLines {
		return diff
	}

	omitted := len(lines) - maxDiffLines
	lines = lines[:maxDiffLines]

	return strings.Join(lines, "\n") + fmt.Sprintf("\n… diff truncated: %d more changed lines (the file was written in full)\n", omitted)
}

var vcsDirs = map[string]bool{
	".git": true,
	".svn": true,
	".hg":  true,
	".bzr": true,
	".jj":  true,
	".sl":  true,
}

// binarySniffLen bounds how many leading bytes are inspected when classifying
// a file as binary.
const binarySniffLen = 8000

// isBinaryContent reports whether data looks like binary (non-text) content.
// It uses the same NUL-byte heuristic as git and grep: a NUL within the first
// several KB reliably marks binary content, while text files — including UTF-8
// sources, SVG, JSON, and extension-less docs — never contain one.
func isBinaryContent(data []byte) bool {
	if len(data) > binarySniffLen {
		data = data[:binarySniffLen]
	}
	return bytes.IndexByte(data, 0) >= 0
}

func relPathSlash(base, target string) string {
	rel, err := filepath.Rel(filepath.FromSlash(base), filepath.FromSlash(target))

	if err != nil {
		return target
	}

	return filepath.ToSlash(rel)
}

func relPathFromBase(base, path string) string {
	if base == "." {
		return path
	}

	return relPathSlash(base, path)
}

func pathDomain(fsPath string) []string {
	if fsPath == "" || fsPath == "." {
		return nil
	}

	return strings.Split(fsPath, "/")
}

func loadGitignore(fsys fs.FS, domain []string) []gitignore.Pattern {
	gitignorePath := ".gitignore"

	if len(domain) > 0 {
		gitignorePath = pathpkg.Join(append(domain, ".gitignore")...)
	}

	f, err := fsys.Open(gitignorePath)

	if err != nil {
		return nil
	}
	defer f.Close()

	var patterns []gitignore.Pattern
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		patterns = append(patterns, gitignore.ParsePattern(line, domain))
	}

	return patterns
}
