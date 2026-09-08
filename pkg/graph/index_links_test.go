package graph

import (
	"context"
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
				"project/main.go": "package main\nfunc main() {}",
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
			engine := New(root, "")
			status, err := engine.Index(context.Background())
			if err != nil || !status.Indexed || status.Files != 1 || status.Nodes == 0 {
				t.Fatalf("index status = %+v, err = %v", status, err)
			}
			files, err := collectFiles(context.Background(), root)
			if err != nil || !slices.Equal(files, []string{filepath.Join(root, "project", "main.go")}) {
				t.Fatalf("files = %v, %v", files, err)
			}
		})
	}
}
