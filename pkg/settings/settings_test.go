package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/internal/testenv"
)

func TestSettingsDefaultAndUpdate(t *testing.T) {
	home := testenv.WingmanHome(t)

	initial, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(initial.Workspaces) != 0 {
		t.Fatalf("initial workspaces = %q, want none", initial.Workspaces)
	}
	if !initial.EditorTabCompletion {
		t.Fatal("editor.tab.completion is disabled by default")
	}

	updated, err := Update(func(value *Settings) {
		value.EditorTabCompletion = false
		value.AddWorkspace("first")
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Workspaces) != 1 || updated.Workspaces[0] != "first" {
		t.Fatalf("workspaces = %q, want first", updated.Workspaces)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.EditorTabCompletion {
		t.Fatal("disabled editor.tab.completion was not persisted")
	}

	path := filepath.Join(home, "config.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config permissions = %o, want 600", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"editor.tab.completion": false`) {
		t.Fatalf("disabled editor.tab.completion was omitted from %s", data)
	}
}

func TestSettingsMissingTabPreferenceDefaultsOn(t *testing.T) {
	home := testenv.WingmanHome(t)
	if err := os.WriteFile(
		filepath.Join(home, "config.json"),
		[]byte(`{"workspaces":["first"]}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.EditorTabCompletion {
		t.Fatal("missing editor.tab.completion did not default on")
	}
}

func TestSettingsRejectsInvalidJSONWithoutOverwritingIt(t *testing.T) {
	home := testenv.WingmanHome(t)
	path := filepath.Join(home, "config.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Update(func(value *Settings) {
		value.AddWorkspace("first")
	}); err == nil {
		t.Fatal("invalid config was accepted")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{" {
		t.Fatalf("invalid config was overwritten with %q", data)
	}
}

func TestSettingsUseWingmanHome(t *testing.T) {
	home := testenv.WingmanHome(t)
	got, err := path()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "config.json")
	if got != want {
		t.Fatalf("path() = %q, want %q", got, want)
	}
}
