package fs_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/internal/testenv"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	. "github.com/adrianliechti/wingman-agent/pkg/agent/tool/fs"
)

func TestSkillDirectoryLinks(t *testing.T) {
	for _, kind := range []string{"symlink", "junction"} {
		for _, scope := range []string{"project", "personal"} {
			for _, placement := range []string{"root", "skill"} {
				t.Run(kind+"/"+scope+"/"+placement, func(t *testing.T) {
					workspace, shared := t.TempDir(), t.TempDir()
					source := filepath.Join(workspace, ".agents", "skills")
					if scope == "personal" {
						source = filepath.Join(t.TempDir(), ".agents", "skills")
					}
					target := filepath.Join(shared, "review")
					if err := os.MkdirAll(filepath.Join(target, "references"), 0755); err != nil {
						t.Fatal(err)
					}
					for path, body := range map[string]string{
						"SKILL.md":                              "---\nname: review\ndescription: Review code.\n---\nRead references/guide.md.\n",
						filepath.Join("references", "guide.md"): "Shared review guidance.\n",
					} {
						if err := os.WriteFile(filepath.Join(target, path), []byte(body), 0644); err != nil {
							t.Fatal(err)
						}
					}
					link, destination := source, shared
					if placement == "skill" {
						link, destination = filepath.Join(source, "review"), target
					}
					if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
						t.Fatal(err)
					}
					root, err := os.OpenRoot(workspace)
					if err != nil {
						t.Fatal(err)
					}
					defer root.Close()
					// Read roots are captured before links exist, as for skills
					// installed during a running session. A broader root comes
					// first to cover overlapping access grants.
					allowed := []string{workspace, source}
					if placement == "skill" {
						allowed = append(allowed, link)
					}
					read, glob, grep := ReadTool(root, allowed...), GlobTool(root, allowed...), GrepTool(root, allowed...)
					testenv.DirLink(t, kind, destination, link)

					paths := []string{filepath.Join(source, "review"), target}
					if scope == "project" {
						paths = append(paths, filepath.Join(".agents", "skills", "review"))
					}
					for _, path := range paths {
						for _, check := range []struct {
							name string
							tool tool.Tool
							args map[string]any
							want string
						}{
							{"skill", read, map[string]any{"file_path": filepath.Join(path, "SKILL.md")}, "name: review"},
							{"reference", read, map[string]any{"file_path": filepath.Join(path, "references", "guide.md")}, "Shared review guidance"},
							{"glob", glob, map[string]any{"path": path, "pattern": "**/*.md"}, "guide.md"},
							{"grep", grep, map[string]any{"path": path, "pattern": "Shared review guidance", "output_mode": "content"}, "Shared review guidance"},
						} {
							result, err := check.tool.Execute(context.Background(), check.args)
							if err != nil || !strings.Contains(result.Content, check.want) {
								t.Errorf("%s through %q = %q, %v", check.name, path, result.Content, err)
							}
						}
					}

					localFile := filepath.Join(workspace, "local.txt")
					if err := os.WriteFile(localFile, []byte("original"), 0644); err != nil {
						t.Fatal(err)
					}
					for _, path := range paths {
						file := filepath.Join(path, "references", "guide.md")
						for _, check := range []struct {
							tool tool.Tool
							args map[string]any
						}{
							{ReadTool(root), map[string]any{"file_path": file}},
							{WriteTool(root), map[string]any{"file_path": file, "content": "overwrite"}},
							{EditTool(root), map[string]any{"edits": []any{
								map[string]any{"file_path": localFile, "old_string": "original", "new_string": "changed"},
								map[string]any{"file_path": file, "old_string": "Shared", "new_string": "Changed"},
							}}},
						} {
							if _, err := check.tool.Execute(context.Background(), check.args); err == nil {
								t.Errorf("%s accessed %q without an access grant", check.tool.Name, file)
							}
						}
					}
					for file, want := range map[string]string{
						localFile: "original",
						filepath.Join(target, "references", "guide.md"): "Shared review guidance.\n",
					} {
						if data, err := os.ReadFile(file); err != nil || string(data) != want {
							t.Errorf("rejected mutation changed %s: %q, %v", file, data, err)
						}
					}
					// Granting a skill root must not let a resource link escape
					// that root to another directory.
					outside := t.TempDir()
					if err := os.WriteFile(filepath.Join(outside, "private.txt"), []byte("private"), 0644); err != nil {
						t.Fatal(err)
					}
					testenv.DirLink(t, kind, outside, filepath.Join(target, "escape"))
					testenv.DirLink(t, kind, filepath.Join(target, "escape"), filepath.Join(target, "chain"))
					for _, link := range []string{"escape", "chain"} {
						path := filepath.Join(source, "review", link)
						for _, check := range []struct {
							tool tool.Tool
							args map[string]any
						}{
							{read, map[string]any{"file_path": filepath.Join(path, "private.txt")}},
							{grep, map[string]any{"path": path, "pattern": "private"}},
							{glob, map[string]any{"path": path, "pattern": "**/*"}},
						} {
							if result, err := check.tool.Execute(context.Background(), check.args); err == nil {
								t.Errorf("%s escaped the allowed skill root through %s: %q", check.tool.Name, link, result.Content)
							}
						}
					}
				})
			}
		}
	}
}
