package fs

import (
	"context"
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman-cli/pkg/tool"
)

func EditTool() tool.Tool {
	return tool.Tool{
		Name: "edit",

		Description: "Edit a file by replacing exact text. The oldText must match exactly (including whitespace). Use this for precise, surgical edits.",

		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":     map[string]any{"type": "string", "description": "Path to the file to edit"},
				"old_text": map[string]any{"type": "string", "description": "Exact text to find and replace"},
				"new_text": map[string]any{"type": "string", "description": "Text to replace the old text with"},
			},
			"required": []string{"path", "old_text", "new_text"},
		},

		Execute: func(ctx context.Context, env *tool.Environment, args map[string]any) (string, error) {
			pathArg, ok := args["path"].(string)

			if !ok || pathArg == "" {
				return "", fmt.Errorf("path is required")
			}

			workingDir := env.WorkingDir()

			normalizedPath, err := ensurePathInWorkspace(pathArg, workingDir, "edit file")

			if err != nil {
				return "", err
			}

			oldText, ok := args["old_text"].(string)

			if !ok || oldText == "" {
				return "", fmt.Errorf("old_text is required")
			}

			newText, ok := args["new_text"].(string)

			if !ok {
				return "", fmt.Errorf("new_text is required")
			}

			contentBytes, err := env.Root.ReadFile(normalizedPath)

			if err != nil {
				return "", pathError("read file", pathArg, normalizedPath, workingDir, err)
			}

			rawContent := string(contentBytes)

			bom, content := stripBom(rawContent)
			originalEnding := detectLineEnding(content)
			normalizedContent := normalizeToLF(content)
			normalizedOldText := normalizeToLF(oldText)
			normalizedNewText := normalizeToLF(newText)

			matchResult := fuzzyFindText(normalizedContent, normalizedOldText)

			if !matchResult.found {
				return "", fmt.Errorf("could not find the exact text in %s. The old text must match exactly including all whitespace and newlines", pathArg)
			}

			fuzzyContent := normalizeForFuzzyMatch(normalizedContent)
			fuzzyOldText := normalizeForFuzzyMatch(normalizedOldText)
			occurrences := strings.Count(fuzzyContent, fuzzyOldText)

			if occurrences > 1 {
				return "", fmt.Errorf("found %d occurrences of the text in %s. The text must be unique. Please provide more context to make it unique", occurrences, pathArg)
			}

			baseContent := matchResult.contentForReplacement
			newContent := baseContent[:matchResult.index] + normalizedNewText + baseContent[matchResult.index+matchResult.matchLength:]

			if baseContent == newContent {
				return "", fmt.Errorf("no changes made to %s. The replacement produced identical content", pathArg)
			}

			finalContent := bom + restoreLineEndings(newContent, originalEnding)

			outFile, err := env.Root.Create(normalizedPath)

			if err != nil {
				return "", pathError("write file", pathArg, normalizedPath, workingDir, err)
			}
			defer outFile.Close()

			if _, err := outFile.WriteString(finalContent); err != nil {
				return "", fmt.Errorf("failed to write file: %w", err)
			}

			diff := generateDiffString(baseContent, newContent)

			return fmt.Sprintf("Successfully replaced text in %s.\n\n%s", pathArg, diff), nil
		},
	}
}
