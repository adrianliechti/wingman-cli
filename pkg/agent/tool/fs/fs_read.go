package fs

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func ReadTool(root *os.Root) tool.Tool {
	return tool.Tool{
		Name: "read",

		Description: strings.Join([]string{
			fmt.Sprintf("Read the contents of a file. Output includes line numbers. Truncated to %d lines or %dKB.", DefaultMaxLines, DefaultMaxBytes/1024),
			"",
			"Usage:",
			"- You must read a file before editing it.",
			"- For large files, use offset and limit to read in chunks. The output will tell you where to continue.",
			"- Read multiple files in parallel by calling this tool multiple times in one response.",
			"- Prefer `grep` to locate relevant code before reading entire files.",
			"- When editing text from read output, preserve the exact indentation as shown AFTER the line number prefix. Never include line numbers in old_text.",
		}, "\n"),

		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":   map[string]any{"type": "string", "description": "File path relative to the working directory"},
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

			normalizedPath, err := ensurePathInWorkspace(pathArg, workingDir, "read file")

			if err != nil {
				return "", err
			}

			limit := 0
			offset := 0

			if l, ok := args["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}

			if o, ok := args["offset"].(float64); ok && o > 0 {
				offset = int(o) - 1
			}

			content, err := root.ReadFile(normalizedPath)

			if err != nil {
				return "", pathError("read file", pathArg, normalizedPath, workingDir, err)
			}

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
		},
	}
}
