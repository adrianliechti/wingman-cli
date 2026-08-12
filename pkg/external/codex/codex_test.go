package codex

import (
	"slices"
	"testing"
)

func TestBuildArgsDisablesUnsupportedCodexFeatures(t *testing.T) {
	cfg := &CodexConfig{
		BaseURL: "https://wingman.example/",
		Model:   "test-model",
	}
	args := BuildArgs(cfg)
	wantPrefix := []string{
		"--config", "agents.enabled=false",
		"--config", "features.multi_agent_v2=false",
	}
	if !slices.Equal(args[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("BuildArgs() prefix = %q, want %q", args[:len(wantPrefix)], wantPrefix)
	}
	for _, value := range []string{
		"otel.exporter=\"none\"",
		"otel.trace_exporter=\"none\"",
		"otel.metrics_exporter=\"none\"",
		"features.plugins=false",
	} {
		if !containsConfig(args, value) {
			t.Errorf("BuildArgs() = %q, missing %q", args, value)
		}
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
