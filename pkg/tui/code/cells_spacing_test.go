package code

import (
	"slices"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	codeagent "github.com/adrianliechti/wingman-agent/pkg/code/agent"
)

func TestThoughtCellsGetSurroundingBlankLines(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

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

	long := strings.Repeat("weighing the tradeoffs carefully ", 6)

	var lines []string
	for _, m := range []agent.Message{
		toolMsg("call_1"),
		thoughtMsg("rs_1", "considering the options"),
		thoughtMsg("rs_2", long),
		thoughtMsg("rs_3", "settling on a plan"),
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

	first := find("considering the options")
	second := find("weighing the tradeoffs")
	third := find("settling on a plan")

	if first == 0 || lines[first-1] != "" {
		t.Errorf("no blank line between tool and thought: %q", lines)
	}
	if second != first+1 {
		t.Errorf("one-line thoughts not tight: %q", lines)
	}
	if lines[third-1] != "" {
		t.Errorf("no blank line after multi-line thought: %q", lines)
	}
	if third == len(lines)-1 || lines[third+1] != "" {
		t.Errorf("no blank line between thought and tool: %q", lines)
	}

	for i := 1; i < len(lines); i++ {
		if lines[i] == "" && lines[i-1] == "" {
			t.Errorf("double blank line at %d: %q", i, lines)
		}
	}
}

func TestStreamTailFollowsWorkOrder(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	a := &App{agent: codeagent.New(ws, &agent.Config{}, nil), queue: make(chan func(), 64), quit: make(chan struct{})}

	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{
		{Text: "streamed answer"},
	}})
	a.handleStreamMessage(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{
		{Reasoning: &agent.Reasoning{ID: "rs_1", Summary: "planning the next step"}},
	}})

	tail := a.streamCells(80)

	textIdx, thoughtIdx := -1, -1
	for i, l := range tail {
		if strings.Contains(l, "streamed answer") {
			textIdx = i
		}
		if strings.Contains(l, "planning the next step") {
			thoughtIdx = i
		}
	}

	if textIdx < 0 || thoughtIdx < 0 {
		t.Fatalf("tail missing cells: %q", tail)
	}
	if thoughtIdx < textIdx {
		t.Errorf("thought rendered above older streamed text: %q", tail)
	}
	if thoughtIdx != textIdx+2 || tail[textIdx+1] != "" {
		t.Errorf("no single blank line between text and thought: %q", tail)
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
	for _, want := range []string{"checking the repository", "choosing the relevant file", "I found the relevant path.", "one.go", "two.go"} {
		if !strings.Contains(joined, want) {
			t.Errorf("live turn lost %q: %q", want, tail)
		}
	}
	if strings.Index(joined, "checking the repository") > strings.Index(joined, "choosing the relevant file") ||
		strings.Index(joined, "choosing the relevant file") > strings.Index(joined, "I found the relevant path.") ||
		strings.Index(joined, "I found the relevant path.") > strings.Index(joined, "one.go") ||
		strings.Index(joined, "one.go") > strings.Index(joined, "two.go") {
		t.Errorf("live cells are out of event order: %q", tail)
	}

	a.clearStreamingState()
	if tail := a.streamCells(100); len(tail) != 0 {
		t.Fatalf("completed turn retained live history: %q", tail)
	}
}

func TestWhitespaceOnlyTextRendersNothing(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	a := &App{agent: codeagent.New(ws, &agent.Config{}, nil)}

	msg := agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{Text: "\n\n"}}}
	if lines := a.formatMessageCells(msg, 80); len(lines) != 0 {
		t.Fatalf("whitespace-only text rendered cells: %q", lines)
	}
}

func TestAnnotationsSurviveChatRebuild(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	a := &App{agent: codeagent.New(ws, &agent.Config{}, nil)}

	a.annotations = append(a.annotations, chatAnnotation{
		afterMessages: 0,
		render: func(width int) []string {
			return []string{"resumed banner"}
		},
	})

	lines := a.restoreChatLines(80)

	if !slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, "resumed banner") }) {
		t.Fatalf("annotation dropped on rebuild: %q", lines)
	}
}
