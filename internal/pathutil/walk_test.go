package pathutil_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/adrianliechti/wingman-agent/internal/pathutil"
	"github.com/adrianliechti/wingman-agent/internal/testenv"
)

func TestWalkDirLinkedRoot(t *testing.T) {
	for _, kind := range []string{"symlink", "junction"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			target, outside := filepath.Join(base, "target"), filepath.Join(base, "outside")
			writeWalkFile(t, filepath.Join(target, "src", "main.go"))
			writeWalkFile(t, filepath.Join(outside, "secret.go"))
			root := filepath.Join(base, "workspace")
			testenv.DirLink(t, kind, target, root)
			testenv.DirLink(t, kind, outside, filepath.Join(target, "escape"))
			testenv.DirLink(t, kind, target, filepath.Join(target, "loop"))
			// Verify both a linked root and a link in the root's parents.
			for _, selected := range []string{root, filepath.Join(root, "src")} {
				var files []string
				err := pathutil.WalkDir(selected, func(path string, entry fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if path == selected && !entry.IsDir() {
						t.Fatal("selected root is not a directory")
					}
					if entry.Type().IsRegular() {
						files = append(files, path)
					}
					return nil
				})
				if err != nil || !slices.Equal(files, []string{filepath.Join(root, "src", "main.go")}) {
					t.Fatalf("WalkDir(%q) = %v, %v", selected, files, err)
				}
			}
		})
	}
}

func TestWalkDirRejectsDirectoryReplacedByEscapingLink(t *testing.T) {
	for _, kind := range []string{"symlink", "junction"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			root, outside := filepath.Join(base, "root"), filepath.Join(base, "outside")
			changing := filepath.Join(root, "changing")
			writeWalkFile(t, filepath.Join(changing, "safe.go"))
			writeWalkFile(t, filepath.Join(outside, "secret.go"))
			blocked := false
			err := pathutil.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
				if path == changing {
					if err != nil {
						blocked = true
						return nil
					}
					if err := os.Rename(changing, filepath.Join(base, "moved")); err != nil {
						t.Fatal(err)
					}
					testenv.DirLink(t, kind, outside, changing)
				}
				if entry != nil && entry.Name() == "secret.go" {
					t.Fatal("walk escaped the root after directory replacement")
				}
				return err
			})
			if err != nil || !blocked {
				t.Fatalf("walk error = %v, escape blocked = %v", err, blocked)
			}
		})
	}
}

func TestWalkDirErrorsAndPruning(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing")
	calls := 0
	err := pathutil.WalkDir(missing, func(path string, entry fs.DirEntry, err error) error {
		calls++
		if path != missing || entry != nil || !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("missing root callback = %q, %v, %v", path, entry, err)
		}
		return fs.SkipAll
	})
	if err != nil || calls != 1 {
		t.Fatalf("missing root: calls = %d, err = %v", calls, err)
	}
	writeWalkFile(t, filepath.Join(root, "skip", "hidden.go"))
	writeWalkFile(t, filepath.Join(root, "visible.go"))
	var files []string
	err = pathutil.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Name() == "skip" {
			return fs.SkipDir
		}
		if entry.Type().IsRegular() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil || !slices.Equal(files, []string{filepath.Join(root, "visible.go")}) {
		t.Fatalf("pruned walk = %v, %v", files, err)
	}
}

func writeWalkFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatal(err)
	}
}
