package fs

import (
	"context"
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman-cli/pkg/tool"
)

func ReadTool() tool.Tool {
	return tool.Tool{
		Name: "read",

		Description: fmt.Sprintf(
			"Read the contents of a file. For text files, output is truncated to %d lines or %dKB (whichever is hit first). Use offset/limit for large files.",
			DefaultMaxLines,
			DefaultMaxBytes/1024,
		),

		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":   map[string]any{"type": "string", "description": "Path to the file to read"},
				"offset": map[string]any{"type": "integer", "description": "Line number to start reading from (1-based)"},
				"limit":  map[string]any{"type": "integer", "description": "Maximum number of lines to read"},
			},
			"required": []string{"path"},
		},

		Execute: func(ctx context.Context, env *tool.Environment, args map[string]any) (string, error) {
			pathArg, ok := args["path"].(string)

			if !ok || pathArg == "" {
				return "", fmt.Errorf("path is required")
			}

			workingDir := env.WorkingDir()

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

			content, err := env.Root.ReadFile(normalizedPath)

			if err != nil {
				return "", pathError("read file", pathArg, normalizedPath, workingDir, err)
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

			selected := strings.Join(lines[offset:end], "\n")
			output, truncatedByLines, truncatedByBytes := truncateHead(selected)

			outputLines := len(strings.Split(output, "\n"))
			endLine := offset + outputLines

			if truncatedByLines || truncatedByBytes {
				notice := fmt.Sprintf("\n\n[Lines %d-%d of %d", offset+1, endLine, total)

				if truncatedByBytes {
					notice += fmt.Sprintf(", %dKB limit", DefaultMaxBytes/1024)
				}

				notice += fmt.Sprintf(". Use offset=%d to continue]", endLine+1)

				return output + notice, nil
			}

			if end < total {
				return output + fmt.Sprintf("\n\n[Lines %d-%d of %d. Use offset=%d to continue]",
					offset+1, endLine, total, endLine+1), nil
			}

			return output, nil
		},
	}
}
