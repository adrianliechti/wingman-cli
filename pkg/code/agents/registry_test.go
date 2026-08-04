package agents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/code"
)

func fakeExecutable(t *testing.T, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("test executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func registrationIDs(registrations []Registration) []string {
	ids := make([]string, 0, len(registrations))
	for _, registration := range registrations {
		ids = append(ids, registration.ID)
	}
	return ids
}

func setTestHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

func TestNativeOptionsPreserveEnvironmentWithoutProviderOverrides(t *testing.T) {
	setTestHome(t, t.TempDir())
	env := []string{
		"PATH=/usr/bin",
		"ANTHROPIC_API_KEY=anthropic-key",
		"OPENAI_API_KEY=openai-key",
	}

	claude := nativeClaudeOptions("/workspace", env)
	if claude.Cwd != "/workspace" || !slices.Equal(claude.Env, env) {
		t.Fatalf("nativeClaudeOptions() = %#v", claude)
	}

	codex := nativeCodexOptions("/workspace", env)
	if codex.Dir != "/workspace" || !slices.Equal(codex.Env, env) {
		t.Fatalf("nativeCodexOptions() = %#v", codex)
	}
	if len(codex.ExtraArgs) != 0 {
		t.Fatalf("nativeCodexOptions().ExtraArgs = %q, want no provider overrides", codex.ExtraArgs)
	}

	pi := nativePiOptions("/workspace", env)
	if pi.Dir != "/workspace" || !slices.Equal(pi.Env, env) || len(pi.Args) != 0 {
		t.Fatalf("nativePiOptions() = %#v", pi)
	}
}

func TestDetectedHonorsExternalPathOverrides(t *testing.T) {
	overrides := map[string]string{
		"WINGMAN_CLAUDE_PATH":   "claude",
		"WINGMAN_CODEX_PATH":    "codex",
		"WINGMAN_COPILOT_PATH":  "copilot",
		"WINGMAN_OPENCODE_PATH": "opencode",
		"WINGMAN_PI_PATH":       "pi",
	}
	for env, name := range overrides {
		t.Setenv(env, fakeExecutable(t, name))
	}

	ids := registrationIDs(detected())
	for _, name := range []string{"claude", "codex", "copilot", "opencode", "pi"} {
		if !slices.Contains(ids, name) {
			t.Errorf("detected() = %q, missing %q path override", ids, name)
		}
	}
}

func TestAvailableMergesConfiguredAndDetectedAgents(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	t.Setenv("WINGMAN_CODEX_PATH", fakeExecutable(t, "codex"))

	custom := fakeExecutable(t, "custom-acp")
	defs := []code.AgentDef{
		{Name: "CODEX", Command: custom},
		{Name: "Custom ACP", Command: custom},
		{Name: "Wingman", Command: custom},
	}
	data, err := json.Marshal(defs)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".wingman")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agents.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	ids := registrationIDs(Available())
	if !slices.Contains(ids, "codex") || !slices.Contains(ids, "custom-acp") {
		t.Fatalf("Available() = %q, want detected and configured agents", ids)
	}
	if slices.Contains(ids, "wingman") {
		t.Fatalf("Available() = %q, configured agent shadowed the built-in name", ids)
	}
	for _, registration := range Available() {
		if registration.ID == "codex" && registration.Name != "CODEX" {
			t.Fatalf("configured codex did not replace detected codex: %#v", registration)
		}
	}
}
