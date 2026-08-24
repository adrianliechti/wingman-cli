package agent

import "testing"

func TestNewToolPresentationPreservesExecutionPayload(t *testing.T) {
	args := `{"command":"go test ./...","workdir":"pkg"}`
	presentation := NewToolPresentation("shell", "", args, nil)
	if presentation.Title != "Run command" || presentation.Hint != "go test ./..." || presentation.Args != `{"workdir":"pkg"}` {
		t.Fatalf("presentation = %#v", presentation)
	}

	message := toolCallMessage(ToolCall{ID: "call-1", Name: "shell", Args: args})
	call := message.Content[0].ToolCall
	if call.Name != "shell" || call.Args != args || call.Presentation == nil || call.Presentation.Hint != "go test ./..." {
		t.Fatalf("call = %#v", call)
	}
}
