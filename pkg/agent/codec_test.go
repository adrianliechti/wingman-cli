package agent

import (
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
)

func TestReasoningToInputReplaysEncryptedContent(t *testing.T) {
	p := reasoningToInput(&Reasoning{ID: "rs_1", Summary: "sum", Content: "blob", Model: "gpt-5.5"})

	if p == nil {
		t.Fatal("expected reasoning item to replay")
	}
	if p.ID != "rs_1" {
		t.Fatalf("ID = %q, want rs_1", p.ID)
	}
	if !p.EncryptedContent.Valid() || p.EncryptedContent.Value != "blob" {
		t.Fatalf("EncryptedContent = %#v, want blob", p.EncryptedContent)
	}
	if len(p.Summary) != 1 || p.Summary[0].Text != "sum" {
		t.Fatalf("Summary = %#v, want single part", p.Summary)
	}
}

func TestReasoningToInputSkipsUnreplayableItems(t *testing.T) {
	cases := []struct {
		name string
		r    *Reasoning
	}{
		{"nil", nil},
		{"no encrypted content", &Reasoning{ID: "rs_1", Summary: "sum"}},
		{"no id", &Reasoning{Summary: "sum", Content: "blob"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if p := reasoningToInput(tc.r); p != nil {
				t.Fatalf("expected nil, got %#v", p)
			}
		})
	}
}

func TestFromReasoningCapturesEncryptedContentAndSummaryParts(t *testing.T) {
	msg, ok := fromReasoning(&responses.ResponseReasoningItemParam{
		ID:               "rs_1",
		EncryptedContent: openai.String("blob"),
		Summary: []responses.ResponseReasoningItemSummaryParam{
			{Text: "part one"},
			{Text: "part two"},
		},
	})

	if !ok {
		t.Fatal("expected reasoning item to convert")
	}
	r := msg.Content[0].Reasoning
	if r.Content != "blob" {
		t.Fatalf("Content = %q, want blob", r.Content)
	}
	if r.Summary != "part one\n\npart two" {
		t.Fatalf("Summary = %q", r.Summary)
	}
}

func TestFromReasoningKeepsEncryptedOnlyItems(t *testing.T) {
	msg, ok := fromReasoning(&responses.ResponseReasoningItemParam{
		ID:               "rs_1",
		EncryptedContent: openai.String("blob"),
	})

	if !ok || msg.Content[0].Reasoning.Content != "blob" {
		t.Fatal("expected summary-less encrypted item to be kept")
	}
}

func TestFromReasoningDropsEmptyItems(t *testing.T) {
	if _, ok := fromReasoning(&responses.ResponseReasoningItemParam{ID: "rs_1"}); ok {
		t.Fatal("expected item without summary or content to be dropped")
	}
}

func TestOutputMessageTextPreservesStableID(t *testing.T) {
	msg, ok := fromOutput(&responses.ResponseOutputMessageParam{
		ID: "message-1",
		Content: []responses.ResponseOutputMessageContentUnionParam{{
			OfOutputText: &responses.ResponseOutputTextParam{Text: "answer"},
		}},
	})
	if !ok || msg.Content[0].TextID != "message-1" {
		t.Fatalf("converted message = %+v", msg)
	}

	items, _ := assistantToInput(msg)
	if len(items) != 1 || items[0].OfOutputMessage == nil {
		t.Fatalf("replayed items = %+v", items)
	}
	if items[0].OfOutputMessage.ID != "" {
		t.Fatalf("provider-local text ID was replayed: %+v", items)
	}
}

func TestToInputFlushesImagesAfterToolResultRun(t *testing.T) {
	messages := []Message{
		{Role: RoleAssistant, Content: []Content{
			{ToolCall: &ToolCall{ID: "c1", Name: "view_image"}},
			{ToolCall: &ToolCall{ID: "c2", Name: "read"}},
		}},
		{Role: RoleAssistant, Content: []Content{
			{ToolResult: &ToolResult{ID: "c1", Name: "view_image", Content: "[image attached below]"}},
			{File: &File{Data: "data:image/png;base64,AAAA"}},
		}},
		{Role: RoleAssistant, Content: []Content{
			{ToolResult: &ToolResult{ID: "c2", Name: "read", Content: "file contents"}},
		}},
	}

	items := toInput(messages)

	var kinds []string
	for _, item := range items {
		switch {
		case item.OfFunctionCall != nil:
			kinds = append(kinds, "call")
		case item.OfFunctionCallOutput != nil:
			kinds = append(kinds, "output")
		case item.OfInputMessage != nil:
			kinds = append(kinds, "image")
		default:
			kinds = append(kinds, "other")
		}
	}

	want := []string{"call", "call", "output", "output", "image"}
	if len(kinds) != len(want) {
		t.Fatalf("items = %v", kinds)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("items = %v, want %v", kinds, want)
		}
	}
}
