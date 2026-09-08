package fs_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	. "github.com/adrianliechti/wingman-agent/pkg/agent/tool/fs"
)

func TestGrepTool(t *testing.T) {
	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	os.WriteFile(filepath.Join(tmpDir, "file1.go"), []byte("package main\n\nfunc Hello() {\n\treturn \"hello\"\n}"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "file2.go"), []byte("package util\n\nfunc World() {\n\treturn \"world\"\n}"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "readme.md"), []byte("# Hello World\nThis is a test."), 0644)
	os.WriteFile(filepath.Join(tmpDir, "icon.svg"), []byte(`<svg><title>Hello Icon</title></svg>`), 0644)

	grepTool := GrepTool(root)

	t.Run("grep simple pattern", func(t *testing.T) {
		result, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern": "func",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "file1.go") {
			t.Errorf("expected file1.go in results, got: %s", result.Content)
		}

		if strings.Contains(result.Content, "func Hello") {
			t.Errorf("default output should list files only, got: %s", result.Content)
		}
	})

	t.Run("grep with regex", func(t *testing.T) {
		result, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern":     "func \\w+\\(",
			"output_mode": "content",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "Hello()") || !strings.Contains(result.Content, "World()") {
			t.Errorf("expected function matches, got: %s", result.Content)
		}

		if !strings.Contains(result.Content, "file1.go:3:func Hello") {
			t.Errorf("expected ripgrep-style line-numbered content, got: %s", result.Content)
		}
	})

	t.Run("grep case insensitive", func(t *testing.T) {
		result, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern":     "HELLO",
			"-i":          true,
			"output_mode": "content",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "Hello") || !strings.Contains(result.Content, "hello") {
			t.Errorf("expected case-insensitive matches, got: %s", result.Content)
		}
	})

	t.Run("grep aliases case and context flags", func(t *testing.T) {
		result, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern":     "HELLO",
			"-i":          true,
			"-A":          float64(1),
			"output_mode": "content",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "Hello") || !strings.Contains(result.Content, "return") {
			t.Errorf("expected case-insensitive match with after context, got: %s", result.Content)
		}
	})

	t.Run("grep with glob filter", func(t *testing.T) {
		result, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern": "Hello",
			"glob":    "*.go",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if strings.Contains(result.Content, "readme.md") {
			t.Errorf("should not include markdown files, got: %s", result.Content)
		}
	})

	t.Run("grep with context lines", func(t *testing.T) {
		result, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern":     "func Hello",
			"context":     float64(1),
			"output_mode": "content",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result.Content, "func Hello") {
			t.Errorf("expected match line, got: %s", result.Content)
		}
		lines := strings.Split(result.Content, "\n")

		if len(lines) < 2 {
			t.Errorf("expected multiple lines with context, got: %s", result.Content)
		}

		if !strings.Contains(result.Content, "file1.go-2-") {
			t.Errorf("expected ripgrep-style context separator, got: %s", result.Content)
		}
	})

	t.Run("grep can omit line numbers", func(t *testing.T) {
		result, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern":     "func Hello",
			"output_mode": "content",
			"-n":          false,
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if strings.Contains(result.Content, "file1.go:3:") {
			t.Errorf("did not expect line numbers, got: %s", result.Content)
		}
		if !strings.Contains(result.Content, "file1.go:func Hello") {
			t.Errorf("expected path:content output without line numbers, got: %s", result.Content)
		}
	})

	t.Run("grep no matches", func(t *testing.T) {
		result, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern": "zzz_no_match_zzz",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.Content != "No files found" {
			t.Errorf("expected 'No files found', got: %s", result.Content)
		}
	})

	t.Run("grep invalid output mode", func(t *testing.T) {
		_, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern":     "Hello",
			"output_mode": "bad",
		})

		if err == nil || !strings.Contains(err.Error(), "output_mode must be") {
			t.Fatalf("expected output_mode validation error, got: %v", err)
		}
	})

	t.Run("grep rejects fractional numeric parameters", func(t *testing.T) {
		_, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern":     "Hello",
			"output_mode": "content",
			"head_limit":  1.5,
		})

		if err == nil || !strings.Contains(err.Error(), "head_limit") {
			t.Fatalf("expected head_limit validation error, got: %v", err)
		}
	})

	t.Run("grep rejects negative numeric parameters", func(t *testing.T) {
		_, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern": "Hello",
			"offset":  -1,
		})

		if err == nil || !strings.Contains(err.Error(), "offset") {
			t.Fatalf("expected offset validation error, got: %v", err)
		}
	})

	t.Run("grep rejects fractional context parameters", func(t *testing.T) {
		_, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern": "Hello",
			"context": 1.5,
		})

		if err == nil || !strings.Contains(err.Error(), "context") {
			t.Fatalf("expected context validation error, got: %v", err)
		}
	})

	t.Run("grep count is exact above previous cap", func(t *testing.T) {
		lines := strings.Repeat("needle\n", 10005)
		os.WriteFile(filepath.Join(tmpDir, "many.txt"), []byte(lines), 0644)

		result, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern":     "needle",
			"path":        "many.txt",
			"output_mode": "count",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "many.txt:10005") {
			t.Errorf("expected exact count, got: %s", result.Content)
		}
	})

	t.Run("grep count counts occurrences, not matching lines", func(t *testing.T) {
		os.WriteFile(filepath.Join(tmpDir, "occurrences.txt"), []byte("needle needle\nneedle\n"), 0644)

		result, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern":     "needle",
			"path":        "occurrences.txt",
			"output_mode": "count",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "occurrences.txt:3") {
			t.Errorf("expected occurrence count, got: %s", result.Content)
		}
	})

	t.Run("grep single file", func(t *testing.T) {
		result, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern": "Hello",
			"path":    "readme.md",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "readme.md") {
			t.Errorf("expected readme.md match, got: %s", result.Content)
		}

		if strings.Contains(result.Content, "file1.go") {
			t.Errorf("should only search single file, got: %s", result.Content)
		}
	})

	t.Run("grep single file applies glob filter", func(t *testing.T) {
		result, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern": "Hello",
			"path":    "readme.md",
			"glob":    "*.go",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "No matches") {
			t.Errorf("expected glob mismatch to return no matches, got: %s", result.Content)
		}
	})

	t.Run("grep single file applies files skip", func(t *testing.T) {
		result, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern": "Hello",
			"path":    "readme.md",
			"skip":    1,
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.Content != "No files found" {
			t.Errorf("expected single files_with_matches result to be skipped, got: %s", result.Content)
		}
	})

	t.Run("grep content skip notice points to next page", func(t *testing.T) {
		os.WriteFile(filepath.Join(tmpDir, "file3.go"), []byte("package extra\n\nfunc Third() {\n\treturn\n}"), 0644)

		result, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern":     "func",
			"output_mode": "content",
			"head_limit":  1,
			"skip":        1,
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "pagination = limit: 1, skip: 1") {
			t.Errorf("expected next skip notice, got: %s", result.Content)
		}
	})

	t.Run("grep files_with_matches sorts by modified time before limit", func(t *testing.T) {
		oldPath := filepath.Join(tmpDir, "oldmatch.txt")
		newPath := filepath.Join(tmpDir, "newmatch.txt")
		os.WriteFile(oldPath, []byte("mtime needle\n"), 0644)
		os.WriteFile(newPath, []byte("mtime needle\n"), 0644)
		oldTime := time.Now().Add(-2 * time.Hour)
		newTime := time.Now().Add(-1 * time.Hour)
		os.Chtimes(oldPath, oldTime, oldTime)
		os.Chtimes(newPath, newTime, newTime)

		result, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern":    "mtime needle",
			"head_limit": 1,
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "newmatch.txt") || strings.Contains(result.Content, "oldmatch.txt") {
			t.Errorf("expected newest matching file only, got: %s", result.Content)
		}
		if !strings.Contains(result.Content, "limit: 1") {
			t.Errorf("expected limit info, got: %s", result.Content)
		}
	})

	t.Run("grep includes hidden files and respects gitignore", func(t *testing.T) {
		os.WriteFile(filepath.Join(tmpDir, ".hidden.txt"), []byte("secret-needle\n"), 0644)
		os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte("ignored.txt\n"), 0644)
		os.WriteFile(filepath.Join(tmpDir, "ignored.txt"), []byte("secret-needle\n"), 0644)

		result, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern": "secret-needle",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, ".hidden.txt") {
			t.Errorf("expected hidden file match, got: %s", result.Content)
		}
		if strings.Contains(result.Content, "ignored.txt") {
			t.Errorf("expected gitignored file to be skipped, got: %s", result.Content)
		}
	})

	t.Run("grep nested gitignore does not leak to siblings", func(t *testing.T) {
		newRoot, newTmp, newCleanup := createTestRoot(t)
		defer newCleanup()

		os.MkdirAll(filepath.Join(newTmp, "a"), 0755)
		os.MkdirAll(filepath.Join(newTmp, "b"), 0755)
		os.WriteFile(filepath.Join(newTmp, "a", ".gitignore"), []byte("ignored.txt\n"), 0644)
		os.WriteFile(filepath.Join(newTmp, "a", "ignored.txt"), []byte("leak-needle\n"), 0644)
		os.WriteFile(filepath.Join(newTmp, "b", "ignored.txt"), []byte("leak-needle\n"), 0644)

		result, err := GrepTool(newRoot).Execute(context.Background(), map[string]any{
			"pattern": "leak-needle",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if strings.Contains(result.Content, filepath.Join("a", "ignored.txt")) {
			t.Errorf("expected a/.gitignore to ignore only a/ignored.txt, got: %s", result.Content)
		}
		if !strings.Contains(result.Content, filepath.Join("b", "ignored.txt")) {
			t.Errorf("expected sibling ignored.txt not to be ignored, got: %s", result.Content)
		}
	})

	t.Run("grep with absolute path", func(t *testing.T) {
		result, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern":     "func",
			"path":        tmpDir,
			"output_mode": "content",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "file1.go") {
			t.Errorf("expected file1.go in results, got: %s", result.Content)
		}

		if !strings.Contains(result.Content, "Hello") {
			t.Errorf("expected 'Hello' in results, got: %s", result.Content)
		}
	})

	t.Run("grep multiline pattern spanning lines", func(t *testing.T) {
		os.WriteFile(filepath.Join(tmpDir, "multi.go"), []byte("type Foo struct {\n\tname string\n\tfield int\n}\n"), 0644)

		result, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern":     `struct \{[\s\S]*?field`,
			"path":        "multi.go",
			"multiline":   true,
			"output_mode": "content",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "multi.go") || !strings.Contains(result.Content, "field") {
			t.Errorf("expected multi.go and matched 'field' line, got: %s", result.Content)
		}

		nonMulti, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern": `struct \{[\s\S]*?field`,
			"path":    "multi.go",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if nonMulti.Content != "No files found" {
			t.Errorf("non-multiline should not match across lines, got: %s", nonMulti.Content)
		}
	})

	t.Run("grep type filter", func(t *testing.T) {
		result, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern": "Hello",
			"type":    "go",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "file1.go") {
			t.Errorf("expected go file in results, got: %s", result.Content)
		}

		if strings.Contains(result.Content, "readme.md") {
			t.Errorf("type=go should exclude markdown files, got: %s", result.Content)
		}
	})

	t.Run("grep unsupported type", func(t *testing.T) {
		_, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern": "Hello",
			"type":    "notatype",
		})

		if err == nil || !strings.Contains(err.Error(), "unsupported type") {
			t.Fatalf("expected unsupported type error, got: %v", err)
		}
	})

	t.Run("grep type and glob both apply", func(t *testing.T) {
		result, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern": "func",
			"type":    "go",
			"glob":    "file2.*",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "file2.go") || strings.Contains(result.Content, "file1.go") {
			t.Errorf("expected only file2.go, got: %s", result.Content)
		}
	})

	t.Run("grep head limit zero is unlimited", func(t *testing.T) {
		result, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern":    "package|Hello",
			"head_limit": float64(0),
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if strings.Contains(result.Content, "limit 0 hit") {
			t.Errorf("head_limit=0 should not impose a zero-result limit, got: %s", result.Content)
		}
		if !strings.Contains(result.Content, "file1.go") || !strings.Contains(result.Content, "file2.go") || !strings.Contains(result.Content, "readme.md") {
			t.Errorf("expected all matching files, got: %s", result.Content)
		}
	})

	t.Run("grep skips VCS directories", func(t *testing.T) {
		newRoot, newTmp, newCleanup := createTestRoot(t)
		defer newCleanup()

		os.MkdirAll(filepath.Join(newTmp, ".git"), 0755)
		os.MkdirAll(filepath.Join(newTmp, "src"), 0755)
		os.WriteFile(filepath.Join(newTmp, ".git", "config"), []byte("vcs-needle\n"), 0644)
		os.WriteFile(filepath.Join(newTmp, "src", "main.go"), []byte("vcs-needle\n"), 0644)

		result, err := GrepTool(newRoot).Execute(context.Background(), map[string]any{
			"pattern": "vcs-needle",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, filepath.Join("src", "main.go")) {
			t.Errorf("expected src/main.go in results, got: %s", result.Content)
		}
		if strings.Contains(result.Content, ".git") {
			t.Errorf(".git contents must be skipped, got: %s", result.Content)
		}
	})

	t.Run("grep with -B before context", func(t *testing.T) {
		result, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern":     "return",
			"-B":          float64(2),
			"output_mode": "content",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "file1.go-2-") {
			t.Errorf("expected file1.go line 2 as before-context, got: %s", result.Content)
		}
		if !strings.Contains(result.Content, "file1.go-3-") {
			t.Errorf("expected file1.go line 3 as before-context, got: %s", result.Content)
		}
	})

	t.Run("grep negated glob excludes matching files", func(t *testing.T) {
		newRoot, newTmp, newCleanup := createTestRoot(t)
		defer newCleanup()

		os.WriteFile(filepath.Join(newTmp, "keep.go"), []byte("needle\n"), 0644)
		os.WriteFile(filepath.Join(newTmp, "drop.log"), []byte("needle\n"), 0644)

		result, err := GrepTool(newRoot).Execute(context.Background(), map[string]any{
			"pattern": "needle",
			"glob":    "!*.log",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "keep.go") {
			t.Errorf("expected keep.go in results, got: %s", result.Content)
		}
		if strings.Contains(result.Content, "drop.log") {
			t.Errorf("negated glob should exclude drop.log, got: %s", result.Content)
		}
	})

	t.Run("grep searches svg as text", func(t *testing.T) {
		result, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern": "Hello Icon",
			"path":    "icon.svg",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Content, "icon.svg") {
			t.Errorf("expected svg match, got: %s", result.Content)
		}
	})
}

func TestGrepHandlesLongLines(t *testing.T) {
	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	longLine := strings.Repeat("x", MaxScanBufSize+1024)
	body := "needle line 1\nneedle line 2\n" + longLine + "\nneedle line 4\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "big.txt"), []byte(body), 0644); err != nil {
		t.Fatalf("write big.txt: %v", err)
	}

	grepTool := GrepTool(root)

	result, err := grepTool.Execute(context.Background(), map[string]any{
		"pattern":     "needle",
		"output_mode": "content",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result.Content, "needle line 1") || !strings.Contains(result.Content, "needle line 2") {
		t.Errorf("expected pre-long-line matches preserved, got: %s", result.Content)
	}

	if !strings.Contains(result.Content, "exceeds") || !strings.Contains(result.Content, "scan limit") {
		t.Errorf("expected scan-cutoff sentinel, got: %s", result.Content)
	}
}

func TestGrepLongLineWithoutMatchDoesNotCreateFalsePositive(t *testing.T) {
	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	longLine := strings.Repeat("x", MaxScanBufSize+1024)
	if err := os.WriteFile(filepath.Join(tmpDir, "big.txt"), []byte(longLine), 0644); err != nil {
		t.Fatalf("write big.txt: %v", err)
	}

	grepTool := GrepTool(root)

	result, err := grepTool.Execute(context.Background(), map[string]any{
		"pattern": "needle",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Content != "No files found" {
		t.Errorf("expected no false positive for long line without match, got: %s", result.Content)
	}
}

func TestGrepSkipsExtensionlessBinaryFiles(t *testing.T) {
	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	payload := append([]byte("prefix needle line\n"), 0x00, 0x01, 0x02, 0x03)
	payload = append(payload, []byte("\nmore needle data")...)
	if err := os.WriteFile(filepath.Join(tmpDir, "blob"), payload, 0644); err != nil {
		t.Fatalf("write blob: %v", err)
	}

	if err := os.WriteFile(filepath.Join(tmpDir, "neighbor.txt"), []byte("needle here\n"), 0644); err != nil {
		t.Fatalf("write neighbor: %v", err)
	}

	result, err := GrepTool(root).Execute(context.Background(), map[string]any{
		"pattern":     "needle",
		"output_mode": "content",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(result.Content, "blob") {
		t.Errorf("expected binary file 'blob' to be skipped, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "neighbor.txt") {
		t.Errorf("expected neighbor.txt match preserved, got: %s", result.Content)
	}
}

func TestGrepBinaryDetectionDoesNotMisfireOnSmallFiles(t *testing.T) {
	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	if err := os.WriteFile(filepath.Join(tmpDir, "tiny.txt"), []byte("needle"), 0644); err != nil {
		t.Fatalf("write tiny.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "empty.txt"), nil, 0644); err != nil {
		t.Fatalf("write empty.txt: %v", err)
	}

	result, err := GrepTool(root).Execute(context.Background(), map[string]any{
		"pattern": "needle",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Content, "tiny.txt") {
		t.Errorf("expected tiny.txt match, got: %s", result.Content)
	}
}

func TestGrepSkipsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink tests may require elevated privileges on Windows")
	}

	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	os.MkdirAll(filepath.Join(tmpDir, "dir1"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "dir1", "file.txt"), []byte("searchme"), 0644)

	symlink := filepath.Join(tmpDir, "dir1", "circular")
	if err := os.Symlink(tmpDir, symlink); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	grepTool := GrepTool(root)

	result, err := grepTool.Execute(context.Background(), map[string]any{
		"pattern": "searchme",
	})

	if err != nil {
		t.Fatalf("grep should not fail with symlinks: %v", err)
	}

	if !strings.Contains(result.Content, "dir1/file.txt") {
		t.Errorf("expected matching file in results, got: %s", result.Content)
	}
}

func TestGrepAllowedReadRoots(t *testing.T) {
	root, _, cleanup := createTestRoot(t)
	defer cleanup()

	outside, err := os.MkdirTemp("", "fs_grep_outside_*")
	if err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	defer os.RemoveAll(outside)

	os.WriteFile(filepath.Join(outside, "memory.md"), []byte("recall this detail\n"), 0644)
	os.MkdirAll(filepath.Join(outside, "sub"), 0755)
	os.WriteFile(filepath.Join(outside, "sub", "other.md"), []byte("recall this detail too\n"), 0644)

	denied, err := os.MkdirTemp("", "fs_grep_denied_*")
	if err != nil {
		t.Fatalf("mkdir denied: %v", err)
	}
	defer os.RemoveAll(denied)
	os.WriteFile(filepath.Join(denied, "secret.md"), []byte("recall\n"), 0644)

	grepTool := GrepTool(root, outside)

	t.Run("directory inside allowed root is searchable", func(t *testing.T) {
		result, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern": "recall",
			"path":    outside,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := filepath.Join(outside, "memory.md")
		if !strings.Contains(result.Content, want) {
			t.Errorf("expected %s in result, got: %s", want, result.Content)
		}
		wantSub := filepath.Join(outside, "sub", "other.md")
		if !strings.Contains(result.Content, wantSub) {
			t.Errorf("expected %s in result, got: %s", wantSub, result.Content)
		}
	})

	t.Run("single file inside allowed root is searchable", func(t *testing.T) {
		filePath := filepath.Join(outside, "memory.md")
		result, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern":     "recall",
			"path":        filePath,
			"output_mode": "content",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result.Content, filePath) {
			t.Errorf("expected absolute path %s in result, got: %s", filePath, result.Content)
		}
	})

	t.Run("path outside both workspace and allow-list is rejected", func(t *testing.T) {
		_, err := grepTool.Execute(context.Background(), map[string]any{
			"pattern": "recall",
			"path":    denied,
		})
		if err == nil {
			t.Fatal("expected error for path outside workspace and allow-list")
		}
	})
}

func TestGrepWildcardReadRoot(t *testing.T) {
	root, _, cleanup := createTestRoot(t)
	defer cleanup()
	dir := t.TempDir()
	file := filepath.Join(dir, "table.go")
	if err := os.WriteFile(file, []byte("type Table struct{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, file} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			result, err := GrepTool(root, "*").Execute(context.Background(), map[string]any{
				"path": path, "pattern": "Table",
			})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(result.Content, file) {
				t.Fatalf("result = %q, want %q", result.Content, file)
			}
		})
	}
}
