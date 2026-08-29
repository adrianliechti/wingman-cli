package agent

import (
	"strings"
	"testing"
)

func trimTestAgent(messages []Message) *Agent {
	return &Agent{Config: &Config{}, Messages: messages}
}

func toolExchange(id, result string) []Message {
	return []Message{
		{Role: RoleAssistant, Content: []Content{{ToolCall: &ToolCall{ID: id, Name: "read"}}}},
		{Role: RoleAssistant, Content: []Content{{ToolResult: &ToolResult{ID: id, Name: "read", Content: result}}}},
	}
}

func TestTrimStaleToolResults(t *testing.T) {
	big := strings.Repeat("x", 8*1024)

	var messages []Message
	messages = append(messages, Message{Role: RoleUser, Content: []Content{{Text: "task"}}})
	for range 20 {
		messages = append(messages, toolExchange("call", big)...)
	}

	a := trimTestAgent(messages)
	freed, err := a.trimStaleToolResults()
	if err != nil {
		t.Fatal(err)
	}
	if freed == 0 {
		t.Fatal("expected bytes freed")
	}
	if a.ContextRevision == 0 {
		t.Fatal("expected context revision bump")
	}

	context := a.requestMessages()
	var trimmed, intact int
	for _, m := range context {
		for _, c := range m.Content {
			if c.ToolResult == nil {
				continue
			}
			if strings.Contains(c.ToolResult.Content, "trimmed to reclaim context") {
				trimmed++
			} else if len(c.ToolResult.Content) == len(big) {
				intact++
			}
		}
	}
	if trimmed == 0 {
		t.Fatal("no tool results were trimmed")
	}
	if intact == 0 {
		t.Fatal("recent tool results must stay intact")
	}

	last := context[len(context)-1]
	if got := last.Content[0].ToolResult.Content; len(got) != len(big) {
		t.Fatalf("newest tool result was trimmed (len %d)", len(got))
	}

	if freed, err := a.trimStaleToolResults(); err != nil || freed != 0 {
		t.Fatal("second trim should be a no-op")
	}
	if strings.Contains(a.Messages[2].Content[0].ToolResult.Content, "trimmed to reclaim context") {
		t.Fatal("trimming model context must not rewrite canonical history")
	}
}

func TestTrimStaleToolResultsDropsImages(t *testing.T) {
	big := strings.Repeat("x", 8*1024)

	var messages []Message
	messages = append(messages, Message{Role: RoleAssistant, Content: []Content{
		{ToolResult: &ToolResult{ID: "img", Name: "view_image", Content: "[image attached below]"}},
		{File: &File{Data: strings.Repeat("A", 64*1024)}},
	}})
	for range 20 {
		messages = append(messages, toolExchange("call", big)...)
	}

	a := trimTestAgent(messages)
	if freed, err := a.trimStaleToolResults(); err != nil || freed == 0 {
		t.Fatal("expected bytes freed")
	}

	first := a.requestMessages()[0]
	for _, c := range first.Content {
		if c.File != nil {
			t.Fatal("old image data must be dropped")
		}
	}
	if !strings.Contains(first.Content[0].ToolResult.Content, "image result trimmed") {
		t.Fatalf("image tool result = %q", first.Content[0].ToolResult.Content)
	}
}

func TestTrimStaleToolResultsCheckpointsEmptyImage(t *testing.T) {
	var messages []Message
	messages = append(messages, Message{Role: RoleAssistant, Content: []Content{
		{ToolResult: &ToolResult{ID: "img", Name: "view_image", Content: "[image attached below]"}},
		{File: &File{}},
	}})
	for range 20 {
		messages = append(messages, Message{
			Role:    RoleUser,
			Content: []Content{{Text: strings.Repeat("x", 8*1024)}},
		})
	}

	a := trimTestAgent(messages)
	if freed, err := a.trimStaleToolResults(); err != nil || freed != 0 {
		t.Fatalf("freed = %d, want 0 for an empty image", freed)
	}
	if a.ContextRevision != 1 {
		t.Fatalf("context revision = %d, want 1 after projection changed", a.ContextRevision)
	}

	first := a.requestMessages()[0]
	if len(first.Content) != 1 || first.Content[0].File != nil {
		t.Fatalf("empty image was not dropped: %#v", first.Content)
	}
	if got := first.Content[0].ToolResult.Content; got != trimImageMarker {
		t.Fatalf("tool result = %q, want %q", got, trimImageMarker)
	}
	if freed, err := a.trimStaleToolResults(); err != nil || freed != 0 {
		t.Fatalf("second trim freed %d bytes, want 0", freed)
	}
	if a.ContextRevision != 1 {
		t.Fatalf("context revision = %d after no-op trim, want 1", a.ContextRevision)
	}
}

func TestTrimStaleToolResultsProtectsSmallSessions(t *testing.T) {
	a := trimTestAgent(toolExchange("call", strings.Repeat("x", 8*1024)))
	if freed, err := a.trimStaleToolResults(); err != nil || freed != 0 {
		t.Fatalf("freed %d bytes from a small session", freed)
	}
}
