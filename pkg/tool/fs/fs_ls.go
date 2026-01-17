package fs

import (
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

		Execute: func(env *tool.Environment, args map[string]any) (string, error) {
			pathArg := "."
			if p, ok := args["path"].(string); ok && p != "" {
				pathArg = p
			}

			limit := DefaultListLimit
			if l, ok := args["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}

			info, err := env.Root.Stat(pathArg)
			if err != nil {
				return "", fmt.Errorf("path not found: %s", pathArg)
			}

			if !info.IsDir() {
				return "", fmt.Errorf("path is not a directory: %s", pathArg)
			}

			dir, err := env.Root.Open(pathArg)
			if err != nil {
				return "", fmt.Errorf("failed to open directory: %w", err)
			}
			defer dir.Close()

			entries, err := dir.ReadDir(-1)
			if err != nil {
				return "", fmt.Errorf("failed to read directory: %w", err)
			}

			if len(entries) == 0 {
				return "(empty directory)", nil
			}

			var results []string
			entryLimitReached := false

			for i, entry := range entries {
				if i >= limit {
					entryLimitReached = true
					break
				}

				name := entry.Name()
				if entry.IsDir() {
					name += "/"
				}
				results = append(results, name)
			}

			sort.Strings(results)

			rawOutput := strings.Join(results, "\n")
			truncatedOutput, _, bytesTruncated := truncateHead(rawOutput)

			var notices []string
			if entryLimitReached {
				notices = append(notices, fmt.Sprintf("%d entries limit reached. Use limit=%d for more", limit, limit*2))
			}

			if bytesTruncated {
				notices = append(notices, fmt.Sprintf("%dKB limit reached", DefaultMaxBytes/1024))
			}

			if len(notices) > 0 {
				truncatedOutput += fmt.Sprintf("\n\n[%s]", strings.Join(notices, ". "))
			}

			return truncatedOutput, nil
		},
	}
}
