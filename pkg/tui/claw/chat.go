package claw

import (
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/claw"
	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
	"github.com/adrianliechti/wingman-agent/pkg/tui/markdown"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

func formatMessages(messages []agent.Message, width int) []string {
	var lines []string

	for _, msg := range messages {
		if msg.Hidden {
			continue
		}

		for _, c := range msg.Content {
			switch {
			case c.Text != "":
				switch msg.Role {
				case agent.RoleUser:
					lines = append(lines, formatChatMessage(claw.Unframe(c.Text), false, width)...)
				case agent.RoleAssistant:
					lines = append(lines, formatChatMessage(c.Text, true, width)...)
				}
			case c.ToolCall != nil:
				lines = append(lines, formatToolCall(c.ToolCall, width))
			}
		}
	}

	return lines
}

func formatChatMessage(text string, isAssistant bool, width int) []string {
	th := theme.Default

	content := strings.TrimRight(text, "\n")

	var lines []string

	if isAssistant {
		content = strings.TrimRight(markdown.Render(content), "\n")
		for line := range strings.SplitSeq(content, "\n") {
			for _, wl := range markdown.WrapLine(line, width-len(indent)) {
				lines = append(lines, indent+wl)
			}
		}
	} else {
		for i, line := range strings.Split(markdown.Sanitize(content), "\n") {
			prefix := "  "
			if i == 0 {
				prefix = ansi.Fg(th.BrBlack) + "› " + ansi.Reset
			}
			for j, wl := range markdown.WrapLine(line, width-len(indent)-2) {
				p := prefix
				if j > 0 {
					p = "  "
				}
				lines = append(lines, indent+p+ansi.Fg(th.Cyan)+wl+ansi.Reset)
			}
		}
	}

	lines = append(lines, "")
	return lines
}

func formatStreamLine(line string, width int) []string {
	var lines []string
	for _, wl := range markdown.WrapLine(markdown.Sanitize(line), width-len(indent)) {
		lines = append(lines, indent+wl)
	}
	return lines
}

func formatToolCall(tc *agent.ToolCall, width int) string {
	th := theme.Default
	hint := tool.ExtractHint(tc.Args, tc.Name)

	line := indent + ansi.Fg(th.BrBlack) + "• " + ansi.Reset + ansi.Bold + tc.Name + ansi.Reset
	if hint != "" {
		line += " " + ansi.Fg(th.BrBlack) + markdown.Sanitize(hint) + ansi.Reset
	}

	return ansi.Truncate(line, width, "…")
}
