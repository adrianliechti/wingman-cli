package model

import (
	"slices"
	"testing"
)

func TestModelRuntimeFields(t *testing.T) {
	t.Setenv("WINGMAN_CONTEXT_WINDOW_MODE", "")
	t.Setenv("WINGMAN_LARGE_CONTEXT", "")

	cases := []struct {
		id      string
		window  int
		efforts []string
	}{
		{"claude-sonnet-5", 1_000_000, nil},
		{"claude-fable-5", 1_000_000, claudeAlwaysThinkingEfforts},
		{"claude-fable-5-1", 1_000_000, claudeAlwaysThinkingEfforts},
		{"claude-mythos-5", 1_000_000, claudeAlwaysThinkingEfforts},
		{"claude-mythos-5-1", 1_000_000, claudeAlwaysThinkingEfforts},
		{"claude-haiku-4-5", 200_000, nil},
		{"gpt-6-astra", 272_000, gpt6AstraEfforts},
		{"gpt-5.6-sol", 272_000, gpt56Efforts},
		{"gpt-5-6-preview", 0, nil},
		{"GPT-5.6-Sol", 272_000, gpt56Efforts},
		{"gpt-5.4-mini", 400_000, gptEfforts},
		{"gpt-5.4-nano", 0, nil},
		{"gpt-5.3-codex", 400_000, gptEfforts},
		{"gpt-5.3-codex-spark", 128_000, gptEfforts},
		{"gpt-5.2", 400_000, gptEfforts},
		{"gpt-5.1-codex", 400_000, gptEfforts},
		{"gpt-5", 400_000, gptEfforts},
		{"gpt-6-experimental", 0, nil},
		{"glm-5.3", 1_000_000, nil},
		{"glm-5.2", 1_000_000, nil},
		{"kimi-k3", 1_048_576, nil},
		{"minimax-m3", 512_000, nil},
		{"grok-4.6", 200_000, nil},
		{"qwen3.8", 262_144, qwen38Efforts},
		{"some-unknown-model", 0, nil},
		{"", 0, nil},
	}

	for _, tc := range cases {
		m, _ := Find(tc.id)
		got := m.ContextTokens()
		if got != tc.window {
			t.Errorf("Find(%q).ContextTokens() = %d, want %d", tc.id, got, tc.window)
		}
		if !slices.Equal(m.Efforts, tc.efforts) {
			t.Errorf("Find(%q).Efforts = %v, want %v", tc.id, m.Efforts, tc.efforts)
		}
	}

	t.Setenv("WINGMAN_CONTEXT_WINDOW_MODE", "full")
	for id, want := range map[string]int{"gpt-6-astra": 1_050_000, "gpt-5.6-sol": 1_050_000, "MiniMax-M3": 1_000_000, "grok-4.6": 500_000} {
		m, _ := Find(id)
		if got := m.ContextTokens(); got != want {
			t.Errorf("Find(%q).ContextTokens() with full context mode = %d, want %d", id, got, want)
		}
	}
}

func TestLegacyLargeContextEnvironmentAlias(t *testing.T) {
	t.Setenv("WINGMAN_CONTEXT_WINDOW_MODE", "")
	t.Setenv("WINGMAN_LARGE_CONTEXT", "true")

	m, _ := Find("gpt-5.6-sol")
	if got := m.ContextTokens(); got != 1_050_000 {
		t.Fatalf("legacy WINGMAN_LARGE_CONTEXT window = %d, want 1050000", got)
	}
}
