package code

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
	"github.com/adrianliechti/wingman-agent/pkg/tui/inline"
)

func TestTranscriptInspectorSearchAndExpansion(t *testing.T) {
	messages := []agent.Message{
		{Role: agent.RoleUser, Content: []agent.Content{{Text: "find the needle"}}},
		{Role: agent.RoleAssistant, Content: []agent.Content{
			{ToolResult: &agent.ToolResult{ID: "tool-1", Name: "read", Args: `{"path":"file.go"}`, Content: "one\ntwo\nthree\nfour\nfive"}},
			{Reasoning: &agent.Reasoning{ID: "reason-1", Summary: "checking the needle carefully"}},
			{Text: "done"},
		}},
	}
	a := &App{ctx: context.Background(), agent: newUITestAgent(messages), sessionID: "session"}
	o := &transcriptOverlay{app: a, selected: -1, follow: true, expanded: map[string]bool{}, cache: map[string]transcriptCache{}}
	o.buildEntries()
	if len(o.entries) != 4 {
		t.Fatalf("entries = %d, want 4", len(o.entries))
	}

	var toolIndex int
	for i, entry := range o.entries {
		if entry.kind == transcriptTool {
			toolIndex = i
			break
		}
	}
	o.selected = toolIndex
	collapsed := o.entryLines(o.entries[toolIndex], 80, true)
	o.toggleSelected()
	expanded := o.entryLines(o.entries[toolIndex], 80, true)
	if len(expanded) <= len(collapsed) {
		t.Fatalf("expanded tool has %d lines, collapsed has %d", len(expanded), len(collapsed))
	}

	o.query = "needle"
	o.updateMatches(true)
	if len(o.matches) != 2 {
		t.Fatalf("matches = %d, want user + reasoning", len(o.matches))
	}

	for _, width := range []int{40, 80, 120} {
		for i, line := range o.Render(width, 18) {
			if ansi.Width(line) > width {
				t.Fatalf("width %d row %d overflowed to %d", width, i, ansi.Width(line))
			}
		}
	}
}

func TestTranscriptUsesChatCellSpacing(t *testing.T) {
	longThought := strings.Repeat("weighing the tradeoffs carefully ", 6)
	messages := []agent.Message{
		{Role: agent.RoleAssistant, Content: []agent.Content{{ToolResult: &agent.ToolResult{
			ID: "tool-1", Name: "read", Content: "ok",
		}}}},
		{Role: agent.RoleAssistant, Content: []agent.Content{{Reasoning: &agent.Reasoning{
			ID: "reason-1", Summary: "considering the options",
		}}}},
		{Role: agent.RoleAssistant, Content: []agent.Content{{Reasoning: &agent.Reasoning{
			ID: "reason-2", Summary: longThought,
		}}}},
		{Role: agent.RoleAssistant, Content: []agent.Content{{Reasoning: &agent.Reasoning{
			ID: "reason-3", Summary: "settling on a plan",
		}}}},
		{Role: agent.RoleAssistant, Content: []agent.Content{{ToolResult: &agent.ToolResult{
			ID: "tool-2", Name: "shell", Content: "one\ntwo\nthree\nfour",
		}}}},
		{Role: agent.RoleAssistant, Content: []agent.Content{{Text: "done"}}},
	}

	const transcriptWidth = 80
	a := &App{ctx: context.Background(), agent: newUITestAgent(messages), sessionID: "session"}
	var chat []string
	for _, message := range messages {
		chat = append(chat, a.formatMessageCells(message, transcriptWidth-2)...)
	}

	o := &transcriptOverlay{
		app: a, selected: -1, expanded: map[string]bool{}, cache: map[string]transcriptCache{},
	}
	o.buildEntries()
	for _, entry := range o.entries {
		if entry.kind == transcriptReasoning {
			o.expanded[entry.key] = true
		}
	}
	// Remove selection styling so only the transcript's fixed two-column
	// navigation gutter differs from the normal chat cells.
	o.selected = -1
	transcript, _, _ := o.bodyLines(transcriptWidth)
	for i, line := range transcript {
		if strings.HasPrefix(line, "  ") {
			transcript[i] = line[2:]
		}
	}

	if !slices.Equal(transcript, chat) {
		t.Fatalf("transcript spacing drifted from chat\ntranscript: %q\nchat:       %q", transcript, chat)
	}
}

func TestTranscriptLiveTailOrderAndVanishedSelection(t *testing.T) {
	messages := []agent.Message{
		{Role: agent.RoleUser, Content: []agent.Content{{Text: "prompt one"}}},
		{Role: agent.RoleAssistant, Content: []agent.Content{{Text: "answer one"}}},
	}
	a := &App{ctx: context.Background(), agent: newUITestAgent(messages), sessionID: "session"}
	a.streamingText = "partial answer"
	a.streamingReasoning = "weighing options"

	o := &transcriptOverlay{app: a, selected: -1, follow: true, expanded: map[string]bool{}, cache: map[string]transcriptCache{}}
	o.buildEntries()

	last := len(o.entries) - 1
	if o.entries[last-1].key != "live:0:text" || o.entries[last].key != "live:0:reasoning" {
		t.Fatalf("live tail = %q, %q; want text before reasoning", o.entries[last-1].key, o.entries[last].key)
	}

	o.moveSelection(-1)
	o.moveSelection(1)
	if o.follow || o.entries[o.selected].key != "live:0:reasoning" {
		t.Fatalf("setup: follow=%v key=%q", o.follow, o.entries[o.selected].key)
	}

	a.streamingText = ""
	a.streamingReasoning = ""
	a.currentToolName = "shell"
	a.currentToolHint = "ls"
	o.buildEntries()

	if o.selected != o.lastSelectable() {
		t.Fatalf("vanished selection fell back to %d, want last selectable %d", o.selected, o.lastSelectable())
	}
}

func TestTranscriptTallEntryLineScroll(t *testing.T) {
	output := strings.TrimSpace(strings.Repeat("line\n", 60))
	messages := []agent.Message{
		{Role: agent.RoleUser, Content: []agent.Content{{Text: "run it"}}},
		{Role: agent.RoleAssistant, Content: []agent.Content{
			{ToolResult: &agent.ToolResult{ID: "tool-1", Name: "shell", Args: `{"command":"seq 60"}`, Content: output}},
			{Text: "done"},
		}},
	}
	a := &App{ctx: context.Background(), agent: newUITestAgent(messages), sessionID: "session"}
	o := &transcriptOverlay{app: a, selected: -1, follow: true, expanded: map[string]bool{}, cache: map[string]transcriptCache{}}
	o.buildEntries()

	toolIndex := -1
	for i, entry := range o.entries {
		if entry.kind == transcriptTool {
			toolIndex = i
		}
	}
	o.selected = toolIndex
	o.follow = false
	o.toggleSelected()

	const height = 20
	o.Render(80, height)
	if o.lineMax == 0 {
		t.Fatal("expanded tool entry is not taller than the viewport")
	}
	_, starts, _ := o.bodyLines(80)
	if o.offset != starts[toolIndex] {
		t.Fatalf("tall entry not anchored to its top: offset=%d start=%d", o.offset, starts[toolIndex])
	}

	o.step(1)
	o.Render(80, height)
	if o.selected != toolIndex || o.offset != starts[toolIndex]+1 {
		t.Fatalf("step did not line-scroll: selected=%d offset=%d", o.selected, o.offset)
	}

	for i := 0; o.selected == toolIndex && i < 200; i++ {
		o.step(1)
		o.Render(80, height)
	}
	if o.selected == toolIndex {
		t.Fatal("stepping past the entry did not advance the selection")
	}
	if o.lineOffset != 0 {
		t.Fatalf("lineOffset = %d after selection change", o.lineOffset)
	}
}

func TestTranscriptShortOverlayKeys(t *testing.T) {
	messages := []agent.Message{
		{Role: agent.RoleUser, Content: []agent.Content{{Text: "one"}}},
		{Role: agent.RoleAssistant, Content: []agent.Content{{Text: "two"}}},
	}
	a := &App{ctx: context.Background(), agent: newUITestAgent(messages), sessionID: "session"}
	o := &transcriptOverlay{app: a, selected: -1, follow: true, expanded: map[string]bool{}, cache: map[string]transcriptCache{}}
	o.Render(40, 5)

	o.selected = o.firstSelectable()
	o.follow = false
	o.HandleKey(inline.KeyEvent{Key: inline.KeyPgDn})
	if o.selected == o.firstSelectable() {
		t.Fatal("pgdn on a short overlay did not move the selection")
	}

	o.HandleKey(inline.KeyEvent{Key: inline.KeyRune, Rune: '/'})
	if !o.searching {
		t.Fatal("search did not start")
	}
	if !o.HandleKey(inline.KeyEvent{Key: inline.KeyCtrl, Rune: 'c'}) {
		t.Fatal("ctrl+c during search did not close the transcript")
	}
}
