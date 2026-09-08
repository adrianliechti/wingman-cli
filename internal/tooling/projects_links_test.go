package tooling

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
				"project/go.mod":  "module example",
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
			projects, err := DetectProject(context.Background(), root, ProjectSpec{Markers: []string{"go.mod"}, Extensions: []string{".go"}})
			if err != nil || !slices.Equal(projects, []string{filepath.Join(root, "project")}) {
				t.Fatalf("projects = %v, %v", projects, err)
			}
		})
	}
}
