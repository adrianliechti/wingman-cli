package fs

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func EditTool(root *os.Root) tool.Tool {
	return tool.Tool{
		Name:   "edit",
		Effect: tool.StaticEffect(tool.EffectMutates),

		Description: strings.Join([]string{
			"Performs exact string replacements in files. This is the preferred tool for modifying existing files.",
			"",
			"Usage:",
			"- You must use `read` at least once on a file before editing it.",
			"- When editing text from read output, preserve the exact indentation (tabs/spaces) as shown AFTER the line number prefix. Never include the line number prefix in old_text or new_text.",
			"- The edit will FAIL if old_text is not unique in the file. Either provide more surrounding context to make it unique, or use replace_all to change every occurrence.",
			"- Use replace_all for renaming variables, functions, or other identifiers across a file.",
			"- ALWAYS prefer editing existing files over writing new ones.",
			"- NEVER use the shell tool (sed, awk) for file edits — use this tool instead.",
		}, "\n"),

		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":        map[string]any{"type": "string", "description": "File path relative to the working directory"},
				"old_text":    map[string]any{"type": "string", "description": "Exact text to find and replace. Must be unique unless replace_all is true."},
				"new_text":    map[string]any{"type": "string", "description": "Text to replace the old text with. Must be different from old_text."},
				"replace_all": map[string]any{"type": "boolean", "description": "Replace all occurrences of old_text instead of just the first. Useful for renaming variables. (default: false)"},
			},
			"required": []string{"path", "old_text", "new_text"},
		},

		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			pathArg, ok := args["path"].(string)

			if !ok || pathArg == "" {
				return "", fmt.Errorf("path is required")
			}

			workingDir := root.Name()

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

			contentBytes, err := root.ReadFile(normalizedPath)

			if err != nil {
				return "", pathError("read file", pathArg, normalizedPath, workingDir, err)
			}

			bom, content := stripBom(string(contentBytes))
			originalEnding := detectLineEnding(content)
			normalizedContent := normalizeToLF(content)
			normalizedOldText := normalizeToLF(oldText)
			normalizedNewText := normalizeToLF(newText)

			replaceAll := false
			if ra, ok := args["replace_all"].(bool); ok {
				replaceAll = ra
			}

			matchResult := fuzzyFindText(normalizedContent, normalizedOldText)

			if !matchResult.found {
				// Provide a helpful snippet of what the file actually contains near the beginning
				preview := normalizedContent
				if len(preview) > 200 {
					preview = preview[:200] + "..."
				}
				return "", fmt.Errorf("could not find old_text in %s. Make sure it matches exactly (including whitespace and newlines). File starts with:\n%s", pathArg, preview)
			}

			fuzzyContent := normalizeForFuzzyMatch(normalizedContent)
			fuzzyOldText := normalizeForFuzzyMatch(normalizedOldText)
			occurrences := strings.Count(fuzzyContent, fuzzyOldText)

			if occurrences > 1 && !replaceAll {
				return "", fmt.Errorf("found %d occurrences of the text in %s. The text must be unique — provide more context to make it unique, or set replace_all=true to replace all occurrences", occurrences, pathArg)
			}

			baseContent := matchResult.contentForReplacement
			var newContent string

			if replaceAll {
				// For replace_all, use the fuzzy-normalized version to find all occurrences
				// but apply the replacement on the original content
				if matchResult.usedFuzzyMatch {
					// When fuzzy matching was used, we need to iteratively replace
					newContent = baseContent
					for {
						mr := fuzzyFindText(newContent, normalizedOldText)
						if !mr.found {
							break
						}
						newContent = mr.contentForReplacement[:mr.index] + normalizedNewText + mr.contentForReplacement[mr.index+mr.matchLength:]
					}
				} else {
					newContent = strings.ReplaceAll(baseContent, normalizedOldText, normalizedNewText)
				}
			} else {
				newContent = baseContent[:matchResult.index] + normalizedNewText + baseContent[matchResult.index+matchResult.matchLength:]
			}

			if baseContent == newContent {
				return "", fmt.Errorf("no changes made to %s. The replacement produced identical content", pathArg)
			}

			finalContent := bom + restoreLineEndings(newContent, originalEnding)

			outFile, err := root.Create(normalizedPath)

			if err != nil {
				return "", pathError("write file", pathArg, normalizedPath, workingDir, err)
			}
			if _, err := outFile.WriteString(finalContent); err != nil {
				outFile.Close()
				return "", fmt.Errorf("failed to write file: %w", err)
			}

			if err := outFile.Close(); err != nil {
				return "", fmt.Errorf("failed to close file: %w", err)
			}

			diff := generateDiffString(baseContent, newContent)

			result := fmt.Sprintf("Successfully replaced text in %s.\n\n%s", pathArg, diff)

			return result, nil
		},
	}
}
