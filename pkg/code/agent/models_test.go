package agent

import (
	"context"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/code"
)

func upstreamAgent(ids ...string) *Agent {
	models := make(map[string]bool, len(ids))
	for _, id := range ids {
		models[id] = true
	}
	return &Agent{upstreamModels: models, sessions: map[string]*sessionState{}}
}

func TestModelSelectionByRole(t *testing.T) {
	a := upstreamAgent("claude-sonnet-5", "claude-opus-4-8", "claude-haiku-4-5", "claude-fable-5")
	s := &sessionState{}

	if _, current := a.modelsFor(s); current != "claude-sonnet-5" {
		t.Fatalf("code model = %q, want claude-sonnet-5", current)
	}

	s.planMode.Store(true)
	if _, current := a.modelsFor(s); current != "claude-opus-4-8" {
		t.Fatalf("plan model = %q, want claude-opus-4-8", current)
	}

	if got := a.utilityModel(); got != "claude-haiku-4-5" {
		t.Fatalf("utility model = %q, want claude-haiku-4-5", got)
	}
}

func TestModelSelectionRoleScoped(t *testing.T) {
	a := upstreamAgent("claude-sonnet-5", "claude-opus-4-8", "claude-fable-5")
	a.modelID = "claude-fable-5"
	s := &sessionState{}

	if _, current := a.modelsFor(s); current != "claude-fable-5" {
		t.Fatalf("explicit code model overridden: %q", current)
	}

	// The coding choice must not leak into plan mode: plan picks large.
	s.planMode.Store(true)
	if _, current := a.modelsFor(s); current != "claude-opus-4-8" {
		t.Fatalf("plan model = %q, want claude-opus-4-8", current)
	}
}

func TestModelSelectionCrossFamilyFallback(t *testing.T) {
	// Claude family with no small model: utility falls back to another family.
	a := upstreamAgent("claude-sonnet-5", "gpt-5.6-luna")
	if got := a.utilityModel(); got != "gpt-5.6-luna" {
		t.Fatalf("utility model = %q, want gpt-5.6-luna", got)
	}

	// GPT-only gateway anchors every role in the gpt family.
	g := upstreamAgent("gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna")
	s := &sessionState{}
	if _, current := g.modelsFor(s); current != "gpt-5.6-terra" {
		t.Fatalf("code model = %q, want gpt-5.6-terra", current)
	}
	s.planMode.Store(true)
	if _, current := g.modelsFor(s); current != "gpt-5.6-sol" {
		t.Fatalf("plan model = %q, want gpt-5.6-sol", current)
	}
}

func TestEffortDefaultsByRole(t *testing.T) {
	a := upstreamAgent("claude-sonnet-5", "claude-opus-4-8")
	s := &sessionState{}

	if got := a.effortFor(s); got != "high" {
		t.Fatalf("code effort = %q, want high", got)
	}

	s.planMode.Store(true)
	if got := a.effortFor(s); got != "xhigh" {
		t.Fatalf("plan effort = %q, want xhigh", got)
	}

	// A coding effort choice must not leak into plan mode.
	s.effortID = "medium"
	if got := a.effortFor(s); got != "xhigh" {
		t.Fatalf("plan effort = %q, want xhigh despite code effort", got)
	}

	s.planMode.Store(false)
	if got := a.effortFor(s); got != "medium" {
		t.Fatalf("code effort = %q, want medium", got)
	}
}

func TestSetModelAndEffortScopeToCurrentMode(t *testing.T) {
	a := upstreamAgent("claude-sonnet-5", "claude-opus-4-8", "claude-fable-5")
	s := &sessionState{}
	a.sessions["sid"] = s

	ctx := context.Background()

	if err := a.SetModel(ctx, "sid", "claude-sonnet-5"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetEffort(ctx, "sid", "medium"); err != nil {
		t.Fatal(err)
	}

	s.planMode.Store(true)
	if err := a.SetModel(ctx, "sid", "claude-fable-5"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetEffort(ctx, "sid", "max"); err != nil {
		t.Fatal(err)
	}

	if _, current := a.modelsFor(s); current != "claude-fable-5" {
		t.Fatalf("plan model = %q, want claude-fable-5", current)
	}
	if got := a.effortFor(s); got != "max" {
		t.Fatalf("plan effort = %q, want max", got)
	}

	s.planMode.Store(false)
	if _, current := a.modelsFor(s); current != "claude-sonnet-5" {
		t.Fatalf("code model = %q, want claude-sonnet-5", current)
	}
	if got := a.effortFor(s); got != "medium" {
		t.Fatalf("code effort = %q, want medium", got)
	}
}

func TestModelClass(t *testing.T) {
	tests := map[string]code.ModelClass{
		"claude-opus-5":     code.ModelClassLarge,
		"claude-opus-4-8":   code.ModelClassLarge,
		"gpt-5.6-sol":       code.ModelClassLarge,
		"claude-fable-5":    code.ModelClassLarge,
		"claude-sonnet-5":   code.ModelClassMedium,
		"gpt-5.6-terra":     code.ModelClassMedium,
		"gpt-5.3-codex":     code.ModelClassMedium,
		"claude-haiku-4-5":  code.ModelClassSmall,
		"gpt-5.6-luna":      code.ModelClassSmall,
		"deepseek-v4-flash": code.ModelClassSmall,
	}
	for id, want := range tests {
		if got := code.ModelClassOf(id); got != want {
			t.Errorf("ModelClassOf(%q) = %d, want %d", id, got, want)
		}
	}

	if code.ModelFamilyOf("claude-sonnet-5") != "claude" || code.ModelFamilyOf("gpt-5.6-sol") != "gpt" {
		t.Fatal("ModelFamilyOf broken")
	}
}

func TestModelEnvOverridesByRole(t *testing.T) {
	a := upstreamAgent("claude-sonnet-5", "claude-opus-4-8", "claude-haiku-4-5", "claude-fable-5")
	a.planModelID = "claude-fable-5"
	a.utilityModelID = "claude-sonnet-5"

	s := &sessionState{}
	if _, current := a.modelsFor(s); current != "claude-sonnet-5" {
		t.Fatalf("code model = %q, want claude-sonnet-5", current)
	}

	s.planMode.Store(true)
	if _, current := a.modelsFor(s); current != "claude-fable-5" {
		t.Fatalf("plan model = %q, want claude-fable-5", current)
	}

	if got := a.utilityModel(); got != "claude-sonnet-5" {
		t.Fatalf("utility model = %q, want claude-sonnet-5", got)
	}

	s.planModelID = "claude-opus-4-8"
	if _, current := a.modelsFor(s); current != "claude-opus-4-8" {
		t.Fatalf("session plan pick overridden: %q", current)
	}
}

func TestPlanEffortOverride(t *testing.T) {
	a := upstreamAgent("claude-sonnet-5", "claude-opus-4-8")
	a.planEffortID = "max"

	s := &sessionState{}
	if got := a.effortFor(s); got != "high" {
		t.Fatalf("code effort = %q, want high", got)
	}

	s.planMode.Store(true)
	if got := a.effortFor(s); got != "max" {
		t.Fatalf("plan effort = %q, want max", got)
	}

	s.planEffortID = "low"
	if got := a.effortFor(s); got != "low" {
		t.Fatalf("session plan effort overridden: %q", got)
	}
}
