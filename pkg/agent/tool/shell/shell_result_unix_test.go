//go:build unix

package shell

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShellReportsEmptyOutput(t *testing.T) {
	shellTool := Tools(t.TempDir(), nil, nil, nil)[0]

	result, err := shellTool.Execute(context.Background(), map[string]any{"command": "true"})
	if err != nil {
		t.Fatal(err)
	}
	if result != "(command completed with no output)" {
		t.Fatalf("got %q", result)
	}
}

func TestShellWorkdir(t *testing.T) {
	workDir := t.TempDir()
	sub := filepath.Join(workDir, "sub")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}

	shellTool := Tools(workDir, nil, nil, nil)[0]

	result, err := shellTool.Execute(context.Background(), map[string]any{"command": "pwd", "workdir": "sub"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "/sub") {
		t.Fatalf("expected command to run in sub dir, got %q", result)
	}

	if _, err := shellTool.Execute(context.Background(), map[string]any{"command": "pwd", "workdir": "missing"}); err == nil {
		t.Fatal("expected error for missing workdir")
	}
}
