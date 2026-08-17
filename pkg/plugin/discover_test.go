package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/internal/testenv"
	"github.com/adrianliechti/wingman-agent/pkg/mcp"
	"github.com/adrianliechti/wingman-agent/pkg/skill"
)

func installPlugin(t *testing.T, dir, name string) {
	t.Helper()

	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}

	manifest := `{"$schema": "` + ManifestSchema + `", "name": "` + name + `"}`

	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0644); err != nil {
		t.Fatalf("write manifest %s: %v", dir, err)
	}
}

func isolateHome(t *testing.T) (string, string) {
	t.Helper()

	return testenv.UserHome(t), testenv.WingmanHome(t)
}

func TestDiscoverPrefersWingmanOverAgentsAndClaude(t *testing.T) {
	isolateHome(t)

	work := t.TempDir()

	installPlugin(t, filepath.Join(work, ".claude", "plugins", "from-claude"), "shared")
	installPlugin(t, filepath.Join(work, ".agents", "plugins", "from-agents"), "shared")
	installPlugin(t, filepath.Join(work, ".wingman", "plugins", "from-wingman"), "shared")

	plugins, diagnostics := Discover(work, t.TempDir(), t.TempDir())

	if len(plugins) != 1 {
		t.Fatalf("plugins = %#v, want one", plugins)
	}

	if !strings.Contains(plugins[0].Root, "from-wingman") {
		t.Fatalf("winner = %q, want the .wingman copy", plugins[0].Root)
	}

	if len(diagnostics) != 2 {
		t.Fatalf("diagnostics = %v, want both losers reported", diagnostics)
	}
}

func TestDiscoverPrefersProjectOverPersonal(t *testing.T) {
	_, wingmanHome := isolateHome(t)
	work := t.TempDir()

	installPlugin(t, filepath.Join(wingmanHome, "plugins", "personal"), "shared")
	installPlugin(t, filepath.Join(work, ".claude", "plugins", "project"), "shared")

	plugins, _ := Discover(work, t.TempDir(), t.TempDir())

	if len(plugins) != 1 || !strings.Contains(plugins[0].Root, "project") {
		t.Fatalf("plugins = %#v, want the project copy", plugins)
	}
}

func TestDiscoverSkipsDirectoriesWithoutManifest(t *testing.T) {
	isolateHome(t)

	work := t.TempDir()

	if err := os.MkdirAll(filepath.Join(work, ".wingman", "plugins", "not-a-plugin"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	plugins, diagnostics := Discover(work, t.TempDir(), t.TempDir())

	if len(plugins) != 0 || len(diagnostics) != 0 {
		t.Fatalf("plugins = %#v, diagnostics = %v, want both empty", plugins, diagnostics)
	}
}

func TestDiscoverReportsInvalidPluginAndKeepsGoing(t *testing.T) {
	isolateHome(t)

	work := t.TempDir()
	root := filepath.Join(work, ".wingman", "plugins")

	installPlugin(t, filepath.Join(root, "good"), "good")

	broken := filepath.Join(root, "broken")
	if err := os.MkdirAll(broken, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(broken, "plugin.json"), []byte(`{"name": "Broken"}`), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	plugins, diagnostics := Discover(work, t.TempDir(), t.TempDir())

	if len(plugins) != 1 || plugins[0].Name != "good" {
		t.Fatalf("plugins = %#v, want only the valid one", plugins)
	}

	if len(diagnostics) != 1 || !strings.Contains(diagnostics[0].Message, "$schema") {
		t.Fatalf("diagnostics = %v, want the broken plugin reported", diagnostics)
	}
}

func TestServersResolveEarliestPluginFirst(t *testing.T) {
	plugins := []Plugin{
		{Name: "first", Root: "/a", Servers: map[string]mcp.ServerConfig{
			"shared": {URL: "https://first.example/mcp"},
		}},
		{Name: "second", Root: "/b", Servers: map[string]mcp.ServerConfig{
			"shared": {URL: "https://second.example/mcp"},
			"own":    {URL: "https://second.example/own"},
		}},
	}

	servers, diagnostics := Servers(plugins)

	if len(servers) != 2 {
		t.Fatalf("servers = %#v, want two", servers)
	}

	if servers["shared"].URL != "https://first.example/mcp" {
		t.Fatalf("shared = %#v, want the first plugin's copy", servers["shared"])
	}

	if len(diagnostics) != 1 || !strings.Contains(diagnostics[0].Message, "shadowed") {
		t.Fatalf("diagnostics = %v, want the collision reported", diagnostics)
	}
}

func TestSkillsPreserveDiscoveryOrder(t *testing.T) {
	plugins := []Plugin{
		{Name: "a", Skills: []skill.Skill{{Name: "one", Plugin: "a"}}},
		{Name: "b", Skills: []skill.Skill{{Name: "two", Plugin: "b"}}},
	}

	skills := Skills(plugins)

	if len(skills) != 2 || skills[0].Name != "one" || skills[1].Name != "two" {
		t.Fatalf("skills = %#v", skills)
	}
}
