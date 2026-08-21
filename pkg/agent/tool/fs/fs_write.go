package fs

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func WriteTool(root *os.Root, allowedWriteRoots ...string) tool.Tool {
	return writeTool(root, nil, nil, allowedWriteRoots...)
}

func writeTool(root *os.Root, tracker *contentTracker, freshness *Freshness, allowedWriteRoots ...string) tool.Tool {
	lines := []string{
		"Writes a file to the local filesystem. Creates parent directories as needed and overwrites any existing file at the same path.",
		"- For existing files, `read` first so you do not discard content.",
		"- Prefer `edit` for existing files: it sends only the diff. Use `write` for new files or complete rewrites.",
		"- Overwrites return a line diff against the previous content; treat it as authoritative instead of re-reading the file.",
	}
	if tracker != nil {
		lines[1] = "- Overwriting an existing text file requires `read`ing it first in this session — the call fails if the file's current content has never been shown to you."
	}

	return tool.Tool{
		Name:   "write",
		Effect: tool.StaticEffect(tool.EffectMutates),

		Description: strings.Join(lines, "\n"),

		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{"type": "string", "description": "The absolute path to the file to write (must be absolute, not relative)."},
				"content":   map[string]any{"type": "string", "description": "The content to write to the file."},
			},
			"required":             []string{"file_path", "content"},
			"additionalProperties": false,
		},

		Execute: func(ctx context.Context, args map[string]any) (tool.Result, error) {
			pathArg, ok := args["file_path"].(string)

			if !ok || pathArg == "" {
				return tool.Result{}, fmt.Errorf("file_path is required")
			}

			content, ok := args["content"].(string)
			if !ok {
				return tool.Result{}, fmt.Errorf("content is required")
			}

			workingDir := root.Name()
			target, err := resolveFileTarget(pathArg, workingDir, allowedWriteRoots, "write file")
			if err != nil {
				return tool.Result{}, err
			}

			isNew := true
			info, err := statFileTarget(root, target)
			switch {
			case err == nil:
				if info.IsDir() {
					return tool.Result{}, fmt.Errorf("cannot write file: path %q is a directory", pathArg)
				}
				if !info.Mode().IsRegular() {
					return tool.Result{}, fmt.Errorf("cannot write file: path %q is not a regular file", pathArg)
				}
				isNew = false
			case !os.IsNotExist(err):
				return tool.Result{}, fmt.Errorf("stat file %q: %w", pathArg, err)
			}

			var oldContent []byte
			if !isNew && info.Size() <= MaxEditFileBytes {
				oldContent, _ = readFileTarget(root, target)
			}

			if len(oldContent) > 0 && !isBinaryContent(oldContent) && !tracker.knows(oldContent) {
				return tool.Result{}, fmt.Errorf("cannot overwrite %s: its current content has not been read in this session — `read` it first", pathArg)
			}

			if err := writeFileTarget(root, target, content); err != nil {
				return tool.Result{}, fmt.Errorf("write file %q: %w", pathArg, err)
			}

			tracker.record([]byte(content))
			freshness.record(ctx, target)

			if isNew {
				return tool.Text(fmt.Sprintf("Created %s (%d bytes written)", pathArg, len(content))), nil
			}

			result := fmt.Sprintf("Updated %s (%d bytes written)", pathArg, len(content))

			if oldContent != nil {
				_, oldText := stripBom(string(oldContent))
				_, newText := stripBom(content)
				if diff := generateDiffString(normalizeToLF(oldText), normalizeToLF(newText)); diff != "" {
					result += "\n\n" + diff
				}
			}

			return tool.Text(result), nil
		},
	}
}
