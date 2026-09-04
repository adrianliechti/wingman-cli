package agent

import (
	"context"
	"slices"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/model"
)

func upstreamAgent(ids ...string) *Agent {
	models := make(map[string]bool, len(ids))
	for _, id := range ids {
		models[id] = true
	}
	return &Agent{
		upstreamModels: models,
		modelByRole:    map[modelRole]string{},
		effortByRole:   map[modelRole]string{modelRoleUtility: ""},
		sessions:       map[string]*sessionState{},
	}
}

func roleModelID(t *testing.T, a *Agent, s *sessionState, role string) string {
	t.Helper()
	option, ok := a.roleModel(s, role)
	if !ok {
		t.Fatalf("role %q did not resolve", role)
	}
	return option.ID
}

func TestModelSelectionByRole(t *testing.T) {
	a := upstreamAgent("claude-sonnet-5", "claude-opus-4-8", "claude-haiku-4-5", "claude-fable-5")
	s := &sessionState{}

	if current := roleModelID(t, a, s, ""); current != "claude-sonnet-5" {
		t.Fatalf("code model = %q, want claude-sonnet-5", current)
	}

	s.setMode(modePlan)
	if current := roleModelID(t, a, s, ""); current != "claude-opus-4-8" {
		t.Fatalf("plan model = %q, want claude-opus-4-8", current)
	}

	if got := roleModelID(t, a, nil, "utility"); got != "claude-haiku-4-5" {
		t.Fatalf("utility model = %q, want claude-haiku-4-5", got)
	}
}

func TestModelSelectionRoleScoped(t *testing.T) {
	a := upstreamAgent("claude-sonnet-5", "claude-opus-4-8", "claude-fable-5")
	a.modelByRole[modelRoleMain] = "claude-fable-5"
	s := &sessionState{}

	if current := roleModelID(t, a, s, ""); current != "claude-fable-5" {
		t.Fatalf("explicit code model overridden: %q", current)
	}

	// The coding choice must not leak into plan mode: plan picks large.
	s.setMode(modePlan)
	if current := roleModelID(t, a, s, ""); current != "claude-opus-4-8" {
		t.Fatalf("plan model = %q, want claude-opus-4-8", current)
	}
}

func TestModelSelectionCrossFamilyFallback(t *testing.T) {
	// Claude family with no small model: utility falls back to another family.
	a := upstreamAgent("claude-sonnet-5", "gpt-5.6-luna")
	if got := roleModelID(t, a, nil, "utility"); got != "gpt-5.6-luna" {
		t.Fatalf("utility model = %q, want gpt-5.6-luna", got)
	}

	// GPT-only gateway anchors every role in the gpt family.
	g := upstreamAgent("gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna")
	s := &sessionState{}
	if current := roleModelID(t, g, s, ""); current != "gpt-5.6-terra" {
		t.Fatalf("code model = %q, want gpt-5.6-terra", current)
	}
	s.setMode(modePlan)
	if current := roleModelID(t, g, s, ""); current != "gpt-5.6-sol" {
		t.Fatalf("plan model = %q, want gpt-5.6-sol", current)
	}
}

func TestRoleModelPreservesUtilityDiscoveryAndAvailabilityRules(t *testing.T) {
	a := &Agent{
		modelByRole:  map[modelRole]string{},
		effortByRole: map[modelRole]string{modelRoleUtility: ""},
		sessions:     map[string]*sessionState{},
	}
	if _, ok := a.RoleModel("utility"); ok {
		t.Fatal("utility role guessed a model before discovery")
	}
	if got := roleModelID(t, a, nil, ""); got != "claude-sonnet-5" {
		t.Fatalf("main model before discovery = %q", got)
	}

	a.modelByRole[modelRoleUtility] = "configured-utility"
	if got := roleModelID(t, a, nil, "utility"); got != "configured-utility" {
		t.Fatalf("configured utility model = %q", got)
	}
	a.modelByRole[modelRoleMain] = "qwen3.8:27b-mlx"
	if got := roleModelID(t, a, nil, ""); got != "qwen3.8:27b-mlx" {
		t.Fatalf("configured main model before discovery = %q", got)
	}

	a.upstreamModels = map[string]bool{"gpt-5.6-luna": true}
	a.modelByRole[modelRoleMain] = "missing-main"
	if got := roleModelID(t, a, nil, ""); got != "gpt-5.6-luna" {
		t.Fatalf("main availability fallback = %q", got)
	}
	if got := roleModelID(t, a, nil, "utility"); got != "configured-utility" {
		t.Fatalf("utility availability unexpectedly replaced %q", got)
	}
}

func TestEffortDefaultsByRole(t *testing.T) {
	a := upstreamAgent("claude-sonnet-5", "claude-opus-4-8")
	s := &sessionState{}

	if got := a.effortFor(s); got != "high" {
		t.Fatalf("code effort = %q, want high", got)
	}

	s.setMode(modePlan)
	if got := a.effortFor(s); got != "xhigh" {
		t.Fatalf("plan effort = %q, want xhigh", got)
	}

	// A coding effort choice must not leak into plan mode.
	s.effortByRole = map[modelRole]string{modelRoleMain: "medium"}
	if got := a.effortFor(s); got != "xhigh" {
		t.Fatalf("plan effort = %q, want xhigh despite code effort", got)
	}

	s.setMode(modeAgent)
	if got := a.effortFor(s); got != "medium" {
		t.Fatalf("code effort = %q, want medium", got)
	}
}

func TestAstraEffortDefaultsAndClampsOverrides(t *testing.T) {
	a := upstreamAgent("gpt-6-astra")
	s := &sessionState{}

	if got := a.effortFor(s); got != "low" {
		t.Fatalf("Astra effort = %q, want low", got)
	}

	a.effortByRole[modelRoleMain] = "none"
	if got := a.effortFor(s); got != "low" {
		t.Fatalf("Astra effort for unsupported none override = %q, want low", got)
	}
	if current, values := a.Effort(""); current != "low" || !slices.Equal(values, []string{"auto", "low", "medium", "high", "xhigh", "max"}) {
		t.Fatalf("Astra effort selector = %q/%v", current, values)
	}

	if err := a.SetEffort(context.Background(), "", "none"); err == nil {
		t.Fatal("SetEffort accepted unsupported Astra effort none")
	}
}

func TestQwen38EffortDefaultsAndClampsOverrides(t *testing.T) {
	a := upstreamAgent("qwen3.8:27b-mlx")
	s := &sessionState{}

	if got := a.effortFor(s); got != "medium" {
		t.Fatalf("Qwen 3.8 default effort = %q, want medium", got)
	}

	a.effortByRole[modelRoleMain] = "max"
	if got := a.effortFor(s); got != "xhigh" {
		t.Fatalf("Qwen 3.8 max effort = %q, want xhigh", got)
	}
	if current, values := a.Effort(""); current != "xhigh" || !slices.Equal(values, []string{"auto", "none", "low", "medium", "xhigh"}) {
		t.Fatalf("Qwen 3.8 effort selector = %q/%v", current, values)
	}

	if err := a.SetEffort(context.Background(), "", "high"); err == nil {
		t.Fatal("SetEffort accepted unsupported Qwen 3.8 effort high")
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

	s.setMode(modePlan)
	if err := a.SetModel(ctx, "sid", "claude-fable-5"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetEffort(ctx, "sid", "max"); err != nil {
		t.Fatal(err)
	}

	if current := roleModelID(t, a, s, ""); current != "claude-fable-5" {
		t.Fatalf("plan model = %q, want claude-fable-5", current)
	}
	if got := a.effortFor(s); got != "max" {
		t.Fatalf("plan effort = %q, want max", got)
	}

	s.setMode(modeAgent)
	if current := roleModelID(t, a, s, ""); current != "claude-sonnet-5" {
		t.Fatalf("code model = %q, want claude-sonnet-5", current)
	}
	if got := a.effortFor(s); got != "medium" {
		t.Fatalf("code effort = %q, want medium", got)
	}
}

func TestSetModelResetsEffort(t *testing.T) {
	a := upstreamAgent("claude-sonnet-5", "gpt-5.6-terra", "claude-opus-4-8")
	s := &sessionState{}
	a.sessions["sid"] = s
	ctx := context.Background()

	if err := a.SetEffort(ctx, "sid", "max"); err != nil {
		t.Fatal(err)
	}
	if got, _ := a.Effort("sid"); got != "max" {
		t.Fatalf("effort = %q, want max", got)
	}

	if err := a.SetModel(ctx, "sid", "gpt-5.6-terra"); err != nil {
		t.Fatal(err)
	}
	if got, _ := a.Effort("sid"); got != "auto" {
		t.Fatalf("effort after model switch = %q, want auto (model default)", got)
	}

	// Plan mode keeps its own effort: switching the plan model resets only the
	// plan effort, back to the large-model plan default.
	s.setMode(modePlan)
	if err := a.SetEffort(ctx, "sid", "low"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetModel(ctx, "sid", "claude-opus-4-8"); err != nil {
		t.Fatal(err)
	}
	if got, _ := a.Effort("sid"); got != "auto" {
		t.Fatalf("plan effort after switch = %q, want auto", got)
	}
	if got := a.effortFor(s); got != "xhigh" {
		t.Fatalf("plan effort default = %q, want xhigh", got)
	}
}

func TestModelClass(t *testing.T) {
	tests := map[string]model.Class{
		"gpt-6-astra":       model.ClassLarge,
		"claude-opus-5":     model.ClassLarge,
		"claude-opus-4-8":   model.ClassLarge,
		"gpt-5.6-sol":       model.ClassLarge,
		"claude-fable-5":    model.ClassLarge,
		"claude-sonnet-5":   model.ClassMedium,
		"gpt-5.6-terra":     model.ClassMedium,
		"gpt-5.3-codex":     model.ClassMedium,
		"claude-haiku-4-5":  model.ClassSmall,
		"gpt-5.6-luna":      model.ClassSmall,
		"deepseek-v4-flash": model.ClassSmall,
	}
	for id, want := range tests {
		if got := model.ClassOf(id); got != want {
			t.Errorf("ModelClassOf(%q) = %d, want %d", id, got, want)
		}
	}

	if model.Family("claude-sonnet-5") != "claude" || model.Family("gpt-5.6-sol") != "gpt" {
		t.Fatal("ModelFamilyOf broken")
	}
}

func TestModelEnvOverridesByRole(t *testing.T) {
	a := upstreamAgent("claude-sonnet-5", "claude-opus-4-8", "claude-haiku-4-5", "claude-fable-5")
	a.modelByRole[modelRolePlan] = "claude-fable-5"
	a.modelByRole[modelRoleUtility] = "claude-sonnet-5"

	s := &sessionState{}
	if current := roleModelID(t, a, s, ""); current != "claude-sonnet-5" {
		t.Fatalf("code model = %q, want claude-sonnet-5", current)
	}

	s.setMode(modePlan)
	if current := roleModelID(t, a, s, ""); current != "claude-fable-5" {
		t.Fatalf("plan model = %q, want claude-fable-5", current)
	}

	if got := roleModelID(t, a, nil, "utility"); got != "claude-sonnet-5" {
		t.Fatalf("utility model = %q, want claude-sonnet-5", got)
	}

	s.modelByRole = map[modelRole]string{modelRolePlan: "claude-opus-4-8"}
	if current := roleModelID(t, a, s, ""); current != "claude-opus-4-8" {
		t.Fatalf("session plan pick overridden: %q", current)
	}
}

func TestPlanEffortOverride(t *testing.T) {
	a := upstreamAgent("claude-sonnet-5", "claude-opus-4-8")
	a.effortByRole[modelRolePlan] = "max"

	s := &sessionState{}
	if got := a.effortFor(s); got != "high" {
		t.Fatalf("code effort = %q, want high", got)
	}

	s.setMode(modePlan)
	if got := a.effortFor(s); got != "max" {
		t.Fatalf("plan effort = %q, want max", got)
	}

	s.effortByRole = map[modelRole]string{modelRolePlan: "low"}
	if got := a.effortFor(s); got != "low" {
		t.Fatalf("session plan effort overridden: %q", got)
	}
}
