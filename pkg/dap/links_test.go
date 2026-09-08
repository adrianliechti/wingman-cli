package dap

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adrianliechti/wingman-agent/internal/testenv"
)

func TestWorkspaceDirectoryLinkBoundaries(t *testing.T) {
	for _, kind := range []string{"symlink", "junction"} {
		t.Run(kind, func(t *testing.T) {
			target := t.TempDir()
			writeTestFile(t, filepath.Join(target, "project", "main.go"), "package main")
			root := filepath.Join(t.TempDir(), "workspace")
			testenv.DirLink(t, kind, target, root)
			testenv.DirLink(t, kind, filepath.Join(target, "project"), filepath.Join(target, "inside"))
			outside := t.TempDir()
			writeTestFile(t, filepath.Join(outside, "main.go"), "package main")
			testenv.DirLink(t, kind, outside, filepath.Join(target, "escape"))
			for _, project := range []string{"project", "inside"} {
				if got, err := ResolveWorkspaceDirectory(root, project); err != nil || got != filepath.Join(root, project) {
					t.Errorf("ResolveWorkspaceDirectory(%q) = %q, %v", project, got, err)
				}
			}
			if _, err := ResolveWorkspaceDirectory(root, "escape"); err == nil {
				t.Fatal("accepted escaping project directory")
			}
			for _, file := range []string{"main.go", "missing/output"} {
				for _, project := range []string{"inside", "escape"} {
					_, err := ResolveConfigurationPaths(root, root, []ConfigurationPath{{Key: "program", AllowMissing: true}}, map[string]any{
						"program": filepath.Join(project, filepath.FromSlash(file)),
					})
					if (err != nil) != (project == "escape") {
						t.Errorf("program %s/%s: %v", project, file, err)
					}
				}
			}
			dangling := t.TempDir()
			testenv.DirLink(t, kind, dangling, filepath.Join(target, "dangling"))
			if err := os.Remove(dangling); err != nil {
				t.Fatal(err)
			}
			if _, err := ResolveConfigurationPaths(root, root, []ConfigurationPath{{Key: "program", AllowMissing: true}}, map[string]any{
				"program": filepath.Join("dangling", "output"),
			}); err == nil {
				t.Fatal("accepted missing output below a dangling link")
			}
		})
	}
}
