package markdown

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/rivo/tview"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"

	"github.com/adrianliechti/wingman-cli/pkg/theme"
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
		incompleteCodeBlockRe := regexp.MustCompile("(?s)```(\\w*)\\n([^`]*)$")
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

func FormatUserMessage(content string, width int) string {
	const indent = "  "
	const barWidth = 4

	t := theme.Default
	contentWidth := width - len(indent) - barWidth

	var result strings.Builder

	for _, line := range strings.Split(content, "\n") {
		wrapped := wrapLine(line, contentWidth)

		for _, wl := range wrapped {
			fmt.Fprintf(&result, "%s[%s]┃[-] %s\n", indent, t.Cyan, wl)
		}
	}

	result.WriteString("\n")

	return result.String()
}

func FormatAssistantMessage(content string, width int) string {
	const indent = "  "
	const barWidth = 4

	t := theme.Default
	contentWidth := width - len(indent) - barWidth

	var result strings.Builder
	rendered := Render(content)

	for _, line := range strings.Split(rendered, "\n") {
		wrapped := wrapLine(line, contentWidth)

		for _, wl := range wrapped {
			fmt.Fprintf(&result, "%s[%s]┃[-] %s\n", indent, t.Blue, wl)
		}
	}

	result.WriteString("\n")

	return result.String()
}

func FormatPrompt(title string, message string, width int) string {
	const indent = "  "
	const barWidth = 4

	t := theme.Default
	contentWidth := width - len(indent) - barWidth

	var result strings.Builder

	titleLine := fmt.Sprintf("[%s::b]%s[-::-]", t.Yellow, title)

	for _, wl := range wrapLine(titleLine, contentWidth) {
		fmt.Fprintf(&result, "%s[%s]┃[-] %s\n", indent, t.Red, wl)
	}

	for _, line := range strings.Split(message, "\n") {
		wrapped := wrapLine(line, contentWidth)

		for _, wl := range wrapped {
			fmt.Fprintf(&result, "%s[%s]┃[-] %s\n", indent, t.Red, wl)
		}
	}

	hint := fmt.Sprintf("[%s]Press [-][%s::b]y[-::-][%s] to approve, [-][%s::b]n[-::-][%s] to deny[-]", t.BrBlack, t.Green, t.BrBlack, t.Red, t.BrBlack)
	fmt.Fprintf(&result, "%s[%s]┃[-] %s\n", indent, t.Red, hint)

	result.WriteString("\n")

	return result.String()
}

func FormatToolCall(name string, output string, width int) string {
	const indent = "  "
	const barWidth = 4

	t := theme.Default
	contentWidth := width - len(indent) - barWidth

	var result strings.Builder

	titleLine := fmt.Sprintf("[%s::b]⚡ %s[-::-]", t.Yellow, name)
	fmt.Fprintf(&result, "%s[%s]┃[-] %s\n", indent, t.Yellow, titleLine)

	for _, line := range strings.Split(output, "\n") {
		wrapped := wrapLine(line, contentWidth)

		for _, wl := range wrapped {
			escaped := tview.Escape(wl)
			fmt.Fprintf(&result, "%s[%s]┃[-] [%s]%s[-]\n", indent, t.Yellow, t.BrBlack, escaped)
		}
	}

	result.WriteString("\n")

	return result.String()
}

func FormatToolProgress(name string, width int) string {
	const indent = "  "

	t := theme.Default

	var result strings.Builder

	titleLine := fmt.Sprintf("[%s::b]⚡ %s[-::-] [%s]running...[-]", t.Yellow, name, t.BrBlack)
	fmt.Fprintf(&result, "%s[%s]┃[-] %s\n", indent, t.Yellow, titleLine)

	result.WriteString("\n")

	return result.String()
}

func FormatCompactionProgress(fromTokens int64, width int) string {
	const indent = "  "

	t := theme.Default

	var result strings.Builder

	titleLine := fmt.Sprintf("[%s::b]⚡ Compacting context[-::-] [%s](%d tokens)...[-]", t.Cyan, t.BrBlack, fromTokens)
	fmt.Fprintf(&result, "%s[%s]┃[-] %s\n", indent, t.Cyan, titleLine)

	result.WriteString("\n")

	return result.String()
}

func FormatCompaction(fromTokens, toTokens int64, width int) string {
	const indent = "  "

	t := theme.Default

	var result strings.Builder

	titleLine := fmt.Sprintf("[%s::b]⚡ Context compacted[-::-] [%s]%d → %d tokens[-]", t.Cyan, t.BrBlack, fromTokens, toTokens)
	fmt.Fprintf(&result, "%s[%s]┃[-] %s\n", indent, t.Cyan, titleLine)

	result.WriteString("\n")

	return result.String()
}

func FormatError(title string, message string, width int) string {
	const barWidth = 4

	t := theme.Default
	contentWidth := width - barWidth

	var result strings.Builder

	titleLine := fmt.Sprintf("[%s::b]⚠ %s[-::-]", t.Yellow, title)
	fmt.Fprintf(&result, "[%s]┃[-] %s\n", t.Red, titleLine)

	for _, line := range strings.Split(message, "\n") {
		if line == "" {
			continue
		}

		wrapped := wrapLine(fmt.Sprintf("[%s]%s[-]", t.BrBlack, line), contentWidth)

		for _, wl := range wrapped {
			fmt.Fprintf(&result, "[%s]┃[-] %s\n", t.Red, wl)
		}
	}

	result.WriteString("\n")

	return result.String()
}

func FormatStatus(message string, width int) string {
	t := theme.Default

	var result strings.Builder

	statusLine := fmt.Sprintf("[%s]%s[-]", t.Cyan, message)
	fmt.Fprintf(&result, "[%s]┃[-] %s\n", t.Cyan, statusLine)

	result.WriteString("\n")

	return result.String()
}
