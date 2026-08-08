package claude

import (
	"slices"
	"testing"
)

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

func TestSessionModesExposeNormalizedModes(t *testing.T) {
	state := buildSessionModeState("")
	if state.CurrentModeId != "agent" {
		t.Fatalf("current mode = %q, want agent", state.CurrentModeId)
	}
	if len(state.AvailableModes) != 3 || state.AvailableModes[0].Id != "agent" || state.AvailableModes[1].Id != "plan" || state.AvailableModes[2].Id != "unattended" {
		t.Fatalf("available modes = %#v, want agent, plan, unattended", state.AvailableModes)
	}
}

func TestModesMapToClaudePermissionModes(t *testing.T) {
	s := New(Options{}).newSession("session", "/workspace", "default", "default", nil)
	for _, test := range []struct{ mode, permission string }{
		{"agent", "auto"},
		{"plan", "plan"},
		{"unattended", "bypassPermissions"},
	} {
		s.mode = test.mode
		args := s.cliArgsLocked()
		index := slices.Index(args, "--permission-mode")
		if index < 0 || index+1 >= len(args) || args[index+1] != test.permission {
			t.Errorf("%s args = %v, want --permission-mode %s", test.mode, args, test.permission)
		}
	}
}
