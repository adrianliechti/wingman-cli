package model

import (
	"slices"
	"testing"
)

func TestProfileFor(t *testing.T) {
	cases := []struct {
		id      string
		large   bool
		window  int
		efforts []string
	}{
		{"claude-sonnet-5", false, 1_000_000, nil},
		{"claude-haiku-4-5", false, 200_000, nil},
		{"claude-opus-4-5", true, 200_000, nil},
		{"gpt-5.6-sol", false, 272_000, gpt56Efforts},
		{"gpt-5.6-sol", true, 1_050_000, gpt56Efforts},
		{"gpt-5-6-preview", false, 272_000, gpt56Efforts},
		{"GPT-5.6-Sol", false, 272_000, gpt56Efforts},
		{"gpt-5.4-mini", false, 400_000, gptEfforts},
		{"gpt-5.4-nano", false, 400_000, gptEfforts},
		{"gpt-5.3-codex", false, 400_000, gptEfforts},
		{"gpt-6-experimental", false, 0, gptEfforts},
		{"gptish-5.6", false, 0, nil},
		{"gemini-2.5-pro", false, 200_000, nil},
		{"gemini-2.5-pro", true, 1_000_000, nil},
		{"glm-5.3", false, 1_000_000, nil},
		{"glm-5.2", false, 1_000_000, nil},
		{"glm-5.1", false, 200_000, nil},
		{"glm-5", false, 204_800, nil},
		{"glm-4.7", false, 204_800, nil},
		{"glm-4.7-flash", false, 200_000, nil},
		{"kimi-k3", false, 1_048_576, kimiK3Efforts},
		{"kimi-k2.7-code", false, 262_144, nil},
		{"kimi-k2.6", false, 0, nil},
		{"minimax-m3", false, 512_000, nil},
		{"MiniMax-M3", true, 1_000_000, nil},
		{"grok-4.6", false, 200_000, nil},
		{"grok-4.6", true, 500_000, nil},
		{"some-unknown-model", false, 0, nil},
		{"", false, 0, nil},
	}

	for _, tc := range cases {
		p := ProfileFor(tc.id)
		if got := p.ContextWindow(tc.large); got != tc.window {
			t.Errorf("ProfileFor(%q).ContextWindow(large=%v) = %d, want %d", tc.id, tc.large, got, tc.window)
		}
		if !slices.Equal(p.Efforts, tc.efforts) {
			t.Errorf("ProfileFor(%q).Efforts = %v, want %v", tc.id, p.Efforts, tc.efforts)
		}
	}
}

func TestKimiK3ReasoningProfile(t *testing.T) {
	p := ProfileFor("kimi-k3")
	if p.DefaultEffort != "max" {
		t.Fatalf("Kimi K3 default effort = %q, want max", p.DefaultEffort)
	}
	if p.ReasoningEffortPlacement != ReasoningEffortAtRoot {
		t.Fatalf("Kimi K3 reasoning effort placement = %v, want request root", p.ReasoningEffortPlacement)
	}
}
