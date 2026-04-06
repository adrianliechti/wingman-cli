package fs

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func WriteTool() tool.Tool {
	return tool.Tool{
		Name:            "write",
		ConcurrencySafe: false,

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
				"path":    map[string]any{"type": "string", "description": "Absolute path to the file to write"},
				"content": map[string]any{"type": "string", "description": "Complete content to write to the file"},
			},
			"required": []string{"path", "content"},
		},

		Execute: func(ctx context.Context, env *tool.Environment, args map[string]any) (string, error) {
			pathArg, ok := args["path"].(string)

			if !ok || pathArg == "" {
				return "", fmt.Errorf("path is required")
			}

			normalizedPath, root, err := resolveRoot(pathArg, env, "write file")

			if err != nil {
				return "", err
			}

			workingDir := env.WorkingDir()

			content, ok := args["content"].(string)

			if !ok {
				return "", fmt.Errorf("content is required")
			}

			if err := enforcePlanMutation(env, root, normalizedPath); err != nil {
				return "", err
			}

			// Check if file exists before writing (for create vs update reporting)
			existing, existsErr := root.ReadFile(normalizedPath)
			isNew := existsErr != nil

			if !isNew {
				if err := requireFreshFullRead(env, root, normalizedPath, string(existing)); err != nil {
					return "", err
				}
			}

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

			rememberRead(env, root, normalizedPath, []byte(content), false)

			action := "Updated"
			if isNew {
				action = "Created"
			}

			result := fmt.Sprintf("%s %s (%d bytes)", action, pathArg, len(content))

			if env.DiagnoseFile != nil {
				if diag := env.DiagnoseFile(ctx, normalizedPath); diag != "" {
					result += "\n\n" + diag
				}
			}

			return result, nil
		},
	}
}
