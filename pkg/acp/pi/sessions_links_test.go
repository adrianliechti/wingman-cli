package pi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/adrianliechti/wingman-agent/internal/testenv"
)

func TestSessionsDirectoryLinks(t *testing.T) {
	for _, kind := range []string{"symlink", "junction"} {
		t.Run(kind, func(t *testing.T) {
			target := t.TempDir()
			root := filepath.Join(t.TempDir(), "sessions")
			testenv.DirLink(t, kind, target, root)
			if canonicalPath(root) != canonicalPath(target) {
				t.Fatal("directory aliases do not match the same session workspace")
			}
			outside := t.TempDir()
			for dir, id := range map[string]string{target: "inside", outside: "outside"} {
				data, err := json.Marshal(map[string]string{"type": "session", "id": id, "cwd": target})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), append(data, '\n'), 0644); err != nil {
					t.Fatal(err)
				}
			}
			testenv.DirLink(t, kind, outside, filepath.Join(target, "escape"))
			testenv.DirLink(t, kind, target, filepath.Join(target, "loop"))
			files := listSessionFiles(root)
			if len(files) != 1 || files[0].ID != "inside" || files[0].Path != filepath.Join(root, "session.jsonl") {
				t.Fatalf("sessions = %#v", files)
			}
		})
	}
}
