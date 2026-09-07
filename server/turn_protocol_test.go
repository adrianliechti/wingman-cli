package server

import (
	"encoding/json"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/code"
)

func TestTurnQueueEntryPreservesEditableInput(t *testing.T) {
	input := code.TurnInput{ID: "input-1", Intent: code.TurnInputFollowUp, Display: &code.TurnInputDisplay{Text: "fix it", Files: []string{"main.go"}, Images: []string{"image"}}}
	entry := turnQueueEntry(input, code.TurnInputQueued, 2)
	input.Display.Files[0] = "mutated"
	if entry.ID != "input-1" || entry.Text != "fix it" || entry.Files[0] != "main.go" || entry.Position != 2 {
		t.Fatalf("entry = %#v", entry)
	}
}

func TestToolCallFrameCarriesPartialState(t *testing.T) {
	b, err := json.Marshal(Frame{
		Type: EvtToolCall, ID: "call-1", Kind: "edit", Partial: true,
		Locations: []agent.ToolLocation{{Path: "/workspace/main.go", Line: 8}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Kind      string               `json:"kind"`
		Partial   bool                 `json:"partial"`
		Locations []agent.ToolLocation `json:"locations"`
	}
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Kind != "edit" || !decoded.Partial || len(decoded.Locations) != 1 || decoded.Locations[0].Line != 8 {
		t.Fatalf("frame = %s", b)
	}
}

func TestSnapshotHasActive(t *testing.T) {
	if snapshotHasActive(code.TurnSnapshot{Inputs: []code.TurnInputSnapshot{{State: code.TurnInputQueued}}}) {
		t.Fatal("queued-only snapshot reported active")
	}
	if !snapshotHasActive(code.TurnSnapshot{Inputs: []code.TurnInputSnapshot{{State: code.TurnInputActive}}}) {
		t.Fatal("active snapshot reported idle")
	}
}

func TestConvertMessagesPreservesAssistantTextIdentity(t *testing.T) {
	messages := convertMessages([]agent.Message{{
		Role: agent.RoleAssistant,
		Content: []agent.Content{{
			Text:   "answer",
			TextID: "message-1",
		}},
	}})

	if len(messages) != 1 || len(messages[0].Content) != 1 {
		t.Fatalf("messages = %+v", messages)
	}
	if got := messages[0].Content[0]; got.Text != "answer" || got.TextID != "message-1" {
		t.Fatalf("content = %+v", got)
	}
}

func TestConvertMessagesPreservesToolKind(t *testing.T) {
	messages := convertMessages([]agent.Message{{
		Role: agent.RoleAssistant,
		Content: []agent.Content{
			{ToolCall: &agent.ToolCall{ID: "edit-1", Name: "Editing files", Kind: "edit", Locations: []agent.ToolLocation{{Path: "/workspace/main.go"}}}},
			{ToolResult: &agent.ToolResult{ID: "edit-1", Name: "Editing files", Kind: "edit", Locations: []agent.ToolLocation{{Path: "/workspace/main.go"}}, Content: "+new"}},
		},
	}})

	if len(messages) != 1 || len(messages[0].Content) != 2 {
		t.Fatalf("messages = %+v", messages)
	}
	if got := messages[0].Content[0].ToolCall; got == nil || got.Kind != "edit" || len(got.Locations) != 1 {
		t.Fatalf("tool call = %+v", got)
	}
	if got := messages[0].Content[1].ToolResult; got == nil || got.Kind != "edit" || len(got.Locations) != 1 {
		t.Fatalf("tool result = %+v", got)
	}
}
