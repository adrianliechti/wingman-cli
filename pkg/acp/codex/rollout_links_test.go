package codex

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adrianliechti/wingman-agent/internal/testenv"
)

func TestRolloutDirectoryLinks(t *testing.T) {
	for _, kind := range []string{"symlink", "junction"} {
		t.Run(kind, func(t *testing.T) {
			target := t.TempDir()
			outside := t.TempDir()
			for dir, id := range map[string]string{target: "inside", outside: "outside"} {
				path := filepath.Join(dir, "2026", "rollout-"+id+".jsonl")
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("{}\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			root := filepath.Join(t.TempDir(), "sessions")
			testenv.DirLink(t, kind, target, root)
			testenv.DirLink(t, kind, outside, filepath.Join(target, "escape"))
			testenv.DirLink(t, kind, target, filepath.Join(target, "loop"))
			if got := findRolloutFile(root, "inside"); got != filepath.Join(root, "2026", "rollout-inside.jsonl") {
				t.Fatalf("rollout = %q", got)
			}
			if got := findRolloutFile(root, "outside"); got != "" {
				t.Fatalf("found rollout outside the session root: %q", got)
			}
		})
	}
}
