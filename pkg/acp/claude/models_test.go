package claude

import "testing"

func TestNormalizeSessionConfig(t *testing.T) {
	models := []ModelEntry{{
		ID: "claude-sonnet", Name: "Sonnet", ResolvedModel: "claude-sonnet-4-5",
		EffortLevels: []string{"low", "high"},
	}}

	model, effort := normalizeSessionConfig(models, "Sonnet", "maximum")
	if model != "claude-sonnet" || effort != "default" {
		t.Fatalf("normalizeSessionConfig() = %q, %q", model, effort)
	}

	model, effort = normalizeSessionConfig(models, "claude-sonnet", "high")
	if model != "claude-sonnet" || effort != "high" {
		t.Fatalf("normalizeSessionConfig() = %q, %q", model, effort)
	}
}
