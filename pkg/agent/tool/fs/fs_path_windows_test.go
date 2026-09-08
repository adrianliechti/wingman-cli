package fs_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	. "github.com/adrianliechti/wingman-agent/pkg/agent/tool/fs"
)

func TestWindowsFileToolPaths(t *testing.T) {
	root, workspace, allowed, cleanup := createWriteRootSetup(t)
	defer cleanup()

	forms := []struct {
		name   string
		format func(string) string
	}{
		{"native", func(p string) string { return p }},
		{"forward slashes", filepath.ToSlash},
		{"leading slash", func(p string) string { return "/" + p }},
		{"leading slash with forward slashes", func(p string) string { return "/" + filepath.ToSlash(p) }},
		{"doubled separators", func(p string) string { return strings.ReplaceAll(p, `\`, `\\`) }},
		{"leading slash with doubled separators", func(p string) string { return "/" + strings.ReplaceAll(p, `\`, `\\`) }},
	}
	for _, scope := range []struct{ name, dir string }{{"workspace", workspace}, {"allowed root", allowed}} {
		t.Run(scope.name, func(t *testing.T) {
			dir := filepath.Join(scope.dir, "go cli")
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(dir, "table.go")
			if err := os.WriteFile(file, []byte("type Table struct{}\n"), 0644); err != nil {
				t.Fatal(err)
			}
			for _, form := range forms {
				t.Run(form.name, func(t *testing.T) {
					for _, test := range []struct {
						name string
						tool tool.Tool
						args map[string]any
						want string
					}{
						{"read", ReadTool(root, allowed), map[string]any{"file_path": form.format(file)}, "type Table struct{}"},
						{"grep file", GrepTool(root, allowed), map[string]any{"path": form.format(file), "pattern": "Table|table"}, "table.go"},
						{"grep directory", GrepTool(root, allowed), map[string]any{"path": form.format(dir), "pattern": "Table|table"}, "table.go"},
						{"glob path", GlobTool(root, allowed), map[string]any{"path": form.format(dir), "pattern": "**/*.go"}, "table.go"},
						{"glob absolute pattern", GlobTool(root, allowed), map[string]any{"pattern": form.format(filepath.Join(dir, "**", "*.go"))}, "table.go"},
					} {
						t.Run(test.name, func(t *testing.T) {
							// Tool arguments arrive as JSON; decoding must leave native
							// backslashes intact without adding quotes or unescaping twice.
							encoded, err := json.Marshal(test.args)
							if err != nil {
								t.Fatal(err)
							}
							var args map[string]any
							if err := json.Unmarshal(encoded, &args); err != nil {
								t.Fatal(err)
							}
							result, err := test.tool.Execute(context.Background(), args)
							if err != nil {
								t.Fatal(err)
							}
							if !strings.Contains(result.Content, test.want) {
								t.Fatalf("result = %q, want %q", result.Content, test.want)
							}
						})
					}
				})
			}
		})
	}
}

func TestWindowsLeadingDriveSlashPreservesAccessChecks(t *testing.T) {
	root, _, denied, cleanup := createWriteRootSetup(t)
	defer cleanup()
	file := filepath.Join(denied, "table.go")
	for _, test := range []struct {
		name string
		tool tool.Tool
		args map[string]any
	}{
		{"read", ReadTool(root), map[string]any{"file_path": "/" + file}},
		{"grep", GrepTool(root), map[string]any{"path": "/" + denied, "pattern": "Table"}},
		{"glob", GlobTool(root), map[string]any{"path": "/" + denied, "pattern": "**/*.go"}},
		{"glob absolute pattern", GlobTool(root), map[string]any{"pattern": "/" + filepath.Join(denied, "**", "*.go")}},
		{"write", WriteTool(root), map[string]any{"file_path": "/" + file, "content": "type Table struct{}"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.tool.Execute(context.Background(), test.args)
			if err == nil || !strings.Contains(err.Error(), "outside workspace") {
				t.Fatalf("expected workspace access error, got %v", err)
			}
		})
	}
}
