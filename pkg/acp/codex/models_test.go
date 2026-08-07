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

func TestSessionModesExposeNormalizedPolicies(t *testing.T) {
	state := buildSessionModeState("")
	if state.CurrentModeId != "agent" {
		t.Fatalf("current mode = %q, want agent", state.CurrentModeId)
	}
	if len(state.AvailableModes) != 3 || state.AvailableModes[0].Id != "agent" || state.AvailableModes[1].Id != "plan" || state.AvailableModes[2].Id != "unattended" {
		t.Fatalf("available modes = %#v, want agent, plan, unattended", state.AvailableModes)
	}
	if mode := modeFor("plan"); mode.approvalPolicy != "on-request" || mode.sandboxPolicy.(map[string]any)["type"] != "readOnly" {
		t.Fatalf("plan policy = %#v", mode)
	}
	if mode := modeFor("unattended"); mode.approvalPolicy != "never" || mode.sandboxPolicy.(map[string]any)["type"] != "dangerFullAccess" {
		t.Fatalf("unattended policy = %#v", mode)
	}
}
