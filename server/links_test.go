package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adrianliechti/wingman-agent/internal/testenv"
	"github.com/adrianliechti/wingman-agent/pkg/dap"
)

func TestWorkspaceIdentityAndDebugFramesThroughDirectoryLinks(t *testing.T) {
	for _, kind := range []string{"symlink", "junction"} {
		t.Run(kind, func(t *testing.T) {
			target := t.TempDir()
			outside := t.TempDir()
			for _, dir := range []string{target, outside} {
				if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			root := filepath.Join(t.TempDir(), "workspace")
			testenv.DirLink(t, kind, target, root)
			testenv.DirLink(t, kind, outside, filepath.Join(target, "escape"))
			actual, alias := workspaceScope(target), workspaceScope(root)
			if actual.WorkspaceID != alias.WorkspaceID || actual.InstanceID == alias.InstanceID {
				t.Fatalf("workspace identity mismatch: actual = %+v, alias = %+v", actual, alias)
			}
			for _, path := range []string{filepath.Join(target, "main.go"), filepath.Join(root, "main.go"), "main.go"} {
				frame := dap.StackFrame{Source: &dap.Source{Path: path}}
				normalizeDebugFrame(root, &frame)
				if frame.Source.Path != "main.go" {
					t.Errorf("frame %q normalized to %q", path, frame.Source.Path)
				}
			}
			for _, path := range []string{filepath.Join(root, "escape", "main.go"), filepath.Join(outside, "main.go")} {
				frame := dap.StackFrame{Source: &dap.Source{Path: path}}
				normalizeDebugFrame(root, &frame)
				if frame.Source.Path != "" {
					t.Errorf("outside frame %q retained as %q", path, frame.Source.Path)
				}
			}
		})
	}
}
