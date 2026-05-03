package fs

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

const DefaultListLimit = 500

func LsTool(root *os.Root) tool.Tool {
	return tool.Tool{
		Name:   "ls",
		Effect: tool.StaticEffect(tool.EffectReadOnly),

		Description: strings.Join([]string{
			fmt.Sprintf(
				"List directory contents. Returns entries sorted alphabetically, with '/' suffix for directories. Includes dotfiles. Output is truncated to %d entries or %dKB (whichever is hit first).",
				DefaultListLimit,
				DefaultMaxBytes/1024,
			),
			"",
			"Usage:",
			"- Prefer `find` or `grep` when looking for specific files or content — they're more targeted.",
			"- Use `ls` only when you genuinely need to inspect a directory's immediate contents and don't yet know what's there.",
		}, "\n"),

		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":  map[string]any{"type": "string", "description": "Path to the directory to list (defaults to current directory)"},
				"limit": map[string]any{"type": "integer", "description": "Maximum number of entries to return"},
			},
		},

		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			pathArg := "."

			if p, ok := args["path"].(string); ok && p != "" {
				pathArg = p
			}

			workingDir := root.Name()

			normalizedPath, err := ensurePathInWorkspace(pathArg, workingDir, "list directory")

			if err != nil {
				return "", err
			}

			limit := DefaultListLimit

			if l, ok := args["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}

			info, err := root.Stat(normalizedPath)

			if err != nil {
				return "", pathError("stat path", pathArg, normalizedPath, workingDir, err)
			}

			if !info.IsDir() {
				return "", fmt.Errorf("path is not a directory: %s", pathArg)
			}

			dir, err := root.Open(normalizedPath)

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
			output, truncated := truncateHead(output)

			var notices []string

			if len(entries) > limit {
				notices = append(notices, fmt.Sprintf("%d entries limit reached", limit))
			}

			if truncated {
				notices = append(notices, fmt.Sprintf("%dKB limit reached", DefaultMaxBytes/1024))
			}

			if len(notices) > 0 {
				output += fmt.Sprintf("\n\n[%s]", strings.Join(notices, ". "))
			}

			return output, nil
		},
	}
}
