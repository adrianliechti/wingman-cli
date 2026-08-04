package server

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestNativeAgentOptionsPreserveEnvironmentWithoutProviderOverrides(t *testing.T) {
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
}

func TestDetectAgentsHonorsNativePathOverrides(t *testing.T) {
	dir := t.TempDir()
	paths := map[string]string{}
	for _, name := range []string{"claude", "codex", "pi"} {
		filename := name
		if runtime.GOOS == "windows" {
			filename += ".exe"
		}
		path := filepath.Join(dir, filename)
		if err := os.WriteFile(path, []byte("test executable"), 0o755); err != nil {
			t.Fatal(err)
		}
		paths[name] = path
	}

	t.Setenv("WINGMAN_CLAUDE_PATH", paths["claude"])
	t.Setenv("WINGMAN_CODEX_PATH", paths["codex"])
	t.Setenv("WINGMAN_PI_PATH", paths["pi"])

	found := map[string]bool{}
	for _, registration := range detectAgents() {
		found[registration.ID] = true
	}
	for _, name := range []string{"claude", "codex", "pi"} {
		if !found[name] {
			t.Errorf("detectAgents() did not include %q from its path override", name)
		}
	}
}
