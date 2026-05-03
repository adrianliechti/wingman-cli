package code

import (
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

// turn groups a user message with everything the agent did before its next
// final answer. Reasoning, tool results, and intermediate assistant text all
// land in `working`; the latest assistant message that actually produced text
// becomes `final`. This mirrors the web UI's grouping so finished turns can
// be rendered as `user → ▸ 1 thought, 20 tools → final`.
type turn struct {
	user    *agent.Message
	working []agent.Message
	final   *agent.Message
}

func buildTurns(messages []agent.Message) []turn {
	var turns []turn
	var cur turn

	flush := func() {
		if cur.user != nil || len(cur.working) > 0 || cur.final != nil {
			turns = append(turns, cur)
		}
		cur = turn{}
	}

	for i := range messages {
		m := &messages[i]
		if m.Hidden || m.Role == agent.RoleSystem {
			continue
		}
		if m.Role == agent.RoleUser {
			flush()
			cur.user = m
			continue
		}

		hasText := false
		for _, c := range m.Content {
			if c.Text != "" {
				hasText = true
				break
			}
		}
		if hasText {
			// New text-bearing assistant message — bump the previous final (if
			// any) into working so only the latest counts as "the answer".
			if cur.final != nil {
				cur.working = append(cur.working, *cur.final)
			}
			cur.final = m
		} else {
			cur.working = append(cur.working, *m)
		}
	}
	flush()

	return turns
}

func (t *turn) workCounts() (tools, thoughts int) {
	for _, m := range t.working {
		for _, c := range m.Content {
			if c.ToolResult != nil {
				tools++
			}
			if c.Reasoning != nil && c.Reasoning.Summary != "" {
				thoughts++
			}
		}
	}
	return
}

func (a *App) formatTurnSummary(t *turn) string {
	th := theme.Default
	tools, thoughts := t.workCounts()

	var parts []string
	if thoughts > 0 {
		s := ""
		if thoughts != 1 {
			s = "s"
		}
		parts = append(parts, fmt.Sprintf("%d thought%s", thoughts, s))
	}
	if tools > 0 {
		s := ""
		if tools != 1 {
			s = "s"
		}
		parts = append(parts, fmt.Sprintf("%d tool%s", tools, s))
	}

	summary := "Worked"
	if len(parts) > 0 {
		summary = strings.Join(parts, ", ")
	}

	return fmt.Sprintf("%s[%s]┃[-] [%s::i]▸ %s[-::-]\n\n", chatIndent, th.BrBlack, th.BrBlack, summary)
}
