package codex

import (
	"slices"
	"strings"
	"testing"
)

func TestBuildArgsDisablesUnsupportedCodexFeatures(t *testing.T) {
	cfg := &CodexConfig{
		BaseURL:      "https://wingman.example/",
		ModelCatalog: "/tmp/wingman models.json",
	}
	args := BuildArgs(cfg)
	wantPrefix := []string{
		"--config", "agents.enabled=true",
		"--config", "features.multi_agent_v2=false",
	}
	if !slices.Equal(args[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("BuildArgs() prefix = %q, want %q", args[:len(wantPrefix)], wantPrefix)
	}
	for _, value := range []string{
		"model_catalog_json=\"/tmp/wingman models.json\"",
		"model_providers.wingman.supports_websockets=false",
		"otel.exporter=\"none\"",
		"otel.trace_exporter=\"none\"",
		"otel.metrics_exporter=\"none\"",
		"features.plugins=false",
	} {
		if !containsConfig(args, value) {
			t.Errorf("BuildArgs() = %q, missing %q", args, value)
		}
	}
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--config" && strings.HasPrefix(args[i+1], "model=") {
			t.Errorf("BuildArgs() sets model override %q", args[i+1])
		}
	}
}

func TestResolveModelsIncludesStandardAndFastOpenAIModels(t *testing.T) {
	models := resolveModels(map[string]bool{
		"gpt-5.6-luna":    true,
		"claude-sonnet-5": true,
		"gpt-5.4":         true,
	})

	if want := []string{"gpt-5.4", "gpt-5.6-luna"}; !slices.Equal(models, want) {
		t.Fatalf("models = %q, want %q", models, want)
	}
}

func containsConfig(args []string, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--config" && args[i+1] == value {
			return true
		}
	}
	return false
}
