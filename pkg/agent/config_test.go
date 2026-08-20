package agent

import "testing"

func TestContextWindowFor(t *testing.T) {
	cases := []struct {
		model string
		large bool
		want  int
	}{
		{"claude-sonnet-5", false, 1_000_000},
		{"claude-opus-4-8", false, 1_000_000},
		{"claude-fable-5", false, 1_000_000},
		{"claude-haiku-4-5", false, 200_000},
		{"claude-opus-4-5", false, 200_000},
		{"claude-sonnet-4-5", false, 200_000},
		{"claude-sonnet-4-5", true, 200_000},
		{"gpt-5.6-sol", false, 272_000},
		{"gpt-5.6-sol", true, 1_050_000},
		{"gpt-5.6-terra", false, 272_000},
		{"gpt-5.6-luna", true, 1_050_000},
		{"gpt-5.5", false, 272_000},
		{"gpt-5.5", true, 1_050_000},
		{"gpt-5.4", false, 272_000},
		{"gpt-5.4", true, 1_050_000},
		{"gpt-5.4-mini", false, 400_000},
		{"gpt-5.4-nano", false, 400_000},
		{"gpt-5.3-codex", false, 400_000},
		{"gpt-5.3-codex", true, 400_000},
		{"gpt-5.3-codex-spark", false, 128_000},
		{"gpt-5.2-codex", false, 400_000},
		{"gpt-4.1-mini", false, 1_000_000},
		{"gpt-4o-mini", false, 128_000},
		{"o3-mini", false, 200_000},
		{"gemini-2.5-pro", false, 200_000},
		{"gemini-2.5-pro", true, 1_000_000},
		{"glm-5.3", false, 1_000_000},
		{"glm-5.2", false, 1_000_000},
		{"glm-5.1", false, 200_000},
		{"glm-5", false, 204_800},
		{"glm-4.7", false, 204_800},
		{"glm-4.7-flash", false, 200_000},
		{"kimi-k3", false, 1_048_576},
		{"kimi-k2.7-code", false, 262_144},
		{"kimi-k2.6", false, DefaultContextWindow},
		{"minimax-m3", false, 512_000},
		{"minimax-m3", true, 1_000_000},
		{"grok-4.6", false, 200_000},
		{"grok-4.6", true, 500_000},
		{"deepseek-v4-pro", false, 1_000_000},
		{"mistral-medium", false, 262_144},
		{"mistral-medium-latest", false, 262_144},
		{"qwen3.7-plus", false, 1_000_000},
		{"qwen3-next", false, 131_072},
		{"GPT-5.5", false, 272_000},
		{"some-unknown-model", false, DefaultContextWindow},
		{"some-unknown-model", true, DefaultContextWindow},
		{"", false, DefaultContextWindow},
	}

	for _, tc := range cases {
		if got := ContextWindowFor(tc.model, tc.large); got != tc.want {
			t.Errorf("ContextWindowFor(%q, large=%v) = %d, want %d", tc.model, tc.large, got, tc.want)
		}
	}
}

func TestUtilityModelNameUsesRoleModelThenMainFallback(t *testing.T) {
	cfg := &Config{
		Model: func() string { return "main-model" },
		RoleModel: func(role string) (ModelOption, bool) {
			if role != "utility" {
				t.Fatalf("role = %q, want utility", role)
			}
			return ModelOption{ID: "utility-model"}, true
		},
	}
	if got := cfg.utilityModelName(); got != "utility-model" {
		t.Fatalf("utility model = %q", got)
	}

	cfg.RoleModel = func(string) (ModelOption, bool) {
		return ModelOption{}, false
	}
	if got := cfg.utilityModelName(); got != "main-model" {
		t.Fatalf("utility fallback = %q", got)
	}

	derived := cfg.Derive()
	if derived.RoleModel == nil || derived.utilityModelName() != "main-model" {
		t.Fatal("derived config lost its role resolver or main fallback")
	}
}
