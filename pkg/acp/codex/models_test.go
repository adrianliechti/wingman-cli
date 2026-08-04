package codex

import "testing"

func TestNormalizeSessionConfig(t *testing.T) {
	models := []modelEntry{
		{ID: "gpt-default", Default: true, EffortLevels: []string{"low", "high"}},
		{ID: "gpt-other", EffortLevels: []string{"medium"}},
	}

	model, effort := normalizeSessionConfig(models, "default", "maximum")
	if model != "gpt-default" || effort != "default" {
		t.Fatalf("normalizeSessionConfig() = %q, %q", model, effort)
	}

	model, effort = normalizeSessionConfig(models, "gpt-other", "medium")
	if model != "gpt-other" || effort != "medium" {
		t.Fatalf("normalizeSessionConfig() = %q, %q", model, effort)
	}
}
