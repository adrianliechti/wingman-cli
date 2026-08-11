package fs_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/adrianliechti/wingman-agent/pkg/agent/tool/fs"
)

func TestReadTool(t *testing.T) {
	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	content := "line1\nline2\nline3\nline4\nline5"
	testFile := filepath.Join(tmpDir, "test.txt")

	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	readTool := ReadTool(root)

	t.Run("read entire file", func(t *testing.T) {
		result, err := readTool.Execute(context.Background(), map[string]any{
			"file_path": "test.txt",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "line1") || !strings.Contains(result.Content, "line5") {
			t.Errorf("expected full content, got: %s", result.Content)
		}

		if !strings.Contains(result.Content, "1\tline1") {
			t.Errorf("expected compact cat -n style line numbers, got: %s", result.Content)
		}
	})

	t.Run("read with offset", func(t *testing.T) {
		result, err := readTool.Execute(context.Background(), map[string]any{
			"file_path": "test.txt",
			"offset":    float64(3),
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if strings.Contains(result.Content, "line1") || strings.Contains(result.Content, "line2") {
			t.Errorf("offset should skip first lines, got: %s", result.Content)
		}

		if !strings.Contains(result.Content, "line3") {
			t.Errorf("should contain line3, got: %s", result.Content)
		}

		if !strings.Contains(result.Content, "3\tline3") {
			t.Errorf("expected offset to start at 1-based line 3, got: %s", result.Content)
		}
	})

	t.Run("read with limit", func(t *testing.T) {
		result, err := readTool.Execute(context.Background(), map[string]any{
			"file_path": "test.txt",
			"limit":     2,
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "line1") {
			t.Errorf("should contain line1, got: %s", result.Content)
		}

		if strings.Contains(result.Content, "line3") {
			t.Errorf("limit should cap returned lines, got: %s", result.Content)
		}
		if !strings.Contains(result.Content, "offset=3") {
			t.Errorf("expected continuation offset, got: %s", result.Content)
		}
	})

	t.Run("read honors limit larger than DefaultMaxLines", func(t *testing.T) {

		var b strings.Builder
		for i := 1; i <= 3000; i++ {
			fmt.Fprintf(&b, "L%d\n", i)
		}
		os.WriteFile(filepath.Join(tmpDir, "long.txt"), []byte(b.String()), 0644)

		result, err := readTool.Execute(context.Background(), map[string]any{
			"file_path": "long.txt",
			"limit":     2500,
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "2500\tL2500") {
			t.Errorf("expected line 2500 in output (DefaultMaxLines was 2000), got: %s", result.Content[max(0, len(result.Content)-200):])
		}
		if strings.Contains(result.Content, "2501\tL2501") {
			t.Errorf("limit=2500 should stop at line 2500, got: %s", result.Content[max(0, len(result.Content)-200):])
		}
	})

	t.Run("read rejects non-positive limit", func(t *testing.T) {
		_, err := readTool.Execute(context.Background(), map[string]any{
			"file_path": "test.txt",
			"limit":     0,
		})

		if err == nil || !strings.Contains(err.Error(), "limit must be") {
			t.Fatalf("expected limit validation error, got: %v", err)
		}
	})

	t.Run("read rejects fractional offset", func(t *testing.T) {
		_, err := readTool.Execute(context.Background(), map[string]any{
			"file_path": "test.txt",
			"offset":    1.5,
		})

		if err == nil || !strings.Contains(err.Error(), "offset must be") {
			t.Fatalf("expected offset validation error, got: %v", err)
		}
	})

	t.Run("read rejects zero offset", func(t *testing.T) {
		_, err := readTool.Execute(context.Background(), map[string]any{
			"file_path": "test.txt",
			"offset":    0,
		})

		if err == nil || !strings.Contains(err.Error(), "positive 1-based") {
			t.Fatalf("expected offset validation error, got: %v", err)
		}
	})

	t.Run("read offset past end returns reminder", func(t *testing.T) {
		result, err := readTool.Execute(context.Background(), map[string]any{
			"file_path": "test.txt",
			"offset":    99,
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result.Content, "shorter than the provided offset (99)") {
			t.Errorf("expected offset reminder, got: %s", result.Content)
		}
	})

	t.Run("read rejects directories", func(t *testing.T) {
		_, err := readTool.Execute(context.Background(), map[string]any{
			"file_path": ".",
		})

		if err == nil || !strings.Contains(err.Error(), "directory") {
			t.Fatalf("expected directory error, got: %v", err)
		}
	})

	t.Run("read windows oversized files instead of loading them", func(t *testing.T) {
		path := filepath.Join(tmpDir, "too-large.txt")
		line := strings.Repeat("x", 1023) + "\n"
		content := strings.Repeat(line, int(MaxReadFileBytes/1024)+2)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		result, err := readTool.Execute(context.Background(), map[string]any{"file_path": "too-large.txt", "offset": 5000, "limit": 2})
		if err != nil {
			t.Fatalf("windowed read failed: %v", err)
		}
		if !strings.Contains(result.Content, "5000\t") || !strings.Contains(result.Content, "5001\t") {
			t.Fatalf("missing windowed lines, got: %.200s", result.Content)
		}
		if !strings.Contains(result.Content, "use offset=5002 to continue") {
			t.Fatalf("missing continuation notice, got: %s", result.Content[len(result.Content)-200:])
		}
	})

	t.Run("read rejects oversized binary files", func(t *testing.T) {
		path := filepath.Join(tmpDir, "too-large.bin")
		if err := os.WriteFile(path, make([]byte, MaxReadFileBytes+1), 0644); err != nil {
			t.Fatal(err)
		}

		_, err := readTool.Execute(context.Background(), map[string]any{"file_path": "too-large.bin"})
		if err == nil || !strings.Contains(err.Error(), "binary") {
			t.Fatalf("expected binary error, got: %v", err)
		}
	})

	t.Run("read non-existent file", func(t *testing.T) {
		_, err := readTool.Execute(context.Background(), map[string]any{
			"file_path": "nonexistent.txt",
		})

		if err == nil {
			t.Error("expected error for non-existent file")
		}
	})

	t.Run("path outside workspace rejected", func(t *testing.T) {
		_, err := readTool.Execute(context.Background(), map[string]any{
			"file_path": "/etc/passwd",
		})

		if err == nil {
			t.Error("expected error for path outside workspace")
		}

		if !strings.Contains(err.Error(), "outside workspace") {
			t.Errorf("expected 'outside workspace' error, got: %v", err)
		}
	})

	t.Run("read with absolute path inside workspace", func(t *testing.T) {
		result, err := readTool.Execute(context.Background(), map[string]any{
			"file_path": testFile,
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "line1") {
			t.Errorf("expected content, got: %s", result.Content)
		}
	})

	t.Run("read rejects binary files", func(t *testing.T) {
		os.WriteFile(filepath.Join(tmpDir, "logo.png"), []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), 0644)

		_, err := readTool.Execute(context.Background(), map[string]any{
			"file_path": "logo.png",
		})

		if err == nil {
			t.Fatal("expected error reading binary file, got nil")
		}

		if !strings.Contains(err.Error(), "binary") {
			t.Errorf("expected 'binary' in error, got: %v", err)
		}
	})

	t.Run("read warns on empty file", func(t *testing.T) {
		os.WriteFile(filepath.Join(tmpDir, "empty.txt"), []byte(""), 0644)

		result, err := readTool.Execute(context.Background(), map[string]any{
			"file_path": "empty.txt",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result.Content, "<system-reminder>") || !strings.Contains(result.Content, "empty") {
			t.Errorf("expected empty-file system-reminder, got: %s", result.Content)
		}
	})

	t.Run("read svg as text", func(t *testing.T) {
		os.WriteFile(filepath.Join(tmpDir, "icon.svg"), []byte(`<svg><title>Logo</title></svg>`), 0644)

		result, err := readTool.Execute(context.Background(), map[string]any{
			"file_path": "icon.svg",
		})

		if err != nil {
			t.Fatalf("unexpected error reading svg: %v", err)
		}

		if !strings.Contains(result.Content, "<title>Logo</title>") {
			t.Errorf("expected svg text, got: %s", result.Content)
		}
	})
}

func TestReadToolConfigurableSizeLimit(t *testing.T) {
	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	if err := os.WriteFile(filepath.Join(tmpDir, "small.txt"), []byte("12345"), 0644); err != nil {
		t.Fatal(err)
	}

	tools := Tools(root, &Options{MaxReadFileBytes: 4})
	result, err := tools[0].Execute(context.Background(), map[string]any{"file_path": "small.txt"})
	if err != nil {
		t.Fatalf("windowed read failed: %v", err)
	}
	if !strings.Contains(result.Content, "1\t12345") {
		t.Fatalf("expected windowed content, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "too large to read fully") {
		t.Fatalf("expected size notice, got: %s", result.Content)
	}
}

func TestReadAllowedReadRoots(t *testing.T) {
	root, _, cleanup := createTestRoot(t)
	defer cleanup()

	outside, err := os.MkdirTemp("", "fs_outside_*")
	if err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	defer os.RemoveAll(outside)

	allowedFile := filepath.Join(outside, "allowed.txt")
	os.WriteFile(allowedFile, []byte("allowed content"), 0644)

	denied, err := os.MkdirTemp("", "fs_denied_*")
	if err != nil {
		t.Fatalf("mkdir denied: %v", err)
	}
	defer os.RemoveAll(denied)
	deniedFile := filepath.Join(denied, "secret.txt")
	os.WriteFile(deniedFile, []byte("secret"), 0644)

	readTool := ReadTool(root, outside)

	t.Run("absolute path inside allowed root is readable", func(t *testing.T) {
		result, err := readTool.Execute(context.Background(), map[string]any{
			"file_path": allowedFile,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result.Content, "allowed content") {
			t.Errorf("expected allowed content, got: %s", result.Content)
		}
	})

	t.Run("absolute path outside both workspace and allowed roots is rejected", func(t *testing.T) {
		_, err := readTool.Execute(context.Background(), map[string]any{
			"file_path": deniedFile,
		})
		if err == nil {
			t.Fatal("expected error for path outside workspace and allow-list")
		}
		if !strings.Contains(err.Error(), "outside workspace") {
			t.Errorf("expected 'outside workspace' error, got: %v", err)
		}
	})

	t.Run("relative path outside workspace is rejected even with allow-list", func(t *testing.T) {
		_, err := readTool.Execute(context.Background(), map[string]any{
			"file_path": "../escape.txt",
		})
		if err == nil {
			t.Fatal("expected error for relative path outside workspace")
		}
	})
}
