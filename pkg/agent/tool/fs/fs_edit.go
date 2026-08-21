package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func EditTool(root *os.Root, allowedWriteRoots ...string) tool.Tool {
	return editTool(root, nil, nil, allowedWriteRoots...)
}

func editTool(root *os.Root, tracker *contentTracker, freshness *Freshness, allowedWriteRoots ...string) tool.Tool {
	return tool.Tool{
		Name:   "edit",
		Effect: tool.StaticEffect(tool.EffectMutates),

		Description: strings.Join([]string{
			"Performs exact string replacements in files.",
			"- You must `read` the file at least once in this conversation before editing it.",
			"- Preserve indentation exactly as it appears after the `read` line-number prefix (`42\\t...`); never include the prefix itself.",
			"- The edit FAILS if `old_string` is not unique in the file: add surrounding context (2-4 adjacent lines usually suffice) or set `replace_all` for intentional file-wide replacements like symbol renames.",
			"- Prefer `write` for new files.",
			"- For several changes to one file, pass `edits` (array of {old_string, new_string, replace_all}) in a single call. Edits apply in order — later ones see earlier results — and the call is atomic: if any edit fails, nothing is written.",
			"- The returned diff is authoritative — do not re-read the file to verify an edit; a failed edit writes nothing.",
		}, "\n"),

		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path":   map[string]any{"type": "string", "description": "The absolute path to the file to modify."},
				"old_string":  map[string]any{"type": "string", "description": "The text to replace. Must be unique unless replace_all=true. Use an empty string only to create a new file or replace an empty file. Omit when using edits."},
				"new_string":  map[string]any{"type": "string", "description": "The text to replace it with (must be different from old_string). Omit when using edits."},
				"replace_all": map[string]any{"type": "boolean", "description": "Replace all occurrences of old_string (default false).", "default": false},
				"edits": map[string]any{
					"type":        "array",
					"description": "Multiple replacements applied in order in a single atomic call. Use instead of old_string/new_string.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"old_string":  map[string]any{"type": "string", "description": "The text to replace. Must be unique unless replace_all=true."},
							"new_string":  map[string]any{"type": "string", "description": "The text to replace it with (must be different from old_string)."},
							"replace_all": map[string]any{"type": "boolean", "description": "Replace all occurrences of old_string (default false).", "default": false},
						},
						"required":             []string{"old_string", "new_string"},
						"additionalProperties": false,
					},
				},
			},
			"required":             []string{"file_path"},
			"additionalProperties": false,
		},

		Execute: func(ctx context.Context, args map[string]any) (tool.Result, error) {
			pathArg, ok := args["file_path"].(string)

			if !ok || pathArg == "" {
				return tool.Result{}, fmt.Errorf("file_path is required")
			}

			workingDir := root.Name()
			target, err := resolveFileTarget(pathArg, workingDir, allowedWriteRoots, "edit file")
			if err != nil {
				return tool.Result{}, err
			}

			ops, err := parseEditOps(args)
			if err != nil {
				return tool.Result{}, err
			}

			info, err := statFileTarget(root, target)
			exists := err == nil
			switch {
			case exists:
				if info.IsDir() {
					return tool.Result{}, fmt.Errorf("cannot edit file: path %q is a directory", pathArg)
				}
				if !info.Mode().IsRegular() {
					return tool.Result{}, fmt.Errorf("cannot edit file: path %q is not a regular file", pathArg)
				}
			case !os.IsNotExist(err):
				return tool.Result{}, fmt.Errorf("stat file %q: %w", pathArg, err)
			case ops[0].oldText != "":
				return tool.Result{}, fmt.Errorf("cannot edit %s: file does not exist", pathArg)
			}

			if exists && freshness.stale(ctx, target, info) {
				return tool.Result{}, fmt.Errorf("cannot edit %s: the file changed on disk after you last read it (edited externally or by another agent) — `read` it again first and take the changes into account", pathArg)
			}

			var contentBytes []byte
			if exists {
				contentBytes, err = readFileTarget(root, target)
				if err != nil {
					return tool.Result{}, fmt.Errorf("read file %q: %w", pathArg, err)
				}
			}

			if len(contentBytes) > MaxEditFileBytes {
				return tool.Result{}, fmt.Errorf("file %s is %d bytes; edits are capped at %d bytes — use `write` for full rewrites or narrow the change", pathArg, len(contentBytes), MaxEditFileBytes)
			}

			bom, content := stripBom(string(contentBytes))
			originalEnding := detectLineEnding(content)
			normalizedContent := normalizeToLF(content)

			newContent := normalizedContent
			for i, op := range ops {
				newContent, err = applyEditOp(newContent, op, pathArg)
				if err != nil {
					if len(ops) > 1 {
						return tool.Result{}, fmt.Errorf("edits[%d]: %w (no edits were applied)", i, err)
					}
					return tool.Result{}, err
				}
			}

			finalContent := bom + restoreLineEndings(newContent, originalEnding)

			if err := writeFileTarget(root, target, finalContent); err != nil {
				return tool.Result{}, fmt.Errorf("write file %q: %w", pathArg, err)
			}

			tracker.record([]byte(finalContent))
			freshness.record(ctx, target)

			diff := generateDiffString(normalizedContent, newContent)

			if len(ops) > 1 {
				return tool.Text(fmt.Sprintf("Successfully applied %d edits to %s.\n\n%s", len(ops), pathArg, diff)), nil
			}
			return tool.Text(fmt.Sprintf("Successfully replaced text in %s.\n\n%s", pathArg, diff)), nil
		},
	}
}

type editOp struct {
	oldText    string
	newText    string
	replaceAll bool
}

func parseEditOps(args map[string]any) ([]editOp, error) {
	rawEdits := args["edits"]

	if rawEdits == nil {
		op, err := newEditOp(args)
		if err != nil {
			return nil, err
		}
		return []editOp{op}, nil
	}

	_, hasOld := args["old_string"].(string)
	_, hasNew := args["new_string"].(string)
	if hasOld || hasNew {
		return nil, fmt.Errorf("provide either edits or old_string/new_string, not both")
	}
	if replaceAll, _ := args["replace_all"].(bool); replaceAll {
		return nil, fmt.Errorf("replace_all cannot be combined with edits; set it per edit entry instead")
	}

	list, ok := rawEdits.([]any)
	if !ok || len(list) == 0 {
		return nil, fmt.Errorf("edits must be a non-empty array of {old_string, new_string} objects")
	}

	ops := make([]editOp, 0, len(list))
	for i, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("edits[%d] must be an object with old_string and new_string", i)
		}
		op, err := newEditOp(entry)
		if err != nil {
			return nil, fmt.Errorf("edits[%d]: %w", i, err)
		}
		ops = append(ops, op)
	}
	return ops, nil
}

func newEditOp(entry map[string]any) (editOp, error) {
	oldText, ok := entry["old_string"].(string)
	if !ok {
		return editOp{}, fmt.Errorf("old_string is required")
	}

	newText, ok := entry["new_string"].(string)
	if !ok {
		return editOp{}, fmt.Errorf("new_string is required")
	}

	if oldText == newText {
		return editOp{}, fmt.Errorf("old_string and new_string are identical")
	}

	replaceAll, _ := entry["replace_all"].(bool)

	return editOp{
		oldText:    normalizeToLF(oldText),
		newText:    normalizeToLF(newText),
		replaceAll: replaceAll,
	}, nil
}

func applyEditOp(content string, op editOp, pathArg string) (string, error) {
	if op.oldText == "" {
		if strings.TrimSpace(content) != "" {
			return "", fmt.Errorf("cannot create or replace empty file %s: file already has content", pathArg)
		}
		return op.newText, nil
	}

	actualOldText := findActualEditString(content, op.oldText)
	actualNewText := preserveEditQuoteStyle(op.oldText, actualOldText, op.newText)

	matchResult := fuzzyFindText(content, actualOldText)

	if !matchResult.found {
		preview := content
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		return "", fmt.Errorf("could not find old_string in %s. Make sure it matches exactly (including whitespace and newlines). File starts with:\n%s", pathArg, preview)
	}

	occurrences := strings.Count(content, actualOldText)
	if matchResult.usedFuzzyMatch {
		occurrences = strings.Count(normalizeForFuzzyMatch(content), normalizeForFuzzyMatch(actualOldText))
	}

	if occurrences > 1 && !op.replaceAll {
		return "", fmt.Errorf("found %d occurrences of the text in %s. The text must be unique — provide more context to make it unique, or set replace_all=true to replace all occurrences", occurrences, pathArg)
	}

	var newContent string

	if op.replaceAll {
		if matchResult.usedFuzzyMatch {
			if strings.Contains(normalizeForFuzzyMatch(actualNewText), normalizeForFuzzyMatch(actualOldText)) {
				return "", fmt.Errorf("replace_all made no progress in %s. Use an exact old_string or a replacement that changes the matched text", pathArg)
			}
			newContent = content
			for {
				mr := fuzzyFindText(newContent, actualOldText)
				if !mr.found {
					break
				}
				next := newContent[:mr.index] + actualNewText + newContent[mr.index+mr.matchLength:]
				if next == newContent {
					return "", fmt.Errorf("replace_all made no progress in %s. Use an exact old_string or a replacement that changes the matched text", pathArg)
				}
				newContent = next
			}
		} else {
			newContent = strings.ReplaceAll(content, actualOldText, actualNewText)
		}
	} else {
		newContent = content[:matchResult.index] + actualNewText + content[matchResult.index+matchResult.matchLength:]
	}

	if content == newContent {
		return "", fmt.Errorf("no changes made to %s. The replacement produced identical content", pathArg)
	}

	return newContent, nil
}

func writeRootFile(root *os.Root, path, content string) (err error) {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := root.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}

	outFile, err := root.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := outFile.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("failed to close file: %w", closeErr)
		}
	}()

	if _, err := outFile.WriteString(content); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

func findActualEditString(content, oldText string) string {
	if strings.Contains(content, oldText) {
		return oldText
	}

	normContent, offsets := normalizeEditQuotesWithMap(content)
	normOld := normalizeEditQuotes(oldText)

	idx := strings.Index(normContent, normOld)
	if idx == -1 {
		return oldText
	}

	return content[offsets[idx]:offsets[idx+len(normOld)]]
}

var editQuoteReplacer = strings.NewReplacer(
	"‘", "'", "’", "'",
	"“", "\"", "”", "\"",
)

func normalizeEditQuotes(text string) string {
	return editQuoteReplacer.Replace(text)
}

func normalizeEditQuotesWithMap(text string) (string, []int) {
	var b strings.Builder
	b.Grow(len(text))
	offsets := make([]int, 0, len(text)+1)
	offsets = append(offsets, 0)

	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		switch r {
		case '‘', '’':
			b.WriteByte('\'')
			offsets = append(offsets, i+size)
		case '“', '”':
			b.WriteByte('"')
			offsets = append(offsets, i+size)
		default:
			b.WriteString(text[i : i+size])
			for j := 1; j <= size; j++ {
				offsets = append(offsets, i+j)
			}
		}
		i += size
	}
	return b.String(), offsets
}

func preserveEditQuoteStyle(oldText, actualOldText, newText string) string {
	if oldText == actualOldText {
		return newText
	}

	result := newText
	if strings.ContainsAny(actualOldText, "“”") {
		result = applyCurlyDoubleQuotes(result)
	}
	if strings.ContainsAny(actualOldText, "‘’") {
		result = applyCurlySingleQuotes(result)
	}
	return result
}

func applyCurlyDoubleQuotes(text string) string {
	var b strings.Builder
	for i, r := range text {
		if r != '"' {
			b.WriteRune(r)
			continue
		}
		if isOpeningQuoteContext(text, i) {
			b.WriteString("“")
		} else {
			b.WriteString("”")
		}
	}
	return b.String()
}

func applyCurlySingleQuotes(text string) string {
	runes := []rune(text)
	var b strings.Builder
	for i, r := range runes {
		if r != '\'' {
			b.WriteRune(r)
			continue
		}

		if i > 0 && i < len(runes)-1 && isLetter(runes[i-1]) && isLetter(runes[i+1]) {
			b.WriteString("’")
			continue
		}

		if isOpeningQuoteContextRunes(runes, i) {
			b.WriteString("‘")
		} else {
			b.WriteString("’")
		}
	}
	return b.String()
}

func isOpeningQuoteContext(text string, byteIndex int) bool {
	preceding := []rune(text[:byteIndex])
	return isOpeningQuoteContextRunes(preceding, len(preceding))
}

func isOpeningQuoteContextRunes(runes []rune, index int) bool {
	if index == 0 {
		return true
	}
	prev := runes[index-1]
	switch prev {
	case ' ', '\t', '\n', '\r', '(', '[', '{':
		return true
	default:
		return false
	}
}

func isLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}
