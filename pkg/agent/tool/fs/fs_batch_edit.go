package fs

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

// batchEditTool exposes one provider-neutral mutation tool. Its JSON schema is
// intentionally small: exact replacements and file creation are enough for
// normal coding flows, while an array gives structured tool calling the same
// multi-file transaction boundary as a proprietary patch dialect.
func batchEditTool(root *os.Root, freshness *Freshness, allowedWriteRoots ...string) tool.Tool {
	return tool.Tool{
		Name:   "edit",
		Effect: tool.StaticEffect(tool.EffectMutates),
		Description: strings.Join([]string{
			"Create or edit one or more files atomically using exact string replacements.",
			"- Put every independent replacement or file creation you already know about into one `edits` array; do not call this tool repeatedly when the changes can be batched.",
			"- Read an existing file before editing it, and preserve its indentation exactly. Never include the read tool's line-number prefix.",
			"- Each `old_string` must occur exactly once unless `replace_all` is true. Include enough unchanged surrounding text to make it unique.",
			"- To create a new file (or replace an empty file), use an empty `old_string` and put the complete file in `new_string`.",
			"- Entries for the same file run in order, so later entries see earlier replacements.",
			"- Every entry is validated and every result is staged before any file is changed. If one entry fails, no files change.",
			"- The result is authoritative; do not re-read files merely to verify a successful edit.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"edits": map[string]any{
					"type":        "array",
					"minItems":    1,
					"description": "All file creations and replacements to apply in one atomic transaction.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"file_path":   map[string]any{"type": "string", "description": "Workspace-relative path, absolute workspace path, or path in an explicitly allowed write root."},
							"old_string":  map[string]any{"type": "string", "description": "Exact text to replace; empty only when creating a file or replacing an empty file."},
							"new_string":  map[string]any{"type": "string", "description": "Replacement text or complete content for a new file."},
							"replace_all": map[string]any{"type": "boolean", "description": "Replace every occurrence instead of requiring a unique match.", "default": false},
						},
						"required":             []string{"file_path", "old_string", "new_string"},
						"additionalProperties": false,
					},
				},
			},
			"required":             []string{"edits"},
			"additionalProperties": false,
		},
		Execute: func(ctx context.Context, args map[string]any) (tool.Result, error) {
			files, editCount, err := parseBatchEdits(root, args, allowedWriteRoots)
			if err != nil {
				return tool.Result{}, err
			}

			tx := &fileTransaction{}
			summaries := make([]string, 0, len(files))
			for _, file := range files {
				info, statErr := statFileTarget(root, file.target)
				exists := statErr == nil
				switch {
				case exists && info.IsDir():
					return tool.Result{}, fmt.Errorf("cannot edit %s: path is a directory", file.displayPath)
				case exists && !info.Mode().IsRegular():
					return tool.Result{}, fmt.Errorf("cannot edit %s: path is not a regular file", file.displayPath)
				case statErr != nil && !os.IsNotExist(statErr):
					return tool.Result{}, fmt.Errorf("stat %s: %w", file.displayPath, statErr)
				}
				if !exists && file.entries[0].op.oldText != "" {
					return tool.Result{}, fmt.Errorf("cannot edit %s: file does not exist; use an empty old_string to create it", file.displayPath)
				}
				if exists && info.Size() > MaxEditFileBytes {
					return tool.Result{}, fmt.Errorf("file %s is %d bytes; edits are capped at %d bytes", file.displayPath, info.Size(), MaxEditFileBytes)
				}
				if exists && freshness.stale(ctx, file.target, info) {
					return tool.Result{}, fmt.Errorf("cannot edit %s: the file changed on disk after you last read it; read it again first", file.displayPath)
				}

				var before []byte
				if exists {
					before, err = readFileTarget(root, file.target)
					if err != nil {
						return tool.Result{}, fmt.Errorf("read %s: %w", file.displayPath, err)
					}
				}
				bom, content := stripBom(string(before))
				ending := detectLineEnding(content)
				updated := normalizeToLF(content)
				for _, entry := range file.entries {
					updated, err = applyEditOp(updated, entry.op, file.displayPath)
					if err != nil {
						return tool.Result{}, fmt.Errorf("edits[%d] for %s: %w (no files changed)", entry.index, file.displayPath, err)
					}
				}
				after := []byte(bom + restoreLineEndings(updated, ending))
				if exists && bytes.Equal(before, after) {
					return tool.Result{}, fmt.Errorf("edits made no changes to %s", file.displayPath)
				}

				mode := os.FileMode(0o644)
				if exists {
					mode = info.Mode().Perm()
					summaries = append(summaries, "M "+file.displayPath)
				} else {
					summaries = append(summaries, "A "+file.displayPath)
				}
				tx.changes = append(tx.changes, fileChange{
					target:        file.target,
					displayPath:   file.displayPath,
					before:        before,
					beforePresent: exists,
					beforeMode:    mode,
					after:         after,
					afterPresent:  true,
				})
			}

			if err := commitFileTransaction(root, tx); err != nil {
				return tool.Result{}, err
			}
			for _, change := range tx.changes {
				freshness.record(ctx, change.target)
			}
			return tool.Text(fmt.Sprintf("Applied %d edits across %d files atomically.\n%s", editCount, len(files), strings.Join(summaries, "\n"))), nil
		},
	}
}

type batchEditEntry struct {
	index int
	op    editOp
}

type batchEditFile struct {
	target      fileTarget
	displayPath string
	entries     []batchEditEntry
}

// parseBatchEdits also accepts the previous single-file call shapes. They are
// deliberately absent from the schema so providers and models see one clear,
// efficient interface while integrations get a non-breaking migration path.
func parseBatchEdits(root *os.Root, args map[string]any, allowedWriteRoots []string) ([]batchEditFile, int, error) {
	rawEntries, err := compatibleBatchEntries(args)
	if err != nil {
		return nil, 0, err
	}

	files := make([]batchEditFile, 0, len(rawEntries))
	byTarget := make(map[string]int)
	for index, entry := range rawEntries {
		pathArg, ok := entry["file_path"].(string)
		if !ok || strings.TrimSpace(pathArg) == "" {
			return nil, 0, fmt.Errorf("edits[%d].file_path is required", index)
		}
		target, err := resolveFileTarget(pathArg, root.Name(), allowedWriteRoots, "edit file")
		if err != nil {
			return nil, 0, fmt.Errorf("edits[%d].file_path: %w", index, err)
		}
		op, err := newEditOp(entry)
		if err != nil {
			return nil, 0, fmt.Errorf("edits[%d]: %w", index, err)
		}

		key := batchTargetKey(root.Name(), target)
		fileIndex, exists := byTarget[key]
		if !exists {
			fileIndex = len(files)
			byTarget[key] = fileIndex
			displayPath := pathArg
			if target.InWorkspace {
				displayPath = filepath.ToSlash(filepath.Clean(target.RelPath))
			}
			files = append(files, batchEditFile{target: target, displayPath: displayPath})
		}
		files[fileIndex].entries = append(files[fileIndex].entries, batchEditEntry{index: index, op: op})
	}
	return files, len(rawEntries), nil
}

func compatibleBatchEntries(args map[string]any) ([]map[string]any, error) {
	raw, hasBatch := args["edits"]
	if !hasBatch {
		return []map[string]any{args}, nil
	}

	if _, hasOld := args["old_string"]; hasOld {
		return nil, fmt.Errorf("provide either edits or old_string/new_string, not both")
	}
	if _, hasNew := args["new_string"]; hasNew {
		return nil, fmt.Errorf("provide either edits or old_string/new_string, not both")
	}
	if replaceAll, _ := args["replace_all"].(bool); replaceAll {
		return nil, fmt.Errorf("replace_all cannot be combined with edits; set it per edit entry instead")
	}

	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil, fmt.Errorf("edits must be a non-empty array")
	}
	commonPath, _ := args["file_path"].(string)
	entries := make([]map[string]any, 0, len(list))
	for index, value := range list {
		entry, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("edits[%d] must be an object", index)
		}
		if _, hasPath := entry["file_path"]; !hasPath && commonPath != "" {
			copy := make(map[string]any, len(entry)+1)
			for key, value := range entry {
				copy[key] = value
			}
			copy["file_path"] = commonPath
			entry = copy
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func batchTargetKey(workspace string, target fileTarget) string {
	var path string
	switch {
	case target.InWorkspace:
		path = filepath.Join(workspace, target.RelPath)
	case target.AbsPath != "":
		path = target.AbsPath
	default:
		path = filepath.Join(target.RootPath, target.RelPath)
	}
	return normalizePathForComparison(resolveForCompare(filepath.Clean(path)))
}
