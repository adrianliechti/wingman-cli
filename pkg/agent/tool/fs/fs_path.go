package fs

import (
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/adrianliechti/wingman-agent/internal/pathutil"
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

// resolveForCompare also handles paths whose parent directories do not exist
// yet. Resolution errors leave matching to the original path spelling; actual
// access still goes through os.Root.
func resolveForCompare(path string) string {
	if resolved, err := pathutil.ResolveExistingPrefix(path); err == nil {
		return resolved
	}
	return path
}

// resolveRootPath converts absolute in-root links and Windows junctions into a
// relative path that can be retried through the existing root handle. The
// root's name must still refer to that handle's directory; retargeting a
// workspace link must not switch an already opened workspace to another tree.
func resolveRootPath(root *os.Root, rel string) (string, bool) {
	workingDir := root.Name()
	abs := filepath.Join(workingDir, rel)
	resolved, err := pathutil.ResolveExistingPrefix(abs)
	if err != nil {
		return "", false
	}
	resolvedRoot, err := pathutil.Resolve(workingDir)
	if err != nil {
		return "", false
	}

	// No alias involved — the original failure was not link-related and a
	// retry through the same bytes would just repeat it.
	if resolved == filepath.Clean(abs) && resolvedRoot == filepath.Clean(workingDir) {
		return "", false
	}

	sub, ok := relPathLiteral(resolved, resolvedRoot)
	if !ok || sub == "" {
		return "", false
	}
	openedInfo, err := root.Stat(".")
	if err != nil {
		return "", false
	}
	namedInfo, err := os.Stat(resolvedRoot)
	if err != nil || !os.SameFile(openedInfo, namedInfo) {
		return "", false
	}

	return sub, true
}

type fileTarget struct {
	InWorkspace bool
	RootPath    string
	RelPath     string
	AbsPath     string // Always populated, including workspace targets.

	// Bind an external target to the directory validated for this request.
	rootInfo os.FileInfo
}

func fileTargetRoot(workspaceRoot *os.Root, target fileTarget) (*os.Root, string, func(), error) {
	if target.InWorkspace {
		return workspaceRoot, target.RelPath, nil, nil
	}
	if target.RootPath == "" {
		return nil, target.AbsPath, nil, nil
	}
	r, err := os.OpenRoot(target.RootPath)
	if err != nil {
		return nil, "", nil, err
	}
	info, err := r.Stat(".")
	if err == nil && !os.SameFile(target.rootInfo, info) {
		err = fmt.Errorf("allowed root %q changed after path validation", target.RootPath)
	}
	if err != nil {
		_ = r.Close()
		return nil, "", nil, err
	}
	return r, target.RelPath, func() { _ = r.Close() }, nil
}

// accessFileTarget shares root ownership and link retries across file operations.
// An unrestricted operation is selected only for an explicit wildcard target;
// a failed rooted operation must never fall back to unrestricted filesystem I/O.
func accessFileTarget[T any](root *os.Root, target fileTarget, within func(*os.Root, string) (T, error), unrestricted func(string) (T, error)) (T, error) {
	targetRoot, path, closeRoot, err := fileTargetRoot(root, target)
	if err != nil {
		var zero T
		return zero, err
	}
	if closeRoot != nil {
		defer closeRoot()
	}
	if targetRoot == nil {
		return unrestricted(path)
	}
	value, err := within(targetRoot, path)
	if err != nil {
		if sub, ok := resolveRootPath(targetRoot, path); ok {
			return within(targetRoot, sub)
		}
	}
	return value, err
}

func statFileTarget(root *os.Root, target fileTarget) (os.FileInfo, error) {
	return accessFileTarget(root, target, (*os.Root).Stat, os.Stat)
}

func readFileTarget(root *os.Root, target fileTarget) ([]byte, error) {
	return accessFileTarget(root, target, (*os.Root).ReadFile, os.ReadFile)
}

func openFileTarget(root *os.Root, target fileTarget) (*os.File, error) {
	return accessFileTarget(root, target, (*os.Root).Open, os.Open)
}

func writeFileTarget(root *os.Root, target fileTarget, content string) error {
	_, err := accessFileTarget(root, target,
		func(r *os.Root, path string) (struct{}, error) {
			return struct{}{}, writeRootFile(r, path, content)
		},
		func(path string) (struct{}, error) {
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return struct{}{}, err
			}
			return struct{}{}, os.WriteFile(path, []byte(content), 0644)
		})
	return err
}

func writeRootFile(root *os.Root, path, content string) error {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := root.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}

	return root.WriteFile(path, []byte(content), 0666)
}

// matchFileTarget selects the access grant shared by file and search tools.
// File tools bind its identity; search tools retain its opened handle.
func matchFileTarget(pathArg, workingDir string, allowedRoots []string, action string) (fileTarget, error) {
	pathArg = expandHome(normalizePathArg(pathArg))

	rootClean, sub, allowed := matchExplicitRoot(pathArg, workingDir, allowedRoots)
	if !allowed {
		if rel, ok := relPathWithinWorkspace(pathArg, workingDir); ok {
			return fileTarget{InWorkspace: true, RelPath: rel, AbsPath: filepath.Join(workingDir, rel)}, nil
		}
		if slices.Contains(allowedRoots, "*") {
			return fileTarget{AbsPath: filepath.Clean(pathArg)}, nil
		}
		return fileTarget{}, fmt.Errorf("cannot %s: outside workspace and allowed roots: path %q (workspace %q)", action, pathArg, workingDir)
	}

	if sub == "" {
		sub = "."
	}
	return fileTarget{RootPath: rootClean, RelPath: sub, AbsPath: filepath.Join(rootClean, sub)}, nil
}

func resolveFileTarget(pathArg, workingDir string, allowedRoots []string, action string) (fileTarget, error) {
	target, err := matchFileTarget(pathArg, workingDir, allowedRoots, action)
	if err != nil || target.RootPath == "" {
		return target, err
	}
	target.RootPath = resolveForCompare(target.RootPath)
	target.AbsPath = filepath.Join(target.RootPath, target.RelPath)
	r, err := os.OpenRoot(target.RootPath)
	if err != nil {
		return fileTarget{}, fmt.Errorf("cannot %s: open allowed root %q: %w", action, target.RootPath, err)
	}
	defer r.Close()
	// Capture identity through the handle: Windows may defer loading a file
	// ID for pathname-based Stat until SameFile, after the path has changed.
	target.rootInfo, err = r.Stat(".")
	if err != nil {
		return fileTarget{}, fmt.Errorf("cannot %s: stat allowed root %q: %w", action, target.RootPath, err)
	}
	return target, nil
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
	target, err := matchFileTarget(pathArg, workingDir, allowedReadRoots, action)
	if err != nil {
		return nil, err
	}
	if target.InWorkspace {
		searchDirFS := pathpkg.Clean(filepath.ToSlash(target.RelPath))
		return (&searchTarget{Root: workspaceRoot, SearchDirFS: searchDirFS}).resolveLinks(), nil
	}

	rootClean, sub := target.RootPath, target.RelPath
	if rootClean == "" {
		// Wildcard searches still need a directory handle to walk. For a
		// single file, anchor at its parent and search just that filename.
		rootClean, sub = target.AbsPath, "."
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

	reportPrefix := rootClean
	if rel, ok := relPathLiteral(rootClean, workingDir); ok {
		reportPrefix = rel
		if rel == "." {
			reportPrefix = ""
		}
	}
	return (&searchTarget{
		Root:         r,
		SearchDirFS:  filepath.ToSlash(sub),
		reportPrefix: reportPrefix,
		close:        func() { r.Close() },
	}).resolveLinks(), nil
}

// resolveLinks handles explicitly requested paths through in-root links for
// both workspace and allowed-root searches. OpenRoot on the existing handle
// keeps directory replacement from redirecting the search outside its boundary.
func (st *searchTarget) resolveLinks() *searchTarget {
	if st.SearchDirFS == "." {
		return st
	}
	if _, err := st.Root.Stat(st.SearchDirFS); err == nil {
		return st
	}
	resolved, ok := resolveRootPath(st.Root, filepath.FromSlash(st.SearchDirFS))
	if !ok {
		return st
	}

	info, err := st.Root.Stat(resolved)
	if err != nil {
		return st
	}

	prefix := st.ReportPath(st.SearchDirFS)
	dir, sub := resolved, "."
	if !info.IsDir() {
		dir, sub = filepath.Dir(resolved), filepath.Base(resolved)
		prefix = filepath.Dir(prefix)
	}

	r, err := st.Root.OpenRoot(dir)
	if err != nil {
		return st
	}

	st.Close()
	return &searchTarget{
		Root:         r,
		SearchDirFS:  filepath.ToSlash(sub),
		reportPrefix: prefix,
		close:        func() { r.Close() },
	}
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
