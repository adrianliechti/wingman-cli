package server

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/code"
)

func TestTurnQueueEntryPreservesEditableInput(t *testing.T) {
	meta := ClientMessage{
		ID: "input-1", Intent: string(code.TurnInputFollowUp), Text: "fix it",
		Files: []string{"main.go"}, Images: []string{"data:image/png;base64,abc"},
	}
	entry := turnQueueEntry(meta, code.TurnInputQueued, 2)
	meta.Files[0] = "mutated"
	meta.Images[0] = "mutated"

	if entry.ID != "input-1" || entry.State != "queued" || entry.Position != 2 || entry.Text != "fix it" {
		t.Fatalf("entry = %#v", entry)
	}
	if entry.Files[0] != "main.go" || entry.Images[0] != "data:image/png;base64,abc" {
		t.Fatalf("attachments = %#v", entry)
	}
}

func TestTurnInputFrameCarriesSingleInputPayload(t *testing.T) {
	frame := turnInputFrame("input-1", ClientMessage{
		Intent: string(code.TurnInputSteer), Text: "guide",
		Files: []string{"main.go"}, Images: []string{"image"},
	}, code.TurnInputQueued, 2, errors.New("waiting"))

	if frame.Input == nil {
		t.Fatal("input payload is missing")
	}
	entry := frame.Input
	if entry.ID != "input-1" || entry.State != "queued" || entry.Intent != "steer" ||
		entry.Position != 2 || entry.Text != "guide" {
		t.Fatalf("input = %+v", entry)
	}
	if frame.Message != "waiting" || len(entry.Files) != 1 || len(entry.Images) != 1 {
		t.Fatalf("frame metadata = %+v", frame)
	}

	b, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []string{"id", "state", "intent", "position", "text", "queue"} {
		if _, ok := fields[legacy]; ok {
			t.Errorf("legacy duplicate field %q is still present: %s", legacy, b)
		}
	}
}

func TestTurnQueueFrameJSONCarriesCapabilitiesAndOrdering(t *testing.T) {
	frame := Frame{
		Type: EvtTurnQueue, Session: "session-1", Paused: true, CanSteer: true,
		Queue: []TurnQueueEntry{{ID: "input-2", State: "queued", Intent: "follow_up", Position: 1, Text: "next"}},
	}
	b, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Type     string           `json:"type"`
		Session  string           `json:"session"`
		Paused   bool             `json:"paused"`
		CanSteer bool             `json:"can_steer"`
		Queue    []TurnQueueEntry `json:"queue"`
	}
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Type != EvtTurnQueue || decoded.Session != "session-1" || !decoded.Paused || !decoded.CanSteer {
		t.Fatalf("frame = %#v", decoded)
	}
	if len(decoded.Queue) != 1 || decoded.Queue[0].ID != "input-2" || decoded.Queue[0].Position != 1 {
		t.Fatalf("queue = %#v", decoded.Queue)
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
