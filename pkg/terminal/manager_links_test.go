package terminal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adrianliechti/wingman-agent/internal/testenv"
)

func TestCommandDirDirectoryLinkBoundaries(t *testing.T) {
	for _, kind := range []string{"symlink", "junction"} {
		t.Run(kind, func(t *testing.T) {
			target := t.TempDir()
			if err := os.Mkdir(filepath.Join(target, "project"), 0755); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(t.TempDir(), "workspace")
			testenv.DirLink(t, kind, target, root)
			testenv.DirLink(t, kind, filepath.Join(target, "project"), filepath.Join(target, "inside"))
			testenv.DirLink(t, kind, t.TempDir(), filepath.Join(target, "escape"))
			manager := NewManager(root)
			for _, value := range []string{"", "project", "inside"} {
				if got, err := manager.commandDir(value); err != nil || got != filepath.Join(root, value) {
					t.Errorf("commandDir(%q) = %q, %v", value, got, err)
				}
			}
			for _, value := range []string{"escape", "../outside"} {
				if _, err := manager.commandDir(value); err == nil {
					t.Errorf("accepted working directory %q outside the workspace", value)
				}
			}
		})
	}
}
