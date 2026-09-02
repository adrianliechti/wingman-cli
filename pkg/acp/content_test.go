package acp

import (
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
)

func TestPromptContentRoundTrip(t *testing.T) {
	input := []agent.Content{
		{Text: "hello"},
		{File: &agent.File{Data: "data:image/png;base64,aGVsbG8="}},
	}

	blocks := ContentToBlocks(input)
	if len(blocks) != 2 || blocks[0].Text == nil || blocks[1].Image == nil {
		t.Fatalf("blocks = %+v", blocks)
	}

	got := ContentFromBlocks(blocks)
	if len(got) != 2 || got[0].Text != input[0].Text || got[1].File == nil || got[1].File.Data != input[1].File.Data {
		t.Fatalf("round trip = %+v", got)
	}
}

func TestContentToBlocksPreservesTextAndImageFromOneContent(t *testing.T) {
	blocks := ContentToBlocks([]agent.Content{{
		Text: "describe this",
		File: &agent.File{Data: "data:image/png;base64,aGVsbG8="},
	}})
	if len(blocks) != 2 || blocks[0].Text == nil || blocks[0].Text.Text != "describe this" ||
		blocks[1].Image == nil || blocks[1].Image.Data != "aGVsbG8=" || blocks[1].Image.MimeType != "image/png" {
		t.Fatalf("blocks = %+v", blocks)
	}
}

func TestContentToBlocksIgnoresMalformedAttachmentsWithoutDroppingText(t *testing.T) {
	blocks := ContentToBlocks([]agent.Content{{
		Text: "keep me",
		File: &agent.File{Data: "not-a-data-url"},
	}})
	if len(blocks) != 1 || blocks[0].Text == nil || blocks[0].Text.Text != "keep me" {
		t.Fatalf("blocks = %+v", blocks)
	}
}

func TestContentFromBlocksKeepsResourceLinks(t *testing.T) {
	got := ContentFromBlocks([]acpsdk.ContentBlock{
		acpsdk.ResourceLinkBlock("docs", "file:///workspace/docs.md"),
	})
	if len(got) != 1 || got[0].Text != "[Resource: file:///workspace/docs.md]" {
		t.Fatalf("resource content = %+v", got)
	}
}
