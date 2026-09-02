package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

const applyPatchGrammar = `start: begin_patch hunk+ end_patch
begin_patch: "*** Begin Patch" LF
end_patch: "*** End Patch" LF?

hunk: add_hunk | delete_hunk | update_hunk
add_hunk: "*** Add File: " filename LF add_line+
delete_hunk: "*** Delete File: " filename LF
update_hunk: "*** Update File: " filename LF change_move? change?

filename: /(.+)/
add_line: "+" /(.*)/ LF -> line

change_move: "*** Move to: " filename LF
change: (change_context | change_line)+ eof_line?
change_context: ("@@" | "@@ " /(.+)/) LF
change_line: ("+" | "-" | " ") /(.*)/ LF
eof_line: "*** End of File" LF

%import common.LF`

type patchHunkKind uint8

const (
	patchAdd patchHunkKind = iota + 1
	patchDelete
	patchUpdate
)

type patchHunk struct {
	kind   patchHunkKind
	path   string
	moveTo string
	add    string
	chunks []patchChunk
}

type patchChunk struct {
	context    string
	hasContext bool
	oldLines   []string
	newLines   []string
	endOfFile  bool
}

type patchFileChange struct {
	path          string
	before        []byte
	beforePresent bool
	beforeMode    os.FileMode
	after         []byte
	afterPresent  bool

	tempPath   string
	backupPath string
	installed  bool
}

type patchPlan struct {
	changes   []patchFileChange
	summaries []string
}

// ApplyPatchTool returns an OpenAI custom/free-form editing tool. The input is
// Codex's patch grammar, which lets one model call add, update, move, and delete
// several files without JSON escaping. All hunks are validated before any file
// contents are changed.
func ApplyPatchTool(root *os.Root, freshness *Freshness) tool.Tool {
	return tool.Tool{
		Name:        "apply_patch",
		Description: "Edit one or more workspace files atomically. This is a FREEFORM tool: send a *** Begin Patch / *** End Patch patch directly, without JSON. Prefer workspace-relative paths; absolute paths are accepted only when they resolve inside the workspace.",
		Effect:      tool.StaticEffect(tool.EffectMutates),
		Freeform: &tool.FreeformFormat{
			Syntax:     "lark",
			Definition: applyPatchGrammar,
		},
		ExecuteText: func(ctx context.Context, input string) (tool.Result, error) {
			hunks, err := parseApplyPatch(root, input)
			if err != nil {
				return tool.Result{}, err
			}
			plan, err := buildPatchPlan(ctx, root, freshness, hunks)
			if err != nil {
				return tool.Result{}, err
			}
			if err := commitPatchPlan(root, plan); err != nil {
				return tool.Result{}, err
			}
			for _, change := range plan.changes {
				target := fileTarget{InWorkspace: true, RelPath: change.path}
				if change.afterPresent {
					freshness.record(ctx, target)
				} else {
					freshness.forget(ctx, target)
				}
			}
			return tool.Text("Done!\n" + strings.Join(plan.summaries, "\n")), nil
		},
	}
}

func parseApplyPatch(root *os.Root, input string) ([]patchHunk, error) {
	input = normalizeToLF(input)
	input = strings.Trim(input, "\n")
	lines := strings.Split(input, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "*** Begin Patch" {
		return nil, fmt.Errorf("invalid patch: the first line must be '*** Begin Patch'")
	}
	if len(lines) < 2 || strings.TrimSpace(lines[len(lines)-1]) != "*** End Patch" {
		return nil, fmt.Errorf("invalid patch: the last line must be '*** End Patch'")
	}

	var hunks []patchHunk
	for i := 1; i < len(lines)-1; {
		line := strings.TrimRight(lines[i], " \t")
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			path, err := normalizePatchPath(root, strings.TrimPrefix(line, "*** Add File: "))
			if err != nil {
				return nil, patchLineError(i+1, err)
			}
			i++
			var added []string
			for i < len(lines)-1 && !isApplyPatchHunkHeader(lines[i]) {
				if !strings.HasPrefix(lines[i], "+") {
					return nil, fmt.Errorf("invalid add hunk at line %d: every content line must start with '+'", i+1)
				}
				added = append(added, strings.TrimPrefix(lines[i], "+"))
				i++
			}
			if len(added) == 0 {
				return nil, fmt.Errorf("invalid add hunk for %s: at least one line is required", path)
			}
			hunks = append(hunks, patchHunk{kind: patchAdd, path: path, add: strings.Join(added, "\n") + "\n"})

		case strings.HasPrefix(line, "*** Delete File: "):
			path, err := normalizePatchPath(root, strings.TrimPrefix(line, "*** Delete File: "))
			if err != nil {
				return nil, patchLineError(i+1, err)
			}
			hunks = append(hunks, patchHunk{kind: patchDelete, path: path})
			i++

		case strings.HasPrefix(line, "*** Update File: "):
			path, err := normalizePatchPath(root, strings.TrimPrefix(line, "*** Update File: "))
			if err != nil {
				return nil, patchLineError(i+1, err)
			}
			h := patchHunk{kind: patchUpdate, path: path}
			i++
			if i < len(lines)-1 && strings.HasPrefix(strings.TrimRight(lines[i], " \t"), "*** Move to: ") {
				h.moveTo, err = normalizePatchPath(root, strings.TrimPrefix(strings.TrimRight(lines[i], " \t"), "*** Move to: "))
				if err != nil {
					return nil, patchLineError(i+1, err)
				}
				i++
			}
			for i < len(lines)-1 && !isApplyPatchHunkHeader(lines[i]) {
				line = strings.TrimRight(lines[i], "\r")
				switch {
				case line == "@@":
					if err := appendPatchChunk(&h.chunks, patchChunk{}, i+1); err != nil {
						return nil, err
					}
				case strings.HasPrefix(line, "@@ "):
					if err := appendPatchChunk(&h.chunks, patchChunk{context: strings.TrimPrefix(line, "@@ "), hasContext: true}, i+1); err != nil {
						return nil, err
					}
				case line == "*** End of File":
					if len(h.chunks) == 0 || patchChunkEmpty(h.chunks[len(h.chunks)-1]) {
						return nil, fmt.Errorf("invalid update hunk at line %d: end-of-file marker follows no changes", i+1)
					}
					h.chunks[len(h.chunks)-1].endOfFile = true
				case line == "":
					chunk := ensurePatchChunk(&h.chunks)
					chunk.oldLines = append(chunk.oldLines, "")
					chunk.newLines = append(chunk.newLines, "")
				case strings.HasPrefix(line, " "):
					chunk := ensurePatchChunk(&h.chunks)
					text := strings.TrimPrefix(line, " ")
					chunk.oldLines = append(chunk.oldLines, text)
					chunk.newLines = append(chunk.newLines, text)
				case strings.HasPrefix(line, "+"):
					chunk := ensurePatchChunk(&h.chunks)
					chunk.newLines = append(chunk.newLines, strings.TrimPrefix(line, "+"))
				case strings.HasPrefix(line, "-"):
					chunk := ensurePatchChunk(&h.chunks)
					chunk.oldLines = append(chunk.oldLines, strings.TrimPrefix(line, "-"))
				default:
					return nil, fmt.Errorf("invalid update hunk at line %d: lines must start with ' ', '+', '-', or '@@'", i+1)
				}
				i++
			}
			if len(h.chunks) == 0 && h.moveTo == "" {
				return nil, fmt.Errorf("invalid update hunk for %s: no changes", path)
			}
			if len(h.chunks) > 0 && patchChunkEmpty(h.chunks[len(h.chunks)-1]) {
				return nil, fmt.Errorf("invalid update hunk for %s: empty change section", path)
			}
			hunks = append(hunks, h)

		default:
			return nil, fmt.Errorf("invalid patch hunk header at line %d: %q", i+1, lines[i])
		}
	}
	if len(hunks) == 0 {
		return nil, fmt.Errorf("invalid patch: at least one file hunk is required")
	}
	return hunks, nil
}

func patchLineError(line int, err error) error {
	return fmt.Errorf("invalid patch path at line %d: %w", line, err)
}

func isApplyPatchHunkHeader(line string) bool {
	line = strings.TrimRight(line, " \t\r")
	return strings.HasPrefix(line, "*** Add File: ") ||
		strings.HasPrefix(line, "*** Delete File: ") ||
		strings.HasPrefix(line, "*** Update File: ")
}

func normalizePatchPath(root *os.Root, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is empty")
	}
	path = filepath.FromSlash(path)
	if filepath.IsAbs(path) || filepath.VolumeName(path) != "" {
		workspace, err := filepath.Abs(root.Name())
		if err != nil {
			return "", fmt.Errorf("resolve workspace: %w", err)
		}
		relative, err := filepath.Rel(filepath.Clean(workspace), filepath.Clean(path))
		if err != nil {
			return "", fmt.Errorf("path %q is outside the workspace", path)
		}
		path = relative
	}
	path = filepath.Clean(path)
	if path == "." || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the workspace", path)
	}
	return filepath.ToSlash(path), nil
}

func appendPatchChunk(chunks *[]patchChunk, chunk patchChunk, line int) error {
	if len(*chunks) > 0 && patchChunkEmpty((*chunks)[len(*chunks)-1]) {
		return fmt.Errorf("invalid update hunk at line %d: consecutive context markers", line)
	}
	*chunks = append(*chunks, chunk)
	return nil
}

func ensurePatchChunk(chunks *[]patchChunk) *patchChunk {
	if len(*chunks) == 0 {
		*chunks = append(*chunks, patchChunk{})
	}
	return &(*chunks)[len(*chunks)-1]
}

func patchChunkEmpty(chunk patchChunk) bool {
	return len(chunk.oldLines) == 0 && len(chunk.newLines) == 0
}

func buildPatchPlan(ctx context.Context, root *os.Root, freshness *Freshness, hunks []patchHunk) (*patchPlan, error) {
	plan := &patchPlan{}
	touched := map[string]bool{}
	markTouched := func(path string) error {
		key := normalizePathForComparison(filepath.Clean(filepath.FromSlash(path)))
		if touched[key] {
			return fmt.Errorf("path %s is modified more than once in the same patch", path)
		}
		touched[key] = true
		return nil
	}

	for _, h := range hunks {
		if err := markTouched(h.path); err != nil {
			return nil, err
		}
		source, exists, err := readPatchFile(ctx, root, freshness, h.path)
		if err != nil {
			return nil, err
		}

		switch h.kind {
		case patchAdd:
			if exists {
				return nil, fmt.Errorf("cannot add %s: file already exists", h.path)
			}
			plan.changes = append(plan.changes, patchFileChange{path: h.path, after: []byte(h.add), afterPresent: true, beforeMode: 0o644})
			plan.summaries = append(plan.summaries, "A "+h.path)

		case patchDelete:
			if !exists {
				return nil, fmt.Errorf("cannot delete %s: file does not exist", h.path)
			}
			plan.changes = append(plan.changes, patchFileChange{path: h.path, before: source.content, beforePresent: true, beforeMode: source.mode})
			plan.summaries = append(plan.summaries, "D "+h.path)

		case patchUpdate:
			if !exists {
				return nil, fmt.Errorf("cannot update %s: file does not exist", h.path)
			}
			after, err := applyPatchChunks(h.path, source.content, h.chunks)
			if err != nil {
				return nil, err
			}
			if h.moveTo == "" {
				if slices.Equal(source.content, after) {
					return nil, fmt.Errorf("patch made no changes to %s", h.path)
				}
				plan.changes = append(plan.changes, patchFileChange{
					path: h.path, before: source.content, beforePresent: true, beforeMode: source.mode,
					after: after, afterPresent: true,
				})
				plan.summaries = append(plan.summaries, "M "+h.path)
				continue
			}

			if err := markTouched(h.moveTo); err != nil {
				return nil, err
			}
			if _, destinationExists, err := readPatchFile(ctx, root, freshness, h.moveTo); err != nil {
				return nil, err
			} else if destinationExists {
				return nil, fmt.Errorf("cannot move %s to %s: destination already exists", h.path, h.moveTo)
			}
			plan.changes = append(plan.changes,
				patchFileChange{path: h.path, before: source.content, beforePresent: true, beforeMode: source.mode},
				patchFileChange{path: h.moveTo, after: after, afterPresent: true, beforeMode: source.mode},
			)
			plan.summaries = append(plan.summaries, "R "+h.path+" -> "+h.moveTo)
		}
	}
	return plan, nil
}

type patchSource struct {
	content []byte
	mode    os.FileMode
}

func readPatchFile(ctx context.Context, root *os.Root, freshness *Freshness, path string) (patchSource, bool, error) {
	path = filepath.FromSlash(path)
	info, err := root.Stat(path)
	if os.IsNotExist(err) {
		return patchSource{}, false, nil
	}
	if err != nil {
		return patchSource{}, false, fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return patchSource{}, false, fmt.Errorf("cannot patch %s: path is not a regular file", path)
	}
	if linkInfo, err := root.Lstat(path); err != nil {
		return patchSource{}, false, fmt.Errorf("inspect %s: %w", path, err)
	} else if linkInfo.Mode()&os.ModeSymlink != 0 {
		return patchSource{}, false, fmt.Errorf("cannot patch %s: symbolic links are not supported", path)
	}
	if info.Size() > MaxEditFileBytes {
		return patchSource{}, false, fmt.Errorf("cannot patch %s: file is %d bytes (limit %d)", path, info.Size(), MaxEditFileBytes)
	}
	target := fileTarget{InWorkspace: true, RelPath: path}
	if freshness.stale(ctx, target, info) {
		return patchSource{}, false, fmt.Errorf("cannot patch %s: the file changed on disk after it was last read; read it again first", path)
	}
	content, err := root.ReadFile(path)
	if err != nil {
		return patchSource{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	return patchSource{content: content, mode: info.Mode().Perm()}, true, nil
}

func applyPatchChunks(path string, content []byte, chunks []patchChunk) ([]byte, error) {
	if len(chunks) == 0 {
		return slices.Clone(content), nil
	}
	bom, text := stripBom(string(content))
	ending := detectLineEnding(text)
	text = normalizeToLF(text)
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	cursor := 0
	for _, chunk := range chunks {
		if chunk.hasContext {
			index := findPatchSequence(lines, []string{chunk.context}, cursor, false)
			if index < 0 {
				return nil, fmt.Errorf("failed to find context %q in %s", chunk.context, path)
			}
			cursor = index + 1
		}
		if len(chunk.oldLines) == 0 {
			lines = append(lines, chunk.newLines...)
			cursor = len(lines)
			continue
		}
		index := findPatchSequence(lines, chunk.oldLines, cursor, chunk.endOfFile)
		if index < 0 {
			return nil, fmt.Errorf("failed to find expected lines in %s:\n%s", path, strings.Join(chunk.oldLines, "\n"))
		}
		next := make([]string, 0, len(lines)-len(chunk.oldLines)+len(chunk.newLines))
		next = append(next, lines[:index]...)
		next = append(next, chunk.newLines...)
		next = append(next, lines[index+len(chunk.oldLines):]...)
		lines = next
		cursor = index + len(chunk.newLines)
	}

	result := strings.Join(lines, "\n")
	if len(lines) > 0 {
		result += "\n"
	}
	return []byte(bom + restoreLineEndings(result, ending)), nil
}

func findPatchSequence(lines, pattern []string, start int, endOfFile bool) int {
	if len(pattern) == 0 {
		return min(start, len(lines))
	}
	if len(pattern) > len(lines) {
		return -1
	}
	start = min(max(start, 0), len(lines)-len(pattern))
	for mode := range 3 {
		if endOfFile {
			at := len(lines) - len(pattern)
			if patchLinesEqual(lines[at:], pattern, mode) {
				return at
			}
		}
		for i := start; i <= len(lines)-len(pattern); i++ {
			if patchLinesEqual(lines[i:i+len(pattern)], pattern, mode) {
				return i
			}
		}
	}
	return -1
}

func patchLinesEqual(lines, pattern []string, mode int) bool {
	for i := range pattern {
		left, right := lines[i], pattern[i]
		switch mode {
		case 1:
			left, right = strings.TrimRight(left, " \t"), strings.TrimRight(right, " \t")
		case 2:
			left, right = strings.TrimSpace(left), strings.TrimSpace(right)
		}
		if left != right {
			return false
		}
	}
	return true
}

func commitPatchPlan(root *os.Root, plan *patchPlan) (err error) {
	cleanup := func() {
		for i := range plan.changes {
			change := &plan.changes[i]
			if change.tempPath != "" {
				_ = root.Remove(filepath.FromSlash(change.tempPath))
			}
		}
	}
	defer func() {
		if err != nil {
			cleanup()
		}
	}()

	// Stage every new version before moving any original file out of place.
	for i := range plan.changes {
		change := &plan.changes[i]
		if !change.afterPresent {
			continue
		}
		path := filepath.FromSlash(change.path)
		dir := filepath.Dir(path)
		if dir != "." {
			if err := root.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("prepare directory for %s: %w", change.path, err)
			}
		}
		change.tempPath = filepath.ToSlash(filepath.Join(dir, ".wingman-patch-"+uuid.NewString()))
		file, err := root.OpenFile(filepath.FromSlash(change.tempPath), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("stage %s: %w", change.path, err)
		}
		_, writeErr := file.Write(change.after)
		closeErr := file.Close()
		if writeErr != nil {
			return fmt.Errorf("stage %s: %w", change.path, writeErr)
		}
		if closeErr != nil {
			return fmt.Errorf("stage %s: %w", change.path, closeErr)
		}
		mode := change.beforeMode.Perm()
		if mode == 0 {
			mode = 0o644
		}
		if err := root.Chmod(filepath.FromSlash(change.tempPath), mode); err != nil {
			return fmt.Errorf("stage permissions for %s: %w", change.path, err)
		}
	}

	rollback := func() {
		for i := len(plan.changes) - 1; i >= 0; i-- {
			change := &plan.changes[i]
			path := filepath.FromSlash(change.path)
			if change.installed {
				_ = root.Remove(path)
			}
			if change.backupPath != "" {
				_ = root.Rename(filepath.FromSlash(change.backupPath), path)
			}
		}
	}

	for i := range plan.changes {
		change := &plan.changes[i]
		path := filepath.FromSlash(change.path)
		if change.beforePresent {
			change.backupPath = filepath.ToSlash(filepath.Join(filepath.Dir(path), ".wingman-backup-"+uuid.NewString()))
			if err := root.Rename(path, filepath.FromSlash(change.backupPath)); err != nil {
				rollback()
				return fmt.Errorf("prepare %s: %w", change.path, err)
			}
		}
		if change.afterPresent {
			if err := root.Rename(filepath.FromSlash(change.tempPath), path); err != nil {
				rollback()
				return fmt.Errorf("install %s: %w", change.path, err)
			}
			change.tempPath = ""
			change.installed = true
		}
	}

	for i := range plan.changes {
		change := &plan.changes[i]
		if change.backupPath != "" {
			_ = root.Remove(filepath.FromSlash(change.backupPath))
			change.backupPath = ""
		}
	}
	cleanup()
	return nil
}
