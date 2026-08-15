package model

import (
	"slices"
	"testing"
)

func TestCurrentProviderModels(t *testing.T) {
	cases := []struct {
		id      string
		name    string
		class   Class
		context int
		output  int
	}{
		{"kimi-k3", "Kimi K3", ClassLarge, 1_048_576, 131_072},
		{"kimi-k2.7-code", "Kimi K2.7 Code", ClassMedium, 262_144, 262_144},
		{"glm-5.3", "GLM 5.3", ClassMedium, 1_000_000, 131_072},
		{"minimax-m3", "MiniMax M3", ClassLarge, 1_000_000, 128_000},
		{"MiniMax-M3", "MiniMax M3", ClassLarge, 1_000_000, 128_000},
		{"grok-4.6", "Grok 4.6", ClassLarge, 500_000, 500_000},
		{"gpt-5.4-nano", "GPT 5.4 Nano", ClassSmall, 400_000, 128_000},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			got, ok := Find(tc.id)
			if !ok {
				t.Fatalf("Find(%q) failed", tc.id)
			}
			if got.ID != tc.id || got.Name != tc.name || got.Class != tc.class || got.ContextTokens() != tc.context || got.OutputTokens() != tc.output {
				t.Fatalf("Find(%q) = %+v, want id=%q name=%q class=%v context=%d output=%d", tc.id, got, tc.id, tc.name, tc.class, tc.context, tc.output)
			}
		})
	}
}

func TestCurrentProviderModelAvailability(t *testing.T) {
	available := Available(map[string]bool{
		"gpt-5.4-nano": true,
		"kimi-k3":      true,
		"glm-5.3":      true,
		"MiniMax-M3":   true,
		"grok-4.6":     true,
	})

	ids := make([]string, 0, len(available))
	for _, m := range available {
		ids = append(ids, m.ID)
	}

	if want := []string{"gpt-5.4-nano", "glm-5.3", "kimi-k3", "MiniMax-M3", "grok-4.6"}; !slices.Equal(ids, want) {
		t.Fatalf("Available() ids = %v, want %v", ids, want)
	}
}

func TestCurrentProviderModelClassification(t *testing.T) {
	cases := []struct {
		id     string
		family string
		class  Class
	}{
		{"kimi-k3", "kimi", ClassLarge},
		{"kimi-k2.7-code", "kimi", ClassMedium},
		{"glm-5.3", "glm", ClassMedium},
		{"MiniMax-M3", "minimax", ClassLarge},
		{"grok-4.6", "grok", ClassLarge},
	}

	for _, tc := range cases {
		if got := Family(tc.id); got != tc.family {
			t.Errorf("Family(%q) = %q, want %q", tc.id, got, tc.family)
		}
		if got := ClassOf(tc.id); got != tc.class {
			t.Errorf("ClassOf(%q) = %v, want %v", tc.id, got, tc.class)
		}
	}
}

func TestRemovedKimiModelsAreNotInCatalog(t *testing.T) {
	for _, id := range []string{"kimi-k2.6", "kimi-k2.7-code-highspeed"} {
		if _, ok := Find(id); ok {
			t.Errorf("removed Kimi model %q is still in the catalog", id)
		}
	}
}
