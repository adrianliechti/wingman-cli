package markdown

import (
	"fmt"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/rivo/tview"

	"github.com/adrianliechti/wingman-cli/pkg/theme"
)

func highlightCode(code, lang string) string {
	lexer := lexers.Get(lang)

	if lexer == nil {
		lexer = lexers.Fallback
	}

	lexer = chroma.Coalesce(lexer)

	styleName := "github-dark"

	if theme.Default.IsLight {
		styleName = "github"
	}

	style := styles.Get(styleName)

	if style == nil {
		return tview.Escape(code)
	}

	iterator, err := lexer.Tokenise(nil, code)

	if err != nil {
		return tview.Escape(code)
	}

	var result strings.Builder

	for _, token := range iterator.Tokens() {
		entry := style.Get(token.Type)
		text := tview.Escape(token.Value)

		if entry.Colour.IsSet() {
			fmt.Fprintf(&result, "[%s]%s[-]", entry.Colour.String(), text)
		} else {
			result.WriteString(text)
		}
	}

	return result.String()
}

func formatCodeBlock(code, lang string, t theme.Theme) string {
	highlighted := highlightCode(code, lang)
	lines := strings.Split(strings.TrimSuffix(highlighted, "\n"), "\n")

	var result strings.Builder

	result.WriteString("\n")

	for i, line := range lines {
		fmt.Fprintf(&result, "  [%s]%3d[%s]│[-] %s\n", t.BrBlack, i+1, t.BrBlack, line)
	}

	return result.String()
}

// HighlightDiff applies syntax highlighting to a unified diff string
// using theme-consistent colors for additions/deletions
func HighlightDiff(diff string) string {
	t := theme.Default
	lines := strings.Split(diff, "\n")

	var result strings.Builder

	for i, line := range lines {
		lineNum := fmt.Sprintf("[%s]%3d[-] ", t.BrBlack, i+1)

		if len(line) == 0 {
			result.WriteString(lineNum + "\n")
			continue
		}

		escaped := tview.Escape(line)

		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
			// File headers - bold foreground
			fmt.Fprintf(&result, "%s[%s::b]%s[-::-]\n", lineNum, t.Foreground, escaped)
		case strings.HasPrefix(line, "@@"):
			// Hunk headers - cyan
			fmt.Fprintf(&result, "%s[%s]%s[-]\n", lineNum, t.Cyan, escaped)
		case strings.HasPrefix(line, "+"):
			// Additions - green
			fmt.Fprintf(&result, "%s[%s]%s[-]\n", lineNum, t.Green, escaped)
		case strings.HasPrefix(line, "-"):
			// Deletions - red
			fmt.Fprintf(&result, "%s[%s]%s[-]\n", lineNum, t.Red, escaped)
		case strings.HasPrefix(line, "diff "), strings.HasPrefix(line, "index "):
			// Meta lines - dim
			fmt.Fprintf(&result, "%s[%s]%s[-]\n", lineNum, t.BrBlack, escaped)
		default:
			// Context lines - normal
			fmt.Fprintf(&result, "%s%s\n", lineNum, escaped)
		}
	}

	return strings.TrimSuffix(result.String(), "\n")
}
