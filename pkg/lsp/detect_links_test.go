package lsp

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/adrianliechti/wingman-agent/internal/testenv"
)

func TestScanLinkedWorkspaceRoot(t *testing.T) {
	for _, kind := range []string{"symlink", "junction"} {
		t.Run(kind, func(t *testing.T) {
			target := t.TempDir()
			for name, content := range map[string]string{
				"project/go.mod": "module example",
			} {
				path := filepath.Join(target, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			}
			root := filepath.Join(t.TempDir(), "workspace")
			testenv.DirLink(t, kind, target, root)
			outside := t.TempDir()
			for _, name := range []string{"go.mod", "secret.go"} {
				if err := os.WriteFile(filepath.Join(outside, name), []byte("package main\nfunc main() {}"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			testenv.DirLink(t, kind, outside, filepath.Join(target, "escape"))
			testenv.DirLink(t, kind, target, filepath.Join(target, "loop"))
			index := indexWorkspace(root)
			if got := index.matching("go.mod"); !slices.Equal(got, []string{filepath.Join(root, "project", "go.mod")}) {
				t.Fatalf("project markers = %v", got)
			}
		})
	}
}
