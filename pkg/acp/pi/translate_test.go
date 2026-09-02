package pi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/coder/acp-go-sdk"
)

func TestParseAvailableModels(t *testing.T) {
	data := json.RawMessage(`{"models":[
		{"provider":"wingman","id":"claude-sonnet-5","name":"Claude Sonnet 5"},
		{"provider":"wingman","id":"gpt-5.5"},
		{"provider":"","id":"skip"},
		{"provider":"x","id":""}
	]}`)

	got := parseAvailableModels(data)
	if len(got) != 2 {
		t.Fatalf("expected 2 models, got %d (%+v)", len(got), got)
	}
	if got[0].ID != "wingman/claude-sonnet-5" || got[0].Name != "wingman/Claude Sonnet 5" {
		t.Errorf("model[0] = %+v", got[0])
	}
	if got[1].ID != "wingman/gpt-5.5" || got[1].Name != "wingman/gpt-5.5" {
		t.Errorf("model[1] = %+v (name should fall back to id)", got[1])
	}
}

func TestParseState(t *testing.T) {
	s := parseState(json.RawMessage(`{"sessionId":"abc","thinkingLevel":"high","model":{"provider":"wingman","id":"gpt-5.5"}}`))
	if s.SessionID != "abc" {
		t.Errorf("sessionId = %q", s.SessionID)
	}
	if s.currentModel() != "wingman/gpt-5.5" {
		t.Errorf("currentModel = %q", s.currentModel())
	}
	if s.thinking() != "high" {
		t.Errorf("thinking = %q", s.thinking())
	}

	empty := parseState(json.RawMessage(`{}`))
	if empty.currentModel() != "" {
		t.Errorf("empty currentModel = %q", empty.currentModel())
	}
	if empty.thinking() != defaultThinkingLevel {
		t.Errorf("empty thinking = %q, want default", empty.thinking())
	}
}

func TestParseAvailableThinkingLevels(t *testing.T) {
	got := parseAvailableThinkingLevels(json.RawMessage(`{"levels":["off","high","max","high",""]}`))
	want := []string{"off", "high", "max"}
	if len(got) != len(want) {
		t.Fatalf("levels = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("levels = %v, want %v", got, want)
		}
	}
}

func TestToolResultToText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"diff wins", `{"content":[{"type":"text","text":"ok"}],"details":{"diff":"--- a\n+++ b"}}`, "--- a\n+++ b"},
		{"content text", `{"content":[{"type":"text","text":"hello "},{"type":"text","text":"world"}]}`, "hello world"},
		{"bash stdout", `{"details":{"stdout":"line1","exitCode":0}}`, "line1\n\nexit code: 0"},
		{"stderr", `{"details":{"stderr":"boom","exitCode":1}}`, "stderr:\nboom\n\nexit code: 1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := toolResultToText(json.RawMessage(c.in)); got != c.want {
				t.Errorf("toolResultToText(%s) = %q, want %q", c.in, got, c.want)
			}
		})
	}

	if got := toolResultToText(nil); got != "" {
		t.Errorf("nil result = %q, want empty", got)
	}
}

func TestPromptToPi(t *testing.T) {
	blocks := []acp.ContentBlock{
		acp.TextBlock("hello "),
		acp.ImageBlock("BASE64DATA", "image/png"),
		acp.TextBlock("world"),
	}

	msg, images := promptToPi(blocks)
	if msg != "hello world" {
		t.Errorf("message = %q", msg)
	}
	if len(images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(images))
	}
	if images[0].Data != "BASE64DATA" || images[0].MimeType != "image/png" || images[0].Type != "image" {
		t.Errorf("image = %+v", images[0])
	}
}

func TestPromptToPiPreservesBlobResources(t *testing.T) {
	imageMIME := "image/png"
	binaryMIME := "application/pdf"
	blocks := []acp.ContentBlock{
		acp.ResourceBlock(acp.EmbeddedResourceResource{BlobResourceContents: &acp.BlobResourceContents{
			Uri: "file:///work/image.png", MimeType: &imageMIME, Blob: "IMAGE",
		}}),
		acp.ResourceBlock(acp.EmbeddedResourceResource{BlobResourceContents: &acp.BlobResourceContents{
			Uri: "file:///work/report.pdf", MimeType: &binaryMIME, Blob: "PDF",
		}}),
	}
	message, images := promptToPi(blocks)
	if len(images) != 1 || images[0].MimeType != imageMIME || images[0].Data != "IMAGE" {
		t.Fatalf("images = %#v", images)
	}
	if !strings.Contains(message, "file:///work/report.pdf (application/pdf, base64)\nPDF") {
		t.Fatalf("message = %q", message)
	}
}

func TestToolKind(t *testing.T) {
	cases := map[string]acp.ToolKind{
		"read":    acp.ToolKindRead,
		"edit":    acp.ToolKindEdit,
		"write":   acp.ToolKindEdit,
		"bash":    acp.ToolKindExecute,
		"unknown": acp.ToolKindOther,
	}
	for name, want := range cases {
		if got := toolKind(name); got != want {
			t.Errorf("toolKind(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestBuildConfigOptions(t *testing.T) {
	models := []modelEntry{{ID: "wingman/a", Name: "wingman/a"}}
	opts := buildConfigOptions(models, "wingman/a", []string{"off", "high", "max"}, "high")
	if len(opts) != 2 {
		t.Fatalf("expected model+effort options, got %d", len(opts))
	}
	if opts[0].Select == nil || opts[0].Select.Id != modelConfigID {
		t.Errorf("opts[0] not model select: %+v", opts[0])
	}
	if opts[1].Select == nil || opts[1].Select.Id != effortConfigID {
		t.Errorf("opts[1] not effort select: %+v", opts[1])
	}
	if string(opts[1].Select.CurrentValue) != "high" {
		t.Errorf("effort current = %q, want high", opts[1].Select.CurrentValue)
	}

	// No models → only effort option.
	if only := buildConfigOptions(nil, "", []string{"off", "medium"}, "medium"); len(only) != 1 || only[0].Select.Id != effortConfigID {
		t.Errorf("no-models config = %+v", only)
	}

	// A model with no thinking support should not expose a non-functional option.
	if onlyModel := buildConfigOptions(models, "wingman/a", nil, "off"); len(onlyModel) != 1 || onlyModel[0].Select.Id != modelConfigID {
		t.Errorf("no-thinking config = %+v", onlyModel)
	}
}

func TestReplayMessagesPreservesRichConversation(t *testing.T) {
	data := json.RawMessage(`{"messages":[
		{"role":"user","content":[{"type":"text","text":"look"},{"type":"image","data":"AA==","mimeType":"image/png"}]},
		{"role":"assistant","content":[{"type":"thinking","thinking":"reason"},{"type":"text","text":"done"},{"type":"toolCall","id":"call-1","name":"read","arguments":{"path":"a.txt"}}]},
		{"role":"toolResult","toolCallId":"call-1","toolName":"read","content":[{"type":"text","text":"contents"}],"isError":false}
	]}`)
	var updates []acp.SessionUpdate
	replayMessages(func(update acp.SessionUpdate) { updates = append(updates, update) }, data, "/workspace")

	var userText, userImage, agentThought, agentText, toolStart, toolEnd bool
	for _, update := range updates {
		if chunk := update.UserMessageChunk; chunk != nil {
			userText = userText || chunk.Content.Text != nil
			userImage = userImage || chunk.Content.Image != nil
		}
		if chunk := update.AgentThoughtChunk; chunk != nil && chunk.Content.Text != nil {
			agentThought = true
		}
		if chunk := update.AgentMessageChunk; chunk != nil && chunk.Content.Text != nil {
			agentText = true
		}
		if update.ToolCall != nil {
			toolStart = true
			if update.ToolCall.Title != "Read file" || len(update.ToolCall.Locations) != 1 || update.ToolCall.RawInput != nil {
				t.Fatalf("replayed tool presentation = %#v", update.ToolCall)
			}
		}
		if update.ToolCallUpdate != nil {
			toolEnd = true
		}
	}
	if !userText || !userImage || !agentThought || !agentText || !toolStart || !toolEnd {
		t.Fatalf("replay lost content: text=%v image=%v thought=%v agent=%v toolStart=%v toolEnd=%v", userText, userImage, agentThought, agentText, toolStart, toolEnd)
	}
}
