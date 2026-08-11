package subagent

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func TestVerificationToolFilterRejectsUnknownAndMutatingTools(t *testing.T) {
	tests := []struct {
		name string
		tool tool.Tool
		want bool
	}{
		{
			name: "read only tool",
			tool: tool.Tool{Name: "read", Effect: tool.StaticEffect(tool.EffectReadOnly)},
			want: true,
		},
		{
			name: "shell allowed for tests",
			tool: tool.Tool{Name: "shell", Effect: func(map[string]any) tool.Effect { return tool.EffectDynamic }},
			want: true,
		},
		{
			name: "unknown effect rejected",
			tool: tool.Tool{Name: "mcp_unknown"},
			want: false,
		},
		{
			name: "mutating tool rejected",
			tool: tool.Tool{Name: "schedule_task", Effect: tool.StaticEffect(tool.EffectMutates)},
			want: false,
		},
		{
			name: "write rejected by name",
			tool: tool.Tool{Name: "write", Effect: tool.StaticEffect(tool.EffectMutates)},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := allowVerificationTool(tt.tool); got != tt.want {
				t.Fatalf("allowVerificationTool() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAllowReadOnlyTool(t *testing.T) {
	tests := []struct {
		name string
		tool tool.Tool
		want bool
	}{
		{"read-only allowed", tool.Tool{Name: "read", Effect: tool.StaticEffect(tool.EffectReadOnly)}, true},
		{"dynamic allowed (will be wrapped)", tool.Tool{Name: "shell", Effect: tool.StaticEffect(tool.EffectDynamic)}, true},
		{"mutating rejected", tool.Tool{Name: "write", Effect: tool.StaticEffect(tool.EffectMutates)}, false},
		{"elicit rejected by name", tool.Tool{Name: "elicit", Effect: tool.StaticEffect(tool.EffectReadOnly)}, false},
		{"agent rejected by name", tool.Tool{Name: "agent", Effect: tool.StaticEffect(tool.EffectReadOnly)}, false},
		{"hidden rejected", tool.Tool{Name: "x", Hidden: true, Effect: tool.StaticEffect(tool.EffectReadOnly)}, false},
		{"missing effect rejected", tool.Tool{Name: "x"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := allowReadOnlyTool(tt.tool); got != tt.want {
				t.Fatalf("allowReadOnlyTool() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAllowNonAgentTool(t *testing.T) {
	if allowNonAgentTool(tool.Tool{Name: "agent"}) {
		t.Error("agent must be rejected")
	}
	if allowNonAgentTool(tool.Tool{Name: "x", Hidden: true}) {
		t.Error("hidden tools must be rejected")
	}
	if !allowNonAgentTool(tool.Tool{Name: "read"}) {
		t.Error("ordinary tool must pass")
	}
}

func TestToolsForTypeFiltersExplore(t *testing.T) {
	all := []tool.Tool{
		{Name: "read", Effect: tool.StaticEffect(tool.EffectReadOnly)},
		{Name: "write", Effect: tool.StaticEffect(tool.EffectMutates)},
		{Name: "shell", Effect: tool.StaticEffect(tool.EffectDynamic), Execute: func(context.Context, map[string]any) (tool.Result, error) { return tool.Text("ok"), nil }},
		{Name: "elicit", Hidden: true, Effect: tool.StaticEffect(tool.EffectReadOnly)},
	}

	filtered := toolsForType(all, subagentTypes["explore"])

	names := toolNames(filtered)
	if !containsName(names, "read") || !containsName(names, "shell") {
		t.Fatalf("explore must keep read + shell, got %v", names)
	}
	if containsName(names, "write") {
		t.Errorf("explore must reject mutating tools, got %v", names)
	}
	if containsName(names, "elicit") {
		t.Errorf("explore must reject elicit, got %v", names)
	}
}

func TestExploreWrapsDynamicToolsAsReadOnly(t *testing.T) {
	called := false
	dynamic := tool.Tool{
		Name: "shell",
		Effect: func(args map[string]any) tool.Effect {

			if args == nil {
				return tool.EffectDynamic
			}
			if v, _ := args["safe"].(bool); v {
				return tool.EffectReadOnly
			}
			return tool.EffectMutates
		},
		Execute: func(context.Context, map[string]any) (tool.Result, error) {
			called = true
			return tool.Text("ran"), nil
		},
	}

	filtered := toolsForType([]tool.Tool{dynamic}, subagentTypes["explore"])
	if len(filtered) != 1 {
		t.Fatalf("len(filtered) = %d, want 1", len(filtered))
	}
	wrapped := filtered[0]

	out, err := wrapped.Execute(context.Background(), map[string]any{"safe": true})
	if err != nil || out.Content != "ran" {
		t.Fatalf("read-only call: got (%q, %v), want (ran, nil)", out.Content, err)
	}
	if !called {
		t.Error("original executor must have run on read-only path")
	}

	called = false
	_, err = wrapped.Execute(context.Background(), map[string]any{"safe": false})
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("mutating call: want read-only refusal, got %v", err)
	}
	if called {
		t.Error("original executor must NOT have run on mutating path")
	}
}

func TestToolsForTypeGeneralPurposeKeepsMutating(t *testing.T) {
	all := []tool.Tool{
		{Name: "write", Effect: tool.StaticEffect(tool.EffectMutates)},
		{Name: "agent", Effect: tool.StaticEffect(tool.EffectMutates)},
	}
	filtered := toolsForType(all, subagentTypes["general-purpose"])

	names := toolNames(filtered)
	if !containsName(names, "write") {
		t.Errorf("general-purpose must keep write, got %v", names)
	}
	if containsName(names, "agent") {
		t.Errorf("general-purpose must reject recursive agent tool, got %v", names)
	}
}

func TestSpecializedReadOnlyAgentsFilterLikeExplore(t *testing.T) {
	all := []tool.Tool{
		{Name: "read", Effect: tool.StaticEffect(tool.EffectReadOnly)},
		{Name: "write", Effect: tool.StaticEffect(tool.EffectMutates)},
		{Name: "shell", Effect: tool.StaticEffect(tool.EffectDynamic), Execute: func(context.Context, map[string]any) (tool.Result, error) { return tool.Text("ok"), nil }},
	}

	for _, name := range []string{"code-architect", "code-reviewer"} {
		t.Run(name, func(t *testing.T) {
			filtered := toolsForType(all, subagentTypes[name])
			names := toolNames(filtered)
			if !containsName(names, "read") || !containsName(names, "shell") {
				t.Fatalf("%s must keep read + shell, got %v", name, names)
			}
			if containsName(names, "write") {
				t.Fatalf("%s must reject write, got %v", name, names)
			}
		})
	}
}

func toolNames(tools []tool.Tool) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	return out
}

func containsName(names []string, want string) bool {
	return slices.Contains(names, want)
}

func TestReportCollectorValidatesSchema(t *testing.T) {
	collector, err := newReportCollector(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"count": map[string]any{"type": "integer"},
		},
		"required":             []any{"count"},
		"additionalProperties": false,
	})
	if err != nil {
		t.Fatalf("newReportCollector: %v", err)
	}

	report := collector.tool()

	if _, err := report.Execute(context.Background(), map[string]any{"result": map[string]any{"wrong": true}}); err == nil {
		t.Fatal("expected validation error for non-matching result")
	}
	if collector.take() != "" {
		t.Fatalf("payload recorded after failed validation: %q", collector.take())
	}

	if _, err := report.Execute(context.Background(), map[string]any{"result": map[string]any{"count": float64(3)}}); err != nil {
		t.Fatalf("valid result rejected: %v", err)
	}
	if got := collector.take(); got != `{"count":3}` {
		t.Fatalf("payload = %q", got)
	}
}

func TestReportCollectorRejectsInvalidSchema(t *testing.T) {
	if _, err := newReportCollector(map[string]any{"type": 42}); err == nil {
		t.Fatal("expected error for malformed schema")
	}
}

func TestRunTrailer(t *testing.T) {
	messages := []agent.Message{
		{Role: agent.RoleAssistant, Content: []agent.Content{{ToolCall: &agent.ToolCall{ID: "1", Name: "read"}}}},
		{Role: agent.RoleAssistant, Content: []agent.Content{{ToolResult: &agent.ToolResult{ID: "1"}}}},
	}

	got := runTrailer(messages, agent.Usage{InputTokens: 45_200, OutputTokens: 900}, 100*time.Second)

	want := "\n\n(agent: 1 tool call · 45.2k in / 900 out tokens · 1m40s)"
	if got != want {
		t.Fatalf("runTrailer = %q, want %q", got, want)
	}
}

func TestApplyModelOverrides(t *testing.T) {
	roles := map[string]agent.ModelOption{
		"plan":    {ID: "large-model"},
		"utility": {ID: "gpt-small", MaxEffort: "xhigh"},
		"":        {ID: "gpt-session", MaxEffort: "xhigh"},
	}
	cfg := &agent.Config{
		Model:  func() string { return "session-model" },
		Effort: func() string { return "medium" },
		SubagentModel: func(role string) (agent.ModelOption, bool) {
			opt, ok := roles[role]
			return opt, ok
		},
	}

	if err := applyModelOverrides(cfg, map[string]any{}, ""); err != nil {
		t.Fatal(err)
	}
	if cfg.Model() != "session-model" || cfg.Effort() != "medium" {
		t.Fatal("empty args must inherit session model and effort")
	}

	if err := applyModelOverrides(cfg, map[string]any{"model": "utility", "effort": "LOW"}, ""); err != nil {
		t.Fatal(err)
	}
	if cfg.Model() != "gpt-small" {
		t.Fatalf("model = %q", cfg.Model())
	}
	if cfg.Effort() != "low" {
		t.Fatalf("effort = %q", cfg.Effort())
	}

	if err := applyModelOverrides(cfg, map[string]any{"model": "utility", "effort": "max"}, ""); err != nil {
		t.Fatal(err)
	}
	if cfg.Effort() != "xhigh" {
		t.Fatalf("effort = %q, want max clamped to the model ceiling", cfg.Effort())
	}

	if err := applyModelOverrides(cfg, map[string]any{"model": "plan", "effort": "max"}, ""); err != nil {
		t.Fatal(err)
	}
	if cfg.Model() != "large-model" {
		t.Fatalf("model = %q", cfg.Model())
	}
	if cfg.Effort() != "max" {
		t.Fatalf("effort = %q, want unclamped on unbounded model", cfg.Effort())
	}

	if err := applyModelOverrides(cfg, map[string]any{"model": "claude-haiku-4-5"}, ""); err == nil {
		t.Fatal("concrete model ids must be rejected")
	}
	if err := applyModelOverrides(cfg, map[string]any{"effort": "extreme"}, ""); err == nil {
		t.Fatal("unknown effort must error")
	}

	if err := applyModelOverrides(cfg, map[string]any{"effort": "max"}, ""); err != nil {
		t.Fatal(err)
	}
	if cfg.Effort() != "xhigh" {
		t.Fatalf("effort = %q, want clamp against the inherited model", cfg.Effort())
	}

	cfg.Effort = func() string { return "max" }
	if err := applyModelOverrides(cfg, map[string]any{"model": "utility"}, ""); err != nil {
		t.Fatal(err)
	}
	if cfg.Effort() != "xhigh" {
		t.Fatalf("effort = %q, want inherited effort clamped to the overridden model", cfg.Effort())
	}

	cfg.Model = func() string { return "session-model" }
	cfg.SubagentModel = nil
	if err := applyModelOverrides(cfg, map[string]any{"model": "plan", "effort": "max"}, ""); err != nil {
		t.Fatal(err)
	}
	if cfg.Model() != "session-model" {
		t.Fatal("unresolvable role must keep the inherited model")
	}
	if cfg.Effort() != "max" {
		t.Fatal("nil resolver must leave effort unclamped")
	}
}
