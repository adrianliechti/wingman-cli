package fs

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/adrianliechti/wingman-cli/pkg/tool"
)

func WriteTool() tool.Tool {
	return tool.Tool{
		Name: "write",

		Description: "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Automatically creates parent directories.",

		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "Path to the file to write"},
				"content": map[string]any{"type": "string", "description": "Content to write to the file"},
			},
			"required": []string{"path", "content"},
		},

		Execute: func(ctx context.Context, env *tool.Environment, args map[string]any) (string, error) {
			pathArg, ok := args["path"].(string)

			if !ok || pathArg == "" {
				return "", fmt.Errorf("path is required")
			}

			content, ok := args["content"].(string)

			if !ok {
				return "", fmt.Errorf("content is required")
			}

			dir := filepath.Dir(pathArg)

			if dir != "." && dir != "" {
				if err := env.Root.MkdirAll(dir, 0755); err != nil {
					return "", fmt.Errorf("failed to create directory: %w", err)
				}
			}

			file, err := env.Root.Create(pathArg)

			if err != nil {
				return "", fmt.Errorf("failed to create file: %w", err)
			}

			defer file.Close()

			if _, err := file.WriteString(content); err != nil {
				return "", fmt.Errorf("failed to write file: %w", err)
			}

			return fmt.Sprintf("Successfully wrote %d bytes to %s", len(content), pathArg), nil
		},
	}
}
