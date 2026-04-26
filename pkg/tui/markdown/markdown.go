package markdown

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"

	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

func Render(text string) string {
	t := theme.Default

	// Handle incomplete code blocks (streaming)
	// Count opening ``` markers that aren't closed
	completeText := text
	incompleteCode := ""
	incompleteLang := ""

	backtickCount := strings.Count(text, "```")

	if backtickCount%2 == 1 {
		// Find the last incomplete code block
		incompleteCodeBlockRe := regexp.MustCompile("(?s)```([\\w+#.-]*)\\n([^`]*)$")
		matches := incompleteCodeBlockRe.FindStringSubmatchIndex(text)

		if matches != nil {
			incompleteLang = text[matches[2]:matches[3]]
			incompleteCode = text[matches[4]:matches[5]]
			completeText = text[:matches[0]]
		}
	}

	// Create goldmark with GFM and custom tview renderer
	// Priority 100 ensures our renderer takes precedence over the default HTML renderer (1000)
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRenderer(
			renderer.NewRenderer(
				renderer.WithNodeRenderers(
					util.Prioritized(NewTviewRenderer(), 100),
				),
			),
		),
	)

	var buf bytes.Buffer

	if err := md.Convert([]byte(completeText), &buf); err != nil {
		// Fallback to original text on error
		return text
	}

	result := buf.String()

	// Append incomplete code block if present
	if incompleteCode != "" {
		result += formatCodeBlock(incompleteCode, incompleteLang, t)
	}

	return result
}
