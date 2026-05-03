package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

// ReadTool returns the file-read tool. allowedReadRoots are absolute paths
// outside the workspace that this tool is additionally permitted to read
// (e.g. discovered personal skill directories). Anything outside both the
// workspace and the allow-list is rejected.
func ReadTool(root *os.Root, allowedReadRoots ...string) tool.Tool {
	return tool.Tool{
		Name:   "read",
		Effect: tool.StaticEffect(tool.EffectReadOnly),

		Description: strings.Join([]string{
			fmt.Sprintf("Read the contents of a file. Output includes line numbers. Truncated to %d lines or %dKB.", DefaultMaxLines, DefaultMaxBytes/1024),
			"",
			"Usage:",
			"- You must read a file before editing it.",
			"- If you read this file earlier in the conversation and nothing has modified it since, the prior result is still current — refer to it instead of re-reading. Re-read only after `edit` or `write`, or when the user indicates external changes.",
			"- For large files, use offset and limit to read in chunks. The output will tell you where to continue.",
			"- Read multiple files in parallel by calling this tool multiple times in one response.",
			"- Prefer `grep` to locate relevant code before reading entire files. Often a single `grep` returns enough context that no `read` is needed.",
			"- When editing text from read output, preserve the exact indentation as shown AFTER the line number prefix. Never include line numbers in old_text.",
		}, "\n"),

		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":   map[string]any{"type": "string", "description": "File path relative to the working directory, or an absolute path inside an allowed root (e.g. a discovered skill directory). Paths beginning with `~/` are expanded to the user's home directory."},
				"offset": map[string]any{"type": "integer", "description": "Line number to start reading from (1-based)"},
				"limit":  map[string]any{"type": "integer", "description": "Maximum number of lines to read. Only provide if the file is too large to read at once."},
			},
			"required": []string{"path"},
		},

		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			pathArg, ok := args["path"].(string)

			if !ok || pathArg == "" {
				return "", fmt.Errorf("path is required")
			}

			workingDir := root.Name()
			expanded := expandHome(pathArg)

			if isBinaryFile(expanded) {
				return "", fmt.Errorf("cannot read %s: file appears to be binary (extension %q). Use the shell tool with an appropriate viewer if you really need to inspect it", pathArg, filepath.Ext(expanded))
			}

			limit := 0
			offset := 0

			if l, ok := args["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}

			if o, ok := args["offset"].(float64); ok && o > 0 {
				offset = int(o) - 1
			}

			content, err := readFromAllowedLocation(root, workingDir, expanded, allowedReadRoots)
			if err != nil {
				return "", err
			}

			return formatRead(content, offset, limit)
		},
	}
}

// readFromAllowedLocation tries the workspace root first, then falls back to
// any path inside the allow-list. Returns the file contents or an error.
func readFromAllowedLocation(root *os.Root, workingDir, path string, allowedRoots []string) ([]byte, error) {
	if !isOutsideWorkspace(path, workingDir) {
		normalizedPath := normalizePath(path, workingDir)
		content, err := root.ReadFile(normalizedPath)
		if err != nil {
			return nil, pathError("read file", path, normalizedPath, workingDir, err)
		}
		return content, nil
	}

	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("cannot read file: relative path %q is outside workspace", path)
	}

	cleaned := cleanPath(path)
	cmpPath := normalizePathForComparison(cleaned)
	for _, allowed := range allowedRoots {
		allowedClean := cleanPath(allowed)
		cmpAllowed := normalizePathForComparison(allowedClean)
		if cmpPath == cmpAllowed || strings.HasPrefix(cmpPath, cmpAllowed+string(filepath.Separator)) {
			content, err := os.ReadFile(cleaned)
			if err != nil {
				return nil, fmt.Errorf("read file %q: %w", path, err)
			}
			return content, nil
		}
	}

	return nil, fmt.Errorf("cannot read file: path %q is outside workspace and not in any allowed root", path)
}

// expandHome resolves a leading `~` to the user's home dir. Accepts both
// `~/...` (forward slash) and `~\...` (Windows backslash) forms.
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

// formatRead applies the truncation + line-numbering display logic.
func formatRead(content []byte, offset, limit int) (string, error) {
	if len(content) == 0 {
		return "(empty file)", nil
	}

	lines := strings.Split(string(content), "\n")
	total := len(lines)

	if offset >= total {
		return "", fmt.Errorf("offset %d is beyond end of file (%d lines)", offset+1, total)
	}

	end := total

	if limit > 0 && offset+limit < total {
		end = offset + limit
	}

	var numbered []string

	for i, line := range lines[offset:end] {
		lineNum := offset + i + 1
		numbered = append(numbered, fmt.Sprintf("%6d\t%s", lineNum, line))
	}

	selected := strings.Join(numbered, "\n")
	output, truncated := truncateHead(selected)

	outputLines := len(strings.Split(output, "\n"))
	endLine := offset + outputLines

	if truncated || end < total {
		notice := fmt.Sprintf("\n\n[Lines %d-%d of %d", offset+1, endLine, total)
		notice += fmt.Sprintf(". Use offset=%d to continue]", endLine+1)

		return output + notice, nil
	}

	return output, nil
}
