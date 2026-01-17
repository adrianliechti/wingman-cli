package markdown

import (
	"fmt"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"

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
		return code
	}

	iterator, err := lexer.Tokenise(nil, code)

	if err != nil {
		return code
	}

	var result strings.Builder

	for _, token := range iterator.Tokens() {
		entry := style.Get(token.Type)
		text := token.Value

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
