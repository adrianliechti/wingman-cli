package claw

import (
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/tui"
	"github.com/adrianliechti/wingman-agent/pkg/tui/markdown"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

const (
	indent   = "    "
	barWidth = 4 // "┃ " + padding
)

func (t *TUI) contentWidth() int {
	w := t.chatWidth - len(indent) - barWidth - 4 // 4 for right margin
	if w < 40 {
		return 40
	}

	return w
}

func (t *TUI) renderMessages(messages []agent.Message) {
	for _, msg := range messages {
		for _, c := range msg.Content {
			switch {
			case c.Text != "":
				switch msg.Role {
				case agent.RoleUser:
					t.writeFormatted(c.Text, false)
				case agent.RoleAssistant:
					t.writeFormatted(c.Text, true)
				}
			case c.ToolCall != nil:
				t.writeToolCall(c.ToolCall)
			}
		}
	}

	t.chatView.ScrollToEnd()
}

func (t *TUI) writeFormatted(text string, isAssistant bool) {
	th := theme.Default

	barColor := th.Cyan
	content := strings.TrimRight(text, "\n")

	if isAssistant {
		barColor = th.Blue
		content = strings.TrimRight(markdown.Render(content), "\n")
	}

	for line := range strings.SplitSeq(content, "\n") {
		for _, wl := range markdown.WrapLine(line, t.contentWidth()) {
			fmt.Fprintf(t.chatView, "%s[%s]\u2503[-] %s\n", indent, barColor, wl)
		}
	}

	fmt.Fprintln(t.chatView)
}

func (t *TUI) writeToolCall(tc *agent.ToolCall) {
	th := theme.Default
	hint := tui.ExtractToolHint(tc.Args)

	if hint != "" {
		hint = " " + hint
	}

	fmt.Fprintf(t.chatView, "%s[%s]\u2503[-] [%s::b]\u25c8 %s[-::-][%s]%s[-]\n",
		indent, th.BrBlack, th.Yellow, tc.Name, th.BrBlack, hint)
}

