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
		{"gpt-5.6-sol", false, 272_000, gptEfforts},
		{"gpt-5.6-sol", true, 1_000_000, gptEfforts},
		{"gpt-5-6-preview", false, 272_000, gptEfforts},
		{"GPT-5.6-Sol", false, 272_000, gptEfforts},
		{"gpt-5.3-codex", false, 400_000, gptEfforts},
		{"gpt-6-experimental", false, 0, gptEfforts},
		{"gemini-2.5-pro", false, 200_000, nil},
		{"gemini-2.5-pro", true, 1_000_000, nil},
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
