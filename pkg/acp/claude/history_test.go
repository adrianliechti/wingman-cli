package claude

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanSessionMetadataPrefersLatestAITitle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	lines := `{"type":"user","cwd":"/proj","message":{"role":"user","content":"hello"}}
{"type":"ai-title","aiTitle":"First title"}
{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}
{"type":"ai-title","aiTitle":"Second, more accurate title"}
`
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}

	title, cwd := scanSessionMetadata(path)
	if title != "Second, more accurate title" {
		t.Errorf("title = %q, want the latest ai-title line", title)
	}
	if cwd != "/proj" {
		t.Errorf("cwd = %q, want /proj", cwd)
	}
}

func TestScanSessionModelUsesLatestAssistantModel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	lines := `{"type":"assistant","message":{"model":"claude-sonnet-5","content":[]}}
{"type":"user","message":{"content":"continue"}}
{"type":"assistant","message":{"model":"claude-opus-4-8","content":[]}}
`
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := scanSessionModel(path); got != "claude-opus-4-8" {
		t.Fatalf("model = %q", got)
	}
}

func TestStripMarkerTags(t *testing.T) {

	for _, in := range []string{
		"<command-name>/model</command-name>",
		"<local-command-stderr>err</local-command-stderr>",
		"<command-name>/model</command-name>\n<command-message>model</command-message>\n<command-args>opus</command-args>",
	} {
		if _, ok := stripMarkerTags(in); ok {
			t.Errorf("stripMarkerTags(%q) should drop the message", in)
		}
	}

	if got, ok := stripMarkerTags("<local-command-stdout>out</local-command-stdout>"); !ok || got != "out" {
		t.Errorf("stdout payload = %q %v, want kept as %q", got, ok, "out")
	}

	mixed := "<command-name>/model</command-name>" +
		"<local-command-stdout>Set model to opus</local-command-stdout>" +
		"\nplease continue"
	got, ok := stripMarkerTags(mixed)
	if !ok {
		t.Fatalf("expected mixed content to be kept")
	}
	if want := "Set model to opus\nplease continue"; got != want {
		t.Errorf("stripMarkerTags mixed = %q, want %q", got, want)
	}

	if got, ok := stripMarkerTags("hello"); !ok || got != "hello" {
		t.Errorf("plain text = %q %v", got, ok)
	}
}
