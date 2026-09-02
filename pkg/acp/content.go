package acp

import (
	"fmt"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
)

// ContentToBlocks converts Wingman prompt content to ACP wire blocks.
func ContentToBlocks(input []agent.Content) []acpsdk.ContentBlock {
	out := make([]acpsdk.ContentBlock, 0, len(input))
	for _, c := range input {
		// agent.Content is not a tagged union: text and an attachment may be
		// carried by the same value. Preserve both when that happens.
		if c.Text != "" {
			out = append(out, acpsdk.TextBlock(c.Text))
		}
		if c.File != nil && c.File.Data != "" {
			if mime, data, ok := splitDataURL(c.File.Data); ok {
				out = append(out, acpsdk.ImageBlock(data, mime))
			}
		}
	}
	return out
}

// ContentFromBlocks converts supported ACP prompt blocks to Wingman content.
func ContentFromBlocks(blocks []acpsdk.ContentBlock) []agent.Content {
	out := make([]agent.Content, 0, len(blocks))
	for _, b := range blocks {
		switch {
		case b.Text != nil:
			out = append(out, agent.Content{Text: b.Text.Text})
		case b.Image != nil:
			out = append(out, agent.Content{File: &agent.File{
				Data: fmt.Sprintf("data:%s;base64,%s", b.Image.MimeType, b.Image.Data),
			}})
		case b.ResourceLink != nil:
			out = append(out, agent.Content{Text: fmt.Sprintf("[Resource: %s]", b.ResourceLink.Uri)})
		}
	}
	return out
}

func splitDataURL(s string) (mime, data string, ok bool) {
	rest, found := strings.CutPrefix(s, "data:")
	if !found {
		return "", "", false
	}
	mime, data, ok = strings.Cut(rest, ";base64,")
	return mime, data, ok && mime != "" && data != ""
}
