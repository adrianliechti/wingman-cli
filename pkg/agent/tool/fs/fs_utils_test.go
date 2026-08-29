package fs_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	. "github.com/adrianliechti/wingman-agent/pkg/agent/tool/fs"
)

func TestWriteAndReadNormalizeAbsoluteWorkspacePaths(t *testing.T) {
	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	absPath := filepath.Join(tmpDir, "foo", "bar.txt")
	_, err := WriteTool(root).Execute(context.Background(), map[string]any{
		"file_path": absPath,
		"content":   "hello",
	})
	if err != nil {
		t.Fatalf("write absolute workspace path: %v", err)
	}

	result, err := ReadTool(root).Execute(context.Background(), map[string]any{"file_path": absPath})
	if err != nil {
		t.Fatalf("read absolute workspace path: %v", err)
	}
	if !strings.Contains(result.Content, "hello") {
		t.Fatalf("read result = %q, want content", result.Content)
	}
}

func TestWorkspaceBoundaryViaTools(t *testing.T) {
	root, _, cleanup := createTestRoot(t)
	defer cleanup()

	_, err := ReadTool(root).Execute(context.Background(), map[string]any{"file_path": "/etc/passwd"})
	if runtime.GOOS != "windows" {
		if err == nil || !strings.Contains(err.Error(), "outside workspace") {
			t.Fatalf("expected outside workspace error, got: %v", err)
		}
	}

	_, err = WriteTool(root).Execute(context.Background(), map[string]any{
		"file_path": filepath.Join(os.TempDir(), "wingman-outside-test.txt"),
		"content":   "x",
	})
	if err == nil || !strings.Contains(err.Error(), "outside workspace") {
		t.Fatalf("expected outside workspace write error, got: %v", err)
	}
}

func TestReadBinaryDetectionByContent(t *testing.T) {
	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	// Text content stays readable regardless of a "binary" extension.
	if err := os.WriteFile(filepath.Join(tmpDir, "unix.doc"), []byte("plain text documentation"), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := ReadTool(root).Execute(context.Background(), map[string]any{"file_path": "unix.doc"}); err != nil {
		t.Fatalf("expected text .doc to be readable, got: %v", err)
	}

	// A NUL byte marks binary content even under a text extension.
	if err := os.WriteFile(filepath.Join(tmpDir, "data.txt"), []byte("head\x00\x00tail"), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	_, err := ReadTool(root).Execute(context.Background(), map[string]any{"file_path": "data.txt"})
	if err == nil || !strings.Contains(err.Error(), "binary") {
		t.Fatalf("expected binary-content rejection, got: %v", err)
	}
}

func TestSandboxWildcardRoot(t *testing.T) {
	root, _, cleanup := createTestRoot(t)
	defer cleanup()

	outside := filepath.Join(os.TempDir(), "wingman-wildcard-root-test.txt")
	if err := os.WriteFile(outside, []byte("system config"), 0644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(outside)

	if _, err := ReadTool(root).Execute(context.Background(), map[string]any{"file_path": outside}); err == nil {
		t.Fatal("expected outside-workspace rejection without a wildcard root")
	}

	out, err := ReadTool(root, "*").Execute(context.Background(), map[string]any{"file_path": outside})
	if err != nil {
		t.Fatalf("wildcard root read failed: %v", err)
	}
	if !strings.Contains(out.Content, "system config") {
		t.Errorf("expected file content, got: %s", out.Content)
	}
}

func TestEditPreservesBOMAndLineEndings(t *testing.T) {
	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	path := filepath.Join(tmpDir, "bom_crlf.txt")
	if err := os.WriteFile(path, []byte("\uFEFFline1\r\nline2\r\n"), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	_, err := EditTool(root).Execute(context.Background(), map[string]any{
		"file_path":  "bom_crlf.txt",
		"old_string": "line2",
		"new_string": "changed",
	})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read edited file: %v", err)
	}
	if !strings.HasPrefix(string(content), "\uFEFF") {
		t.Fatalf("BOM was not preserved: %q", string(content))
	}
	if !strings.Contains(string(content), "\r\n") {
		t.Fatalf("CRLF line endings were not preserved: %q", string(content))
	}
}

func TestEditFuzzyMatchesCommonTypography(t *testing.T) {
	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	path := filepath.Join(tmpDir, "typography.txt")
	if err := os.WriteFile(path, []byte("say “hello”\nfoo—bar\nhello\u00A0world\n"), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	_, err := EditTool(root).Execute(context.Background(), map[string]any{
		"file_path":  "typography.txt",
		"old_string": "say \"hello\"\nfoo-bar\nhello world",
		"new_string": "say \"bye\"\nfoo-baz\nhello world",
	})
	if err != nil {
		t.Fatalf("fuzzy edit: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read edited file: %v", err)
	}
	if !strings.Contains(string(content), `say "bye"`) || !strings.Contains(string(content), "foo-baz") {
		t.Fatalf("unexpected edited content: %q", string(content))
	}
}

func TestEditReturnsLineDiff(t *testing.T) {
	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	if err := os.WriteFile(filepath.Join(tmpDir, "diff.txt"), []byte("line1\nold\nline3"), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	result, err := EditTool(root).Execute(context.Background(), map[string]any{
		"file_path":  "diff.txt",
		"old_string": "old",
		"new_string": "new",
	})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if !strings.Contains(result.Content, "-2 old") || !strings.Contains(result.Content, "+2 new") {
		t.Fatalf("expected line-numbered diff, got: %q", result.Content)
	}
}

func TestWriteTruncatesVeryLongSingleLineDiff(t *testing.T) {
	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	oldContent := strings.Repeat("a", 100_000)
	newContent := strings.Repeat("b", 100_000)
	path := filepath.Join(tmpDir, "minified.txt")
	if err := os.WriteFile(path, []byte(oldContent), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := WriteTool(root).Execute(context.Background(), map[string]any{
		"file_path": "minified.txt",
		"content":   newContent,
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if !strings.Contains(result.Content, "diff line truncated") {
		t.Fatalf("expected long-line truncation marker, got %d output bytes", len(result.Content))
	}
	if len(result.Content) > 12*1024 {
		t.Fatalf("long-line diff output is still too large: %d bytes", len(result.Content))
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != newContent {
		t.Fatalf("file was not written in full: %v, %d bytes", err, len(data))
	}
}
