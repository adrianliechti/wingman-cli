package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func WriteTool(root *os.Root) tool.Tool {
	return tool.Tool{
		Name: "write",

		Description: strings.Join([]string{
			"Write content to a file. Creates the file and parent directories if they don't exist, overwrites if it does.",
			"",
			"Usage:",
			"- Prefer the `edit` tool for modifying existing files — it only sends the diff and is safer.",
			"- Only use this tool to create new files or for complete rewrites of existing files.",
			"- If overwriting an existing file, you MUST read it first to understand the current content.",
			"- NEVER create documentation files (*.md) or README files unless explicitly requested.",
		}, "\n"),

		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "File path relative to the working directory"},
				"content": map[string]any{"type": "string", "description": "Complete content to write to the file"},
			},
			"required": []string{"path", "content"},
		},

		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			pathArg, ok := args["path"].(string)

			if !ok || pathArg == "" {
				return "", fmt.Errorf("path is required")
			}

			workingDir := root.Name()

			normalizedPath, err := ensurePathInWorkspace(pathArg, workingDir, "write file")

			if err != nil {
				return "", err
			}

			content, ok := args["content"].(string)

			if !ok {
				return "", fmt.Errorf("content is required")
			}

			// Check if file exists before writing (for create vs update reporting)
			_, existsErr := root.ReadFile(normalizedPath)
			isNew := existsErr != nil

			dir := filepath.Dir(normalizedPath)

			if dir != "." && dir != "" {
				if err := root.MkdirAll(dir, 0755); err != nil {
					return "", pathError("create directory", pathArg, normalizedPath, workingDir, err)
				}
			}

			file, err := root.Create(normalizedPath)

			if err != nil {
				return "", pathError("create file", pathArg, normalizedPath, workingDir, err)
			}

			if _, err := file.WriteString(content); err != nil {
				file.Close()
				return "", fmt.Errorf("failed to write file: %w", err)
			}

			if err := file.Close(); err != nil {
				return "", fmt.Errorf("failed to close file: %w", err)
			}

			action := "Updated"
			if isNew {
				action = "Created"
			}

			result := fmt.Sprintf("%s %s (%d bytes)", action, pathArg, len(content))

			return result, nil
		},
	}
}
