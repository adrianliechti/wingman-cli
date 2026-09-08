package fs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adrianliechti/wingman-agent/internal/testenv"
)

func TestFileTargetRejectsReplacedAllowedRoot(t *testing.T) {
	for _, kind := range []string{"symlink", "junction"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			workspace := filepath.Join(base, "workspace")
			allowed, outside := filepath.Join(base, "allowed"), filepath.Join(base, "outside")
			for _, dir := range []string{workspace, allowed, outside} {
				if err := os.Mkdir(dir, 0755); err != nil {
					t.Fatal(err)
				}
			}
			for dir, content := range map[string]string{allowed: "allowed", outside: "secret"} {
				if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			}
			root, err := os.OpenRoot(workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			target, err := resolveFileTarget(filepath.Join(allowed, "note.txt"), workspace, []string{allowed}, "read file")
			if err != nil {
				t.Fatal(err)
			}
			search, err := resolveSearchTarget(allowed, workspace, root, []string{allowed}, "search")
			if err != nil {
				t.Fatal(err)
			}
			defer search.Close()
			// Replace the authorized directory between target resolution and I/O.
			if err := os.Rename(allowed, filepath.Join(base, "moved")); err != nil {
				t.Fatal(err)
			}
			testenv.DirLink(t, kind, outside, allowed)
			if _, err := statFileTarget(root, target); err == nil {
				t.Error("stat followed a replacement allowed root")
			}
			if data, err := readFileTarget(root, target); err == nil {
				t.Errorf("read followed a replacement allowed root: %q", data)
			}
			if file, err := openFileTarget(root, target); err == nil {
				file.Close()
				t.Error("streaming read followed a replacement allowed root")
			}
			if err := writeFileTarget(root, target, "overwrite"); err == nil {
				t.Error("write followed a replacement allowed root")
			}
			if _, _, closeRoot, err := transactionLocation(root, target); err == nil {
				if closeRoot != nil {
					closeRoot()
				}
				t.Error("edit transaction followed a replacement allowed root")
			}
			if data, err := os.ReadFile(filepath.Join(outside, "note.txt")); err != nil || string(data) != "secret" {
				t.Errorf("outside file was changed: %q, %v", data, err)
			}
			// Searches retain their original handle for the whole request.
			if data, err := search.Root.ReadFile("note.txt"); err != nil || string(data) != "allowed" {
				t.Errorf("search lost its original allowed root: %q, %v", data, err)
			}
		})
	}
}
