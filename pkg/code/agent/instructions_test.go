package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalShellAndTimezoneFromEnvironment(t *testing.T) {
	t.Setenv("SHELL", filepath.Join("opt", "bin", "zsh"))
	t.Setenv("TZ", "Europe/Berlin")

	if got := localShell(); got != "zsh" {
		t.Fatalf("localShell() = %q, want zsh", got)
	}
	if got := localTimezone(time.Now()); got != "Europe/Berlin" {
		t.Fatalf("localTimezone() = %q, want Europe/Berlin", got)
	}
}

func TestProjectInstructionsRootFirstAndDeduped(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}

	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	write(filepath.Join(root, "AGENTS.md"), "root rules")
	write(filepath.Join(sub, "AGENTS.md"), "sub rules")
	write(filepath.Join(sub, "CLAUDE.md"), "sub rules")

	found := findProjectInstructions(sub)
	if len(found) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(found))
	}
	if filepath.Dir(found[0].path) != root {
		t.Fatalf("expected root-level file first, got %s", found[0].path)
	}
	if filepath.Base(found[1].path) != "AGENTS.md" {
		t.Fatalf("expected AGENTS.md before CLAUDE.md within a directory, got %s", found[1].path)
	}

	rendered, mtimes := renderProjectInstructions(found)
	if len(mtimes) != 3 {
		t.Fatalf("expected all read files tracked for cache invalidation, got %d", len(mtimes))
	}
	if strings.Count(rendered, "sub rules") != 1 {
		t.Fatalf("duplicate CLAUDE.md content not deduped:\n%s", rendered)
	}
	if strings.Index(rendered, "root rules") > strings.Index(rendered, "sub rules") {
		t.Fatalf("root guidance should precede the more specific file:\n%s", rendered)
	}
}

func TestProjectInstructionsBudgetKeepsMostSpecific(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}

	huge := strings.Repeat("general guidance line\n", projectInstructionsMaxBytes/20)
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(huge), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "AGENTS.md"), []byte("subproject-specific rule"), 0644); err != nil {
		t.Fatal(err)
	}

	rendered, _ := renderProjectInstructions(findProjectInstructions(sub))
	if !strings.Contains(rendered, "subproject-specific rule") {
		t.Fatal("the most specific instruction file must survive the budget cut")
	}
	if !strings.Contains(rendered, "omitted") {
		t.Fatalf("expected an omission notice, got:\n%.200s", rendered)
	}
}
