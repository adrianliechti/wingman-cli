package debugadapter

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/adrianliechti/wingman-agent/internal/pathutil"
	"github.com/adrianliechti/wingman-agent/internal/testenv"
)

func TestScanLinkedWorkspaceRoot(t *testing.T) {
	for _, kind := range []string{"symlink", "junction"} {
		t.Run(kind, func(t *testing.T) {
			target := t.TempDir()
			for name, content := range map[string]string{
				"project/main.go":  "package main\nfunc main() {}",
				"project/App.java": "class App {}",
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
			targets, err := NewRegistry().DetectWorkspace(context.Background(), root)
			if err != nil || len(targets) != 1 || targets[0].Path != "project/main.go" {
				t.Fatalf("targets = %#v, %v", targets, err)
			}
			source, err := findJavaSource(root, "App")
			if err != nil || source != filepath.Join(root, "project", "App.java") {
				t.Fatalf("Java source = %q, %v", source, err)
			}
			output := filepath.Join("target", "debug", "unbuilt")
			got, err := canonicalCargoPath(filepath.Join(root, output))
			want, wantErr := pathutil.Resolve(target)
			if err != nil || wantErr != nil || got != filepath.Join(want, output) {
				t.Fatalf("Cargo output = %q, %v; target resolution = %v", got, err, wantErr)
			}
		})
	}
}
