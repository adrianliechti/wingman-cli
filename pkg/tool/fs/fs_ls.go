package fs

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/adrianliechti/wingman-cli/pkg/tool"
)

const DefaultListLimit = 500

func LsTool() tool.Tool {
	return tool.Tool{
		Name: "ls",

		Description: fmt.Sprintf(
			"List directory contents. Returns entries sorted alphabetically, with '/' suffix for directories. Includes dotfiles. Output is truncated to %d entries or %dKB (whichever is hit first).",
			DefaultListLimit,
			DefaultMaxBytes/1024,
		),

		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":  map[string]any{"type": "string", "description": "Path to the directory to list (defaults to current directory)"},
				"limit": map[string]any{"type": "integer", "description": "Maximum number of entries to return"},
			},
		},

		Execute: func(ctx context.Context, env *tool.Environment, args map[string]any) (string, error) {
			pathArg := "."

			if p, ok := args["path"].(string); ok && p != "" {
				pathArg = p
			}

			workingDir := env.WorkingDir()

			if isOutsideWorkspace(pathArg, workingDir) {
				return "", fmt.Errorf("cannot list directory: path %q is outside workspace %q", pathArg, workingDir)
			}

			normalizedPath := normalizePath(pathArg, workingDir)

			limit := DefaultListLimit

			if l, ok := args["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}

			info, err := env.Root.Stat(normalizedPath)

			if err != nil {
				return "", pathError("stat path", pathArg, normalizedPath, workingDir, err)
			}

			if !info.IsDir() {
				return "", fmt.Errorf("path is not a directory: %s", pathArg)
			}

			dir, err := env.Root.Open(normalizedPath)

			if err != nil {
				return "", pathError("open directory", pathArg, normalizedPath, workingDir, err)
			}
			defer dir.Close()

			entries, err := dir.ReadDir(-1)

			if err != nil {
				return "", fmt.Errorf("failed to read directory: %w", err)
			}

			if len(entries) == 0 {
				return "(empty directory)", nil
			}

			sort.Slice(entries, func(i, j int) bool {
				return entries[i].Name() < entries[j].Name()
			})

			var names []string

			for _, entry := range entries {
				if len(names) >= limit {
					break
				}

				name := entry.Name()

				if entry.IsDir() {
					name += "/"
				}

				names = append(names, name)
			}

			output := strings.Join(names, "\n")
			output, _, bytesTruncated := truncateHead(output)

			var notices []string

			if len(entries) > limit {
				notices = append(notices, fmt.Sprintf("%d entries limit reached", limit))
			}

			if bytesTruncated {
				notices = append(notices, fmt.Sprintf("%dKB limit reached", DefaultMaxBytes/1024))
			}

			if len(notices) > 0 {
				output += fmt.Sprintf("\n\n[%s]", strings.Join(notices, ". "))
			}

			return output, nil
		},
	}
}