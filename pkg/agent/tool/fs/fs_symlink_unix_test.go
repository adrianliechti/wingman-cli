//go:build unix

package fs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

// Absolute link targets are what Windows junctions always store; os.Root
// rejects them, so these tests exercise the resolved-containment fallback.
func TestReadThroughAbsoluteInRootSymlink(t *testing.T) {
	ws := t.TempDir()

	real := filepath.Join(ws, "real")
	if err := os.Mkdir(real, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "file.txt"), []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(ws, "link")); err != nil {
		t.Fatal(err)
	}

	root, err := os.OpenRoot(ws)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	read := ReadTool(root)

	result, err := read.Execute(context.Background(), map[string]any{"file_path": "link/file.txt"})
	if err != nil {
		t.Fatalf("read through in-root absolute symlink failed: %v", err)
	}
	if !strings.Contains(result.Content, "hello") {
		t.Fatalf("unexpected content: %q", result.Content)
	}
}

func TestWriteAndEditThroughAbsoluteInRootSymlink(t *testing.T) {
	ws := t.TempDir()

	real := filepath.Join(ws, "real")
	if err := os.Mkdir(real, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(ws, "link")); err != nil {
		t.Fatal(err)
	}

	root, err := os.OpenRoot(ws)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	tools := Tools(root, nil)
	var write, edit tool_
	for _, tl := range tools {
		switch tl.Name {
		case "write":
			write = tl.Execute
		case "edit":
			edit = tl.Execute
		}
	}

	if _, err := write(context.Background(), map[string]any{
		"file_path": "link/new.txt",
		"content":   "alpha beta\n",
	}); err != nil {
		t.Fatalf("write through symlink failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(real, "new.txt"))
	if err != nil || string(data) != "alpha beta\n" {
		t.Fatalf("file not written through link: %v %q", err, data)
	}

	if _, err := edit(context.Background(), map[string]any{
		"file_path":  "link/new.txt",
		"old_string": "beta",
		"new_string": "gamma",
	}); err != nil {
		t.Fatalf("edit through symlink failed: %v", err)
	}

	data, _ = os.ReadFile(filepath.Join(real, "new.txt"))
	if string(data) != "alpha gamma\n" {
		t.Fatalf("edit did not land: %q", data)
	}
}

type tool_ = func(context.Context, map[string]any) (tool.Result, error)

func TestReadStillRejectsEscapingSymlink(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()

	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(ws, "leak")); err != nil {
		t.Fatal(err)
	}

	root, err := os.OpenRoot(ws)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	read := ReadTool(root)

	if _, err := read.Execute(context.Background(), map[string]any{"file_path": "leak/secret.txt"}); err == nil {
		t.Fatal("expected escaping symlink to stay blocked")
	}
}

func TestReadAcceptsAliasSpellingOfWorkspacePath(t *testing.T) {
	parent := t.TempDir()

	real := filepath.Join(parent, "real")
	if err := os.Mkdir(real, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "file.txt"), []byte("aliased"), 0644); err != nil {
		t.Fatal(err)
	}

	alias := filepath.Join(parent, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}

	root, err := os.OpenRoot(real)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	read := ReadTool(root)

	result, err := read.Execute(context.Background(), map[string]any{
		"file_path": filepath.Join(alias, "file.txt"),
	})
	if err != nil {
		t.Fatalf("alias spelling of a workspace path rejected: %v", err)
	}
	if !strings.Contains(result.Content, "aliased") {
		t.Fatalf("unexpected content: %q", result.Content)
	}
}

func TestGrepThroughAbsoluteInRootSymlink(t *testing.T) {
	ws := t.TempDir()

	real := filepath.Join(ws, "real")
	if err := os.Mkdir(real, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "code.go"), []byte("package needlepkg\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(ws, "link")); err != nil {
		t.Fatal(err)
	}

	root, err := os.OpenRoot(ws)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	grep := GrepTool(root)

	result, err := grep.Execute(context.Background(), map[string]any{
		"pattern":     "needlepkg",
		"path":        "link",
		"output_mode": "content",
	})
	if err != nil {
		t.Fatalf("grep through symlinked path failed: %v", err)
	}
	if !strings.Contains(result.Content, "needlepkg") || !strings.Contains(result.Content, "link") {
		t.Fatalf("unexpected grep output: %q", result.Content)
	}
}
