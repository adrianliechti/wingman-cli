package markdown

import (
	"fmt"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"

	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

var (
	chromaMu         sync.Mutex
	chromaStyle      *chroma.Style
	chromaStyleTheme string
)

// codeStyle builds a chroma style from the active theme palette so code
// blocks match the rest of the UI.
func codeStyle() *chroma.Style {
	t := theme.Default

	chromaMu.Lock()
	defer chromaMu.Unlock()

	signature := t.Signature()
	if chromaStyle != nil && chromaStyleTheme == signature {
		return chromaStyle
	}

	hex := func(c interface{ Hex() int32 }) string {
		return fmt.Sprintf("#%06x", c.Hex())
	}

	builder := chroma.NewStyleBuilder("wingman")
	builder.Add(chroma.Comment, "italic "+hex(t.BrBlack))
	builder.Add(chroma.Keyword, hex(t.Blue))
	builder.Add(chroma.KeywordType, hex(t.Cyan))
	builder.Add(chroma.Operator, hex(t.Foreground))
	builder.Add(chroma.Punctuation, hex(t.Foreground))
	builder.Add(chroma.Name, hex(t.Foreground))
	builder.Add(chroma.NameFunction, hex(t.Cyan))
	builder.Add(chroma.NameClass, hex(t.Yellow))
	builder.Add(chroma.NameBuiltin, hex(t.Cyan))
	builder.Add(chroma.NameTag, hex(t.Blue))
	builder.Add(chroma.NameAttribute, hex(t.Cyan))
	builder.Add(chroma.NameDecorator, hex(t.Magenta))
	builder.Add(chroma.LiteralString, hex(t.Green))
	builder.Add(chroma.LiteralNumber, hex(t.Magenta))
	builder.Add(chroma.GenericDeleted, hex(t.Red))
	builder.Add(chroma.GenericInserted, hex(t.Green))

	style, err := builder.Build()
	if err != nil {
		return nil
	}

	chromaStyle = style
	chromaStyleTheme = signature

	return style
}

// Highlight returns ANSI syntax-highlighted source for lang, themed like
// chat code blocks.
func Highlight(code, lang string) string {
	return highlightCode(code, lang)
}

func highlightCode(code, lang string) string {
	lexer := lexers.Get(lang)

	if lexer == nil {
		lexer = lexers.Fallback
	}

	lexer = chroma.Coalesce(lexer)

	style := codeStyle()

	if style == nil {
		return sanitize(code)
	}

	iterator, err := lexer.Tokenise(nil, code)

	if err != nil {
		return sanitize(code)
	}

	var result strings.Builder

	for _, token := range iterator.Tokens() {
		entry := style.Get(token.Type)
		text := sanitize(token.Value)

		var sgr strings.Builder
		if entry.Colour.IsSet() {
			sgr.WriteString(ansi.Fg(hexColor(entry.Colour.String())))
		}
		if entry.Italic == chroma.Yes {
			sgr.WriteString(ansi.Italic)
		}
		if entry.Bold == chroma.Yes {
			sgr.WriteString(ansi.Bold)
		}

		if sgr.Len() > 0 {
			result.WriteString(sgr.String())
			result.WriteString(text)
			result.WriteString(ansi.Reset)
		} else {
			result.WriteString(text)
		}
	}

	return result.String()
}

func formatCodeBlock(code, lang string, t theme.Theme) string {
	highlighted := highlightCode(code, lang)
	lines := strings.Split(strings.TrimSuffix(highlighted, "\n"), "\n")

	dim := ansi.Fg(t.BrBlack)

	var result strings.Builder

	if lang != "" {
		fmt.Fprintf(&result, "%s%s%s\n", dim, sanitize(lang), ansi.Reset)
	}

	for i, line := range lines {
		fmt.Fprintf(&result, "%s%3d│%s %s\n", dim, i+1, ansi.Reset, line)
	}

	return result.String()
}

func HighlightDiff(diff string) string {
	t := theme.Default
	dim := ansi.Fg(t.BrBlack)
	lines := strings.Split(diff, "\n")

	var result strings.Builder

	for i, line := range lines {
		lineNum := fmt.Sprintf("%s%3d%s ", dim, i+1, ansi.Reset)

		if len(line) == 0 {
			result.WriteString(lineNum + "\n")
			continue
		}

		escaped := sanitize(line)

		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
			fmt.Fprintf(&result, "%s%s%s%s\n", lineNum, ansi.Bold, escaped, ansi.Reset)
		case strings.HasPrefix(line, "@@"):
			fmt.Fprintf(&result, "%s%s%s%s\n", lineNum, ansi.Fg(t.Cyan), escaped, ansi.Reset)
		case strings.HasPrefix(line, "+"):
			fmt.Fprintf(&result, "%s%s%s%s\n", lineNum, ansi.Fg(t.Green), escaped, ansi.Reset)
		case strings.HasPrefix(line, "-"):
			fmt.Fprintf(&result, "%s%s%s%s\n", lineNum, ansi.Fg(t.Red), escaped, ansi.Reset)
		case strings.HasPrefix(line, "diff "), strings.HasPrefix(line, "index "):
			fmt.Fprintf(&result, "%s%s%s%s\n", lineNum, dim, escaped, ansi.Reset)
		default:
			fmt.Fprintf(&result, "%s%s\n", lineNum, escaped)
		}
	}

	return strings.TrimSuffix(result.String(), "\n")
}
