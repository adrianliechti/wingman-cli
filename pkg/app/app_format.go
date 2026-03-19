package app

import (
	"fmt"
	"strings"

	"github.com/rivo/tview"

	"github.com/adrianliechti/wingman-agent/pkg/ui/markdown"
	"github.com/adrianliechti/wingman-agent/pkg/ui/theme"
)

func formatUserMessage(content string, width int) string {
	const indent = "  "
	const barWidth = 4

	t := theme.Default
	contentWidth := width - len(indent) - barWidth

	var result strings.Builder

	for line := range strings.SplitSeq(content, "\n") {
		wrapped := markdown.WrapLine(line, contentWidth)

		for _, wl := range wrapped {
			fmt.Fprintf(&result, "%s[%s]┃[-] %s\n", indent, t.Cyan, wl)
		}
	}

	result.WriteString("\n")

	return result.String()
}

func formatAssistantMessage(content string, width int) string {
	const indent = "  "
	const barWidth = 4

	t := theme.Default
	contentWidth := width - len(indent) - barWidth

	var result strings.Builder
	rendered := markdown.Render(content)

	for line := range strings.SplitSeq(rendered, "\n") {
		wrapped := markdown.WrapLine(line, contentWidth)

		for _, wl := range wrapped {
			fmt.Fprintf(&result, "%s[%s]┃[-] %s\n", indent, t.Blue, wl)
		}
	}

	result.WriteString("\n")

	return result.String()
}

func formatPrompt(title string, message string, width int) string {
	const indent = "  "
	const barWidth = 4

	t := theme.Default
	contentWidth := width - len(indent) - barWidth

	var result strings.Builder

	titleLine := fmt.Sprintf("[%s::b]%s[-::-]", t.Yellow, title)

	for _, wl := range markdown.WrapLine(titleLine, contentWidth) {
		fmt.Fprintf(&result, "%s[%s]┃[-] %s\n", indent, t.Red, wl)
	}

	for line := range strings.SplitSeq(message, "\n") {
		wrapped := markdown.WrapLine(line, contentWidth)

		for _, wl := range wrapped {
			fmt.Fprintf(&result, "%s[%s]┃[-] %s\n", indent, t.Red, wl)
		}
	}

	hint := fmt.Sprintf("[%s]Press [-][%s::b]y[-::-][%s] to approve, [-][%s::b]n[-::-][%s] to deny[-]", t.BrBlack, t.Green, t.BrBlack, t.Red, t.BrBlack)
	fmt.Fprintf(&result, "%s[%s]┃[-] %s\n", indent, t.Red, hint)

	result.WriteString("\n")

	return result.String()
}

func formatToolCall(name string, hint string, output string, width int) string {
	const indent = "  "
	const barWidth = 4

	t := theme.Default
	contentWidth := width - len(indent) - barWidth

	var result strings.Builder

	titleLine := fmt.Sprintf("[%s::b]⚡ %s[-::-]", t.Yellow, name)

	if hint != "" {
		titleLine = fmt.Sprintf("[%s::b]⚡ %s[-::-] [%s]%s[-]", t.Yellow, name, t.BrBlack, tview.Escape(hint))
	}
	fmt.Fprintf(&result, "%s[%s]┃[-] %s\n", indent, t.Yellow, titleLine)

	for line := range strings.SplitSeq(output, "\n") {
		wrapped := markdown.WrapLine(line, contentWidth)

		for _, wl := range wrapped {
			escaped := tview.Escape(wl)
			fmt.Fprintf(&result, "%s[%s]┃[-] [%s]%s[-]\n", indent, t.Yellow, t.BrBlack, escaped)
		}
	}

	result.WriteString("\n")

	return result.String()
}

func formatToolProgress(name string, hint string, width int) string {
	const indent = "  "

	t := theme.Default

	var result strings.Builder

	titleLine := fmt.Sprintf("[%s::b]⚡ %s[-::-] [%s]running...[-]", t.Yellow, name, t.BrBlack)

	if hint != "" {
		titleLine = fmt.Sprintf("[%s::b]⚡ %s[-::-] [%s]%s[-]", t.Yellow, name, t.BrBlack, tview.Escape(hint))
	}
	fmt.Fprintf(&result, "%s[%s]┃[-] %s\n", indent, t.Yellow, titleLine)

	result.WriteString("\n")

	return result.String()
}

func formatError(title string, message string, width int) string {
	const barWidth = 4

	t := theme.Default
	contentWidth := width - barWidth

	var result strings.Builder

	titleLine := fmt.Sprintf("[%s::b]⚠ %s[-::-]", t.Yellow, title)
	fmt.Fprintf(&result, "[%s]┃[-] %s\n", t.Red, titleLine)

	for line := range strings.SplitSeq(message, "\n") {
		if line == "" {
			continue
		}

		wrapped := markdown.WrapLine(fmt.Sprintf("[%s]%s[-]", t.BrBlack, line), contentWidth)

		for _, wl := range wrapped {
			fmt.Fprintf(&result, "[%s]┃[-] %s\n", t.Red, wl)
		}
	}

	result.WriteString("\n")

	return result.String()
}
