//go:build unix

package fs_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/adrianliechti/wingman-agent/pkg/agent/tool/fs"
	"golang.org/x/sys/unix"
)

func TestReadToolRejectsNamedPipe(t *testing.T) {
	root, tmpDir, cleanup := createTestRoot(t)
	defer cleanup()

	pipe := filepath.Join(tmpDir, "events.pipe")
	if err := unix.Mkfifo(pipe, 0600); err != nil {
		t.Fatal(err)
	}

	_, err := ReadTool(root).Execute(context.Background(), map[string]any{"file_path": "events.pipe"})
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("expected regular-file error, got: %v", err)
	}

	if info, statErr := os.Stat(pipe); statErr != nil || info.Mode().IsRegular() {
		t.Fatalf("test fixture is not a named pipe: info=%v err=%v", info, statErr)
	}
}
