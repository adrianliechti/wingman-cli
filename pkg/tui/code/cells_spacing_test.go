package code

import (
	"slices"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/internal/testenv"
	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	codeagent "github.com/adrianliechti/wingman-agent/pkg/code/agent"
	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
)

func newTestWorkspace(t *testing.T, path string) *code.Workspace {
	t.Helper()
	testenv.UserHome(t)
	testenv.WingmanHome(t)

	workspace, err := code.NewWorkspace(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(workspace.Close)
	return workspace
}

func TestThoughtCellsGetSurroundingBlankLines(t *testing.T) {
	ws := newTestWorkspace(t, t.TempDir())

	a := &App{agent: codeagent.New(ws, &agent.Config{}, nil)}

	toolMsg := func(id string) agent.Message {
		return agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{
			{ToolResult: &agent.ToolResult{ID: id, Name: "read", Content: "ok"}},
		}}
	}
	thoughtMsg := func(id, summary string) agent.Message {
		return agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{
			{Reasoning: &agent.Reasoning{ID: id, Summary: summary}},
		}}
	}

	var lines []string
	for _, m := range []agent.Message{
		toolMsg("call_1"),
		thoughtMsg("rs_1", "**Considering the options**\nfull reasoning body"),
		thoughtMsg("rs_2", "**Settling on a plan**\nmore reasoning body"),
		toolMsg("call_2"),
	} {
		lines = append(lines, a.formatMessageCells(m, 80)...)
	}

	find := func(needle string) int {
		for i, l := range lines {
			if strings.Contains(l, needle) {
				return i
			}
		}
		t.Fatalf("%q missing from %q", needle, lines)
		return -1
	}

	first := find("Considering the options")
	second := find("Settling on a plan")

	if first == 0 || lines[first-1] != "" {
		t.Errorf("no blank line between tool and thought: %q", lines)
	}
	if second != first+1 {
		t.Errorf("one-line thought headings not tight: %q", lines)
	}
	if second == len(lines)-1 || lines[second+1] != "" {
		t.Errorf("no blank line between thought and tool: %q", lines)
	}
	if joined := strings.Join(lines, "\n"); strings.Contains(joined, "reasoning body") {
		t.Errorf("normal chat exposed full reasoning: %q", lines)
	}

	for i := 1; i < len(lines); i++ {
		if lines[i] == "" && lines[i-1] == "" {
			t.Errorf("double blank line at %d: %q", i, lines)
		}
	}
}

func TestStreamingReasoningUsesStableChatHeading(t *testing.T) {
	ws := newTestWorkspace(t, t.TempDir())

	a := &App{agent: codeagent.New(ws, &agent.Config{}, nil), queue: make(chan func(), 64), quit: make(chan struct{})}

	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{
		{Text: "streamed answer"},
	}})
	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{
		{Reasoning: &agent.Reasoning{ID: "rs_1", Summary: "**Planning the next step** more detail"}},
	}})
	a.setPhase(PhaseThinking)

	tail := ansi.Strip(strings.Join(a.streamCells(80), "\n"))
	if !strings.Contains(tail, "streamed answer") {
		t.Fatalf("tail lost streamed assistant text: %q", tail)
	}
	if !strings.Contains(tail, "Planning the next step") || strings.Contains(tail, "more detail") {
		t.Fatalf("tail did not isolate the stable reasoning heading: %q", tail)
	}
	footer := ansi.Strip(a.footerLine(80))
	if !strings.Contains(footer, "Thinking") || strings.Contains(footer, "Planning the next step") {
		t.Fatalf("footer did not remain generic activity: %q", footer)
	}
}

func TestReasoningHeadingWaitsForCompleteBoldText(t *testing.T) {
	a := &App{queue: make(chan func(), 64), quit: make(chan struct{})}
	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{
		Reasoning: &agent.Reasoning{ID: "reason-1", Summary: "**Checking"},
	}}})
	if tail := ansi.Strip(strings.Join(a.streamCells(80), "\n")); strings.Contains(tail, "Checking") {
		t.Fatalf("partial reasoning heading became visible: %q", tail)
	}

	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{
		Reasoning: &agent.Reasoning{ID: "reason-1", Summary: " files** and reading details"},
	}}})
	tail := ansi.Strip(strings.Join(a.streamCells(80), "\n"))
	if !strings.Contains(tail, "Checking files") || strings.Contains(tail, "reading details") {
		t.Fatalf("completed heading was not isolated from reasoning tokens: %q", tail)
	}
}

func TestReasoningHeadingsAccumulateAcrossPartBoundaries(t *testing.T) {
	a := &App{queue: make(chan func(), 64), quit: make(chan struct{})}
	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{
		Reasoning: &agent.Reasoning{ID: "reason-1", Part: 0, Summary: "**Inspecting files** body"},
	}}})
	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{
		Reasoning: &agent.Reasoning{ID: "reason-1", Part: 1, Summary: "**Planning changes** body"},
	}}})

	tail := ansi.Strip(strings.Join(a.streamCells(80), "\n"))
	if !strings.Contains(tail, "Inspecting files") || !strings.Contains(tail, "Planning changes") {
		t.Fatalf("reasoning headings were not preserved across parts: %q", tail)
	}
}

func TestReasoningHeadingIsSafeSingleLineText(t *testing.T) {
	got := extractReasoningHeader("**Checking\n\x1b[31mred\x1b[0m files** body")
	if got != "Checking red files" {
		t.Fatalf("unsafe reasoning heading = %q", got)
	}
}

func TestCommittedReasoningExtractsOnlyLineHeadings(t *testing.T) {
	summary := "**Inspecting files**\nbody with **important** detail\n\n**Planning changes**\nmore body"
	if got := extractReasoningHeadings(summary); got != "Inspecting files\nPlanning changes" {
		t.Fatalf("reasoning headings = %q", got)
	}
}

func TestStreamTailRetainsIntermediateACPCells(t *testing.T) {
	a := &App{queue: make(chan func(), 64), quit: make(chan struct{})}
	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{
		{Reasoning: &agent.Reasoning{ID: "reason-1", Summary: "checking the repository"}},
	}})
	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{
		{Reasoning: &agent.Reasoning{ID: "reason-2", Summary: "choosing the relevant file"}},
	}})
	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{
		{Text: "I found the relevant path."},
	}})
	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{
		{ToolCall: &agent.ToolCall{ID: "call-1", Name: "read", Args: `{"path":"one.go"}`}},
	}})
	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{
		{ToolResult: &agent.ToolResult{ID: "call-1", Name: "read", Content: "ok"}},
	}})
	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{
		{ToolCall: &agent.ToolCall{ID: "call-2", Name: "read", Args: `{"path":"two.go"}`}},
	}})

	tail := a.streamCells(100)
	joined := strings.Join(tail, "\n")
	for _, want := range []string{"I found the relevant path.", "one.go", "two.go"} {
		if !strings.Contains(joined, want) {
			t.Errorf("live turn lost %q: %q", want, tail)
		}
	}
	for _, hidden := range []string{"checking the repository", "choosing the relevant file"} {
		if strings.Contains(joined, hidden) {
			t.Errorf("reasoning token tape retained %q: %q", hidden, tail)
		}
	}
	if strings.Index(joined, "I found the relevant path.") > strings.Index(joined, "one.go") ||
		strings.Index(joined, "one.go") > strings.Index(joined, "two.go") {
		t.Errorf("live cells are out of event order: %q", tail)
	}
	for _, line := range tail {
		plain := ansi.Strip(line)
		if strings.Contains(plain, "one.go") && strings.Contains(plain, " …") {
			t.Errorf("completed ACP tool still looks active: %q", plain)
		}
		if strings.Contains(plain, "two.go") && !strings.Contains(plain, " …") {
			t.Errorf("active ACP tool lost its running marker: %q", plain)
		}
	}

	a.clearStreamingState()
	if tail := a.streamCells(100); len(tail) != 0 {
		t.Fatalf("completed turn retained live history: %q", tail)
	}
}

func TestWhitespaceOnlyTextRendersNothing(t *testing.T) {
	ws := newTestWorkspace(t, t.TempDir())

	a := &App{agent: codeagent.New(ws, &agent.Config{}, nil)}

	msg := agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{Text: "\n\n"}}}
	if lines := a.formatMessageCells(msg, 80); len(lines) != 0 {
		t.Fatalf("whitespace-only text rendered cells: %q", lines)
	}
}

func TestAnnotationsSurviveChatRebuild(t *testing.T) {
	ws := newTestWorkspace(t, t.TempDir())

	a := &App{agent: codeagent.New(ws, &agent.Config{}, nil)}

	a.annotations = append(a.annotations, chatAnnotation{
		afterMessages: 0,
		render: func(width int) []string {
			return []string{"resumed banner"}
		},
	})

	lines := a.restoreChatLines(a.agent.Messages(a.sessionID), 80)

	if !slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, "resumed banner") }) {
		t.Fatalf("annotation dropped on rebuild: %q", lines)
	}
}
