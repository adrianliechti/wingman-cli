package claude

import (
	"strings"
	"testing"

	"github.com/coder/acp-go-sdk"
)

func TestCLIScannerAcceptsLargeDocumentRecord(t *testing.T) {
	line := `{"type":"user","message":{"content":[{"type":"tool_result","content":[{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"` + strings.Repeat("A", 9*1024*1024) + `"}}]}]}}`
	scanner := newCLIScanner(strings.NewReader(line + "\n"))
	if !scanner.Scan() {
		t.Fatalf("large record was rejected: %v", scanner.Err())
	}
	if got := len(scanner.Bytes()); got != len(line) {
		t.Fatalf("record length = %d, want %d", got, len(line))
	}
	if scanner.Scan() {
		t.Fatal("scanner returned an unexpected second record")
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestPromptMessagePreservesURLImageAndEmbeddedContextOrder(t *testing.T) {
	imageURI := "https://example.test/image.png"
	image := acp.ImageBlock("", "")
	image.Image.Uri = &imageURI
	mime := "text/plain"
	embedded := acp.ResourceBlock(acp.EmbeddedResourceResource{
		TextResourceContents: &acp.TextResourceContents{Uri: "file:///work/notes.txt", MimeType: &mime, Text: "notes"},
	})

	got := promptMessage([]acp.ContentBlock{
		acp.ResourceLinkBlock("guide", "file:///work/guide.md"),
		embedded,
		image,
	})
	content := got.Message.Content
	if len(content) != 4 {
		t.Fatalf("content = %#v", content)
	}
	if content[0].Text != "[@guide](file:///work/guide.md)" || content[1].Text != "[@notes.txt](file:///work/notes.txt)" {
		t.Fatalf("resource links = %#v", content[:2])
	}
	if content[2].Source == nil || content[2].Source.Type != "url" || content[2].Source.URL != imageURI {
		t.Fatalf("URL image = %#v", content[2])
	}
	if content[3].Text != "\n<context ref=\"file:///work/notes.txt\">\nnotes\n</context>" {
		t.Fatalf("deferred context = %#v", content[3])
	}
}

func TestPromptMessagePreservesBlobResources(t *testing.T) {
	imageMIME := "image/png"
	binaryMIME := "application/pdf"
	got := promptMessage([]acp.ContentBlock{
		acp.ResourceBlock(acp.EmbeddedResourceResource{BlobResourceContents: &acp.BlobResourceContents{
			Uri: "file:///work/image.png", MimeType: &imageMIME, Blob: "IMAGE",
		}}),
		acp.ResourceBlock(acp.EmbeddedResourceResource{BlobResourceContents: &acp.BlobResourceContents{
			Uri: "file:///work/report.pdf", MimeType: &binaryMIME, Blob: "PDF",
		}}),
	})
	content := got.Message.Content
	if len(content) != 3 || content[0].Source == nil || content[0].Source.Type != "base64" ||
		content[0].Source.MediaType != imageMIME || content[0].Source.Data != "IMAGE" {
		t.Fatalf("image resource = %#v", content)
	}
	if content[1].Text != "[@report.pdf](file:///work/report.pdf)" ||
		!strings.Contains(content[2].Text, `mimeType="application/pdf" encoding="base64"`) ||
		!strings.Contains(content[2].Text, "PDF") {
		t.Fatalf("binary resource = %#v", content)
	}
}
