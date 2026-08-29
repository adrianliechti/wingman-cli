package agent

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func TestContextCheckpointPreservesCanonicalHistory(t *testing.T) {
	a := &Agent{Config: &Config{}}
	if err := a.appendMessages(
		Message{Role: RoleUser, Content: []Content{{Text: "original question"}}},
		Message{Role: RoleAssistant, Content: []Content{{Text: "original answer"}}},
	); err != nil {
		t.Fatal(err)
	}
	if err := a.replaceContext("test compaction", []Message{
		{Role: RoleUser, Hidden: true, Content: []Content{{Text: "summary"}}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.appendMessages(Message{Role: RoleUser, Content: []Content{{Text: "follow-up"}}}); err != nil {
		t.Fatal(err)
	}

	canonical := a.MessagesSnapshot()
	if len(canonical) != 3 || canonical[0].Content[0].Text != "original question" {
		t.Fatalf("canonical history was rewritten: %#v", canonical)
	}
	context := a.requestMessages()
	if len(context) != 2 || context[0].Content[0].Text != "summary" || context[1].Content[0].Text != "follow-up" {
		t.Fatalf("model context = %#v", context)
	}

	var restored Agent
	if err := restored.Restore(a.StateSnapshot()); err != nil {
		t.Fatal(err)
	}
	if got := restored.MessagesSnapshot(); len(got) != 3 || got[0].Content[0].Text != "original question" {
		t.Fatalf("restored canonical history = %#v", got)
	}
	if got := restored.requestMessages(); len(got) != 2 || got[0].Content[0].Text != "summary" {
		t.Fatalf("restored context = %#v", got)
	}
}

func TestRecorderFailureDoesNotApplyEvent(t *testing.T) {
	a := &Agent{
		Config: &Config{},
		Recorder: EventRecorderFunc(func([]RuntimeEvent) error {
			return errors.New("disk full")
		}),
	}
	if err := a.appendMessages(Message{Role: RoleUser, Content: []Content{{Text: "lost"}}}); err == nil {
		t.Fatal("expected recorder failure")
	}
	if len(a.Events) != 0 || len(a.Messages) != 0 {
		t.Fatalf("failed event changed state: events=%#v messages=%d", a.Events, len(a.Messages))
	}
}

func TestReconcileInterruptedLifecycleAndToolUncertainty(t *testing.T) {
	a := &Agent{Config: &Config{}}
	if err := a.recordEvents(
		RuntimeEvent{Type: EventTurnStarted, TurnID: "turn"},
		RuntimeEvent{Type: EventRunStarted, TurnID: "turn", RunID: "run"},
		RuntimeEvent{Type: EventToolStarted, TurnID: "turn", OperationID: "read", Tool: &RuntimeTool{Name: "read", Effect: "read_only"}},
		RuntimeEvent{Type: EventToolStarted, TurnID: "turn", OperationID: "write", Tool: &RuntimeTool{Name: "write", Effect: "mutates"}},
	); err != nil {
		t.Fatal(err)
	}
	if err := a.ReconcileInterrupted("restart"); err != nil {
		t.Fatal(err)
	}

	terminals := map[string]RuntimeTerminal{}
	for _, event := range a.StateSnapshot().Events {
		if event.Terminal != nil {
			terminals[runtimeEntityID(event)] = *event.Terminal
		}
	}
	for _, id := range []string{"turn", "run", "read", "write"} {
		if terminals[id].Status != RuntimeInterrupted {
			t.Fatalf("%s terminal = %#v", id, terminals[id])
		}
	}
	if terminals["read"].OutcomeUncertain {
		t.Fatal("read-only tool must not be marked side-effect uncertain")
	}
	if !terminals["write"].OutcomeUncertain {
		t.Fatal("open mutating tool must be marked side-effect uncertain")
	}

	count := len(a.StateSnapshot().Events)
	if err := a.ReconcileInterrupted("again"); err != nil {
		t.Fatal(err)
	}
	if len(a.StateSnapshot().Events) != count {
		t.Fatal("reconciliation appended duplicate terminal facts")
	}
}

func TestRuntimeRejectsDuplicateTerminalFact(t *testing.T) {
	a := &Agent{Config: &Config{}}
	if err := a.recordEvents(
		RuntimeEvent{Type: EventTurnStarted, TurnID: "turn"},
		RuntimeEvent{Type: EventTurnTerminal, TurnID: "turn", Terminal: &RuntimeTerminal{Status: RuntimeCompleted}},
	); err != nil {
		t.Fatal(err)
	}
	if err := a.recordEvents(RuntimeEvent{
		Type: EventTurnTerminal, TurnID: "turn", Terminal: &RuntimeTerminal{Status: RuntimeFailed},
	}); err == nil {
		t.Fatal("duplicate terminal fact was accepted")
	}
}

func TestProviderOutputPrecedesRunTerminalInSameAppend(t *testing.T) {
	client := streamingTestClient(func(*http.Request) string {
		return "data: {\"type\":\"response.completed\",\"sequence_number\":1,\"response\":{\"output\":[{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"durable answer\",\"annotations\":[]}]}],\"usage\":{\"input_tokens\":1,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":2}}}\n\n"
	})
	var batches [][]RuntimeEvent
	a := &Agent{Config: &Config{client: &client}, Recorder: EventRecorderFunc(func(events []RuntimeEvent) error {
		batches = append(batches, cloneRuntimeEvents(events))
		return nil
	})}
	if err := a.recordEvents(RuntimeEvent{Type: EventTurnStarted, TurnID: "turn"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.completeRun(context.Background(), "turn", &request{}, yieldAll); err != nil {
		t.Fatal(err)
	}

	last := batches[len(batches)-1]
	if len(last) != 2 || last[0].Type != EventMessage || last[1].Type != EventRunTerminal {
		t.Fatalf("provider commit batch = %#v", last)
	}
	if got := last[0].Message.Content[0].Text; got != "durable answer" {
		t.Fatalf("durable provider output = %q", got)
	}
}

func TestToolResultPrecedesTerminalInSameAppend(t *testing.T) {
	var batches [][]RuntimeEvent
	a := &Agent{Config: &Config{ToolTimeout: -1}, Recorder: EventRecorderFunc(func(events []RuntimeEvent) error {
		batches = append(batches, cloneRuntimeEvents(events))
		return nil
	})}
	result := a.runSingleToolCall(context.Background(), ToolCall{ID: "call", Name: "write"}, []tool.Tool{{
		Name: "write", Effect: tool.StaticEffect(tool.EffectMutates),
		Execute: func(context.Context, map[string]any) (tool.Result, error) {
			return tool.Text("written"), nil
		},
	}})
	if result.IsError {
		t.Fatalf("tool result = %#v", result)
	}

	last := batches[len(batches)-1]
	if len(last) != 2 || last[0].Type != EventMessage || last[1].Type != EventToolTerminal {
		t.Fatalf("tool commit batch = %#v", last)
	}
	if got := last[0].Message.Content[0].ToolResult.Content; got != "written" {
		t.Fatalf("durable tool output = %q", got)
	}
}

func TestRuntimeRejectsDuplicateEventID(t *testing.T) {
	a := &Agent{Config: &Config{}}
	if err := a.recordEvents(RuntimeEvent{ID: "fact", Type: EventTurnStarted, TurnID: "one"}); err != nil {
		t.Fatal(err)
	}
	if err := a.recordEvents(RuntimeEvent{ID: "fact", Type: EventTurnStarted, TurnID: "two"}); err == nil {
		t.Fatal("duplicate event id was accepted")
	}
}
