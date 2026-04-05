package app

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/adrianliechti/wingman-agent/pkg/ui/markdown"
	"github.com/adrianliechti/wingman-agent/pkg/ui/theme"
)

const (
	chatIndent   = "  "
	chatBarWidth = 4
)

func (a *App) contentWidth() int {
	return a.chatWidth - len(chatIndent) - chatBarWidth
}

func (a *App) formatUserMessage(content string) string {
	t := theme.Default

	var result strings.Builder

	for line := range strings.SplitSeq(strings.TrimRight(content, "\n"), "\n") {
		for _, wl := range markdown.WrapLine(line, a.contentWidth()) {
			fmt.Fprintf(&result, "%s[%s]┃[-] %s\n", chatIndent, t.Cyan, wl)
		}
	}

	result.WriteString("\n")

	return result.String()
}

func (a *App) formatAssistantMessage(content string) string {
	t := theme.Default

	var result strings.Builder
	rendered := strings.TrimRight(markdown.Render(content), "\n")

	for line := range strings.SplitSeq(rendered, "\n") {
		for _, wl := range markdown.WrapLine(line, a.contentWidth()) {
			fmt.Fprintf(&result, "%s[%s]┃[-] %s\n", chatIndent, t.Blue, wl)
		}
	}

	result.WriteString("\n")

	return result.String()
}

func (a *App) formatPrompt(title string, message string, hint string) string {
	t := theme.Default

	var result strings.Builder

	titleLine := fmt.Sprintf("[%s::b]%s[-::-]", t.Yellow, title)

	for _, wl := range markdown.WrapLine(titleLine, a.contentWidth()) {
		fmt.Fprintf(&result, "%s[%s]┃[-] %s\n", chatIndent, t.Red, wl)
	}

	fmt.Fprintf(&result, "%s[%s]┃[-]\n", chatIndent, t.Red)

	for line := range strings.SplitSeq(message, "\n") {
		for _, wl := range markdown.WrapLine(line, a.contentWidth()) {
			fmt.Fprintf(&result, "%s[%s]┃[-] %s\n", chatIndent, t.Red, wl)
		}
	}

	if hint != "" {
		fmt.Fprintf(&result, "%s[%s]┃[-] %s\n", chatIndent, t.Red, hint)
	}

	result.WriteString("\n")

	return result.String()
}

// toolDisplay returns the icon and label for a tool.
func toolDisplay(name string) (string, string) {
	if name == "agent" {
		return "🤖", ""
	}

	return "⚡", name
}

func (a *App) toolHintSpace(label string) int {
	prefixLen := len(chatIndent) + chatBarWidth + 2 // bar + space + icon
	if label != "" {
		prefixLen += 1 + len(label)
	}

	return a.chatWidth - prefixLen - 2
}

func truncateHint(hint string, maxLen int) string {
	if maxLen <= 3 || hint == "" {
		return ""
	}

	if len(hint) <= maxLen {
		return hint
	}

	return hint[:maxLen-3] + "..."
}

func (a *App) formatToolTitle(icon, label, hint, color string, bold bool) string {
	hint = truncateHint(hint, a.toolHintSpace(label))
	t := theme.Default

	style := ""
	styleEnd := "[-]"
	if bold {
		style = "::b"
		styleEnd = "[-::-]"
	}

	var title string

	if label != "" {
		title = fmt.Sprintf("[%s%s]%s %s%s", color, style, icon, label, styleEnd)
	} else {
		title = fmt.Sprintf("[%s%s]%s%s", color, style, icon, styleEnd)
	}

	if hint != "" {
		title += fmt.Sprintf(" [%s]%s[-]", t.BrBlack, tview.Escape(hint))
	}

	return title
}

func (a *App) formatToolCall(name string, hint string, output string) string {
	t := theme.Default
	icon, label := toolDisplay(name)

	var result strings.Builder

	title := a.formatToolTitle(icon, label, hint, t.Yellow.String(), true)
	fmt.Fprintf(&result, "%s[%s]┃[-] %s\n", chatIndent, t.Yellow, title)

	for line := range strings.SplitSeq(strings.TrimRight(output, "\n"), "\n") {
		for _, wl := range markdown.WrapLine(line, a.contentWidth()) {
			fmt.Fprintf(&result, "%s[%s]┃[-] [%s]%s[-]\n", chatIndent, t.Yellow, t.BrBlack, tview.Escape(wl))
		}
	}

	result.WriteString("\n")

	return result.String()
}

func (a *App) formatToolCallCollapsed(name string, hint string) string {
	t := theme.Default
	icon, label := toolDisplay(name)

	title := a.formatToolTitle(icon, label, hint, t.BrBlack.String(), false)

	return fmt.Sprintf("%s[%s]┃[-] %s\n", chatIndent, t.BrBlack, title)
}

func (a *App) formatToolProgress(name string, hint string) string {
	t := theme.Default
	icon, label := toolDisplay(name)

	if hint == "" {
		hint = "running..."
	}

	title := a.formatToolTitle(icon, label, hint, t.Yellow.String(), true)

	return fmt.Sprintf("%s[%s]┃[-] %s\n\n", chatIndent, t.Yellow, title)
}

func (a *App) formatNotice(message string, color tcell.Color) string {
	return fmt.Sprintf("%s[%s]┃[-] [%s]%s[-]\n\n", chatIndent, color, color, message)
}

func (a *App) formatError(title string, message string) string {
	t := theme.Default

	var result strings.Builder

	titleLine := fmt.Sprintf("[%s::b]⚠ %s[-::-]", t.Yellow, title)
	fmt.Fprintf(&result, "[%s]┃[-] %s\n", t.Red, titleLine)

	for line := range strings.SplitSeq(message, "\n") {
		if line == "" {
			continue
		}

		for _, wl := range markdown.WrapLine(fmt.Sprintf("[%s]%s[-]", t.BrBlack, line), a.contentWidth()) {
			fmt.Fprintf(&result, "[%s]┃[-] %s\n", t.Red, wl)
		}
	}

	result.WriteString("\n")

	return result.String()
}
