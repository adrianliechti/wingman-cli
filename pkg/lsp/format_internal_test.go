package lsp

import (
	"path/filepath"
	"testing"
)

func TestFindProjectUsesDeepestMatchingRoot(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "web")
	projects := []projectRoot{
		{Dir: root, Server: Server{Command: "gopls", Args: []string{"serve"}, Languages: []string{"go"}}},
		{Dir: nested, Server: Server{Command: "gopls", Args: []string{"serve"}, Languages: []string{"go"}}},
	}

	project := findProject(projects, filepath.Join(nested, "MAIN.GO"))
	if project == nil || project.Dir != nested {
		t.Fatalf("findProject = %+v, want nested project %q", project, nested)
	}
}

func TestProjectKeyIncludesServerArguments(t *testing.T) {
	dir := t.TempDir()
	left := projectRoot{Dir: dir, Server: Server{Command: "server", Args: []string{"--stdio"}}}
	right := projectRoot{Dir: dir, Server: Server{Command: "server", Args: []string{"lsp", "--stdio"}}}
	if projectKey(left) == projectKey(right) {
		t.Fatal("project keys should differ when server arguments differ")
	}
}
