package model

import (
	"slices"
	"testing"
)

func TestCurrentProviderModels(t *testing.T) {
	t.Setenv("WINGMAN_LARGE_CONTEXT", "1")

	cases := []struct {
		id      string
		name    string
		class   Class
		context int
		output  int
	}{
		{"kimi-k3", "Kimi K3", ClassLarge, 1_048_576, 131_072},
		{"glm-5.3", "GLM 5.3", ClassMedium, 1_000_000, 131_072},
		{"gemini-3.7-flash", "Gemini 3.7 Flash", ClassMedium, 1_114_112, 65_536},
		{"minimax-m3", "MiniMax M3", ClassLarge, 1_000_000, 128_000},
		{"MiniMax-M3", "MiniMax M3", ClassLarge, 1_000_000, 128_000},
		{"grok-4.6", "Grok 4.6", ClassLarge, 500_000, 500_000},
		{"claude-fable-5-1", "Claude Fable 5.1", ClassLarge, 1_000_000, 128_000},
		{"claude-mythos-5-1", "Claude Mythos 5.1", ClassLarge, 1_000_000, 128_000},
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

	if want := []string{"glm-5.3", "kimi-k3", "MiniMax-M3", "grok-4.6"}; !slices.Equal(ids, want) {
		t.Fatalf("Available() ids = %v, want %v", ids, want)
	}
}

func TestClaude51ModelAvailability(t *testing.T) {
	available := Available(map[string]bool{
		"claude-fable-5-1":  true,
		"claude-mythos-5-1": true,
	})

	ids := make([]string, 0, len(available))
	for _, m := range available {
		ids = append(ids, m.ID)
	}

	if want := []string{"claude-fable-5-1", "claude-mythos-5-1"}; !slices.Equal(ids, want) {
		t.Fatalf("Available() ids = %v, want %v", ids, want)
	}
}

func TestAlwaysThinkingClaudeEfforts(t *testing.T) {
	want := []string{"low", "medium", "high", "xhigh", "max"}
	for _, id := range []string{"claude-fable-5", "claude-fable-5-1", "claude-mythos-5", "claude-mythos-5-1"} {
		t.Run(id, func(t *testing.T) {
			got, ok := Find(id)
			if !ok {
				t.Fatalf("Find(%q) failed", id)
			}
			if !slices.Equal(got.Efforts, want) {
				t.Fatalf("Find(%q).Efforts = %v, want %v", id, got.Efforts, want)
			}
		})
	}
}

func TestDeepSeekFlash0731IsPreferred(t *testing.T) {
	available := Available(map[string]bool{
		"deepseek/deepseek-v4-flash":      true,
		"deepseek/deepseek-v4-flash-0731": true,
	})

	if len(available) != 2 {
		t.Fatalf("Available() = %+v, want both DeepSeek Flash models", available)
	}
	if available[0].ID != "deepseek/deepseek-v4-flash-0731" || available[1].ID != "deepseek/deepseek-v4-flash" {
		t.Fatalf("Available() ids = [%q, %q], want 0731 before normal Flash", available[0].ID, available[1].ID)
	}
}

func TestOllamaModelMappingUsesLongestPrefix(t *testing.T) {
	t.Setenv("WINGMAN_LARGE_CONTEXT", "1")

	cases := []struct {
		id      string
		name    string
		context int
	}{
		{"qwen3.7:27b-mlx", "Qwen 3.7 Plus", 1_000_000},
		{"qwen3.8:27b-mlx", "Qwen 3.8", 262_144},
		{"qwen3.8-27b:latest", "Qwen 3.8", 262_144},
		{"qwen3.8-max:latest", "Qwen 3.8 Max", 1_000_000},
		{"qwen/qwen3.8-27b", "Qwen 3.8", 262_144},
		{"qwen/qwen3.8-max", "Qwen 3.8 Max", 1_000_000},
		{"qwen/qwen3.8-2.4t-a95b", "Qwen 3.8 Max", 1_000_000},
		{"openai/gpt-5.6-sol", "GPT 5.6 Sol", 1_050_000},
		{"GPT-5.6-SOL:27B-MLX", "GPT 5.6 Sol", 1_050_000},
	}

	for _, tc := range cases {
		got, ok := Find(tc.id)
		if !ok {
			t.Fatalf("Find(%q) failed", tc.id)
		}
		if got.ID != tc.id || got.Name != tc.name || got.ContextTokens() != tc.context {
			t.Fatalf("Find(%q) = %+v, want id=%q name=%q context=%d", tc.id, got, tc.id, tc.name, tc.context)
		}
	}

	available := Available(map[string]bool{"qwen3.7:27b-mlx": true})
	if len(available) != 1 || available[0].ID != "qwen3.7:27b-mlx" || available[0].Name != "Qwen 3.7 Plus" {
		t.Fatalf("Available() = %+v, want the Ollama model mapped to Qwen 3.7 Plus", available)
	}

	available = Available(map[string]bool{"qwen/qwen3.8-27b": true})
	if len(available) != 1 || available[0].ID != "qwen/qwen3.8-27b" || available[0].Name != "Qwen 3.8" {
		t.Fatalf("Available() = %+v, want the OpenRouter model mapped to Qwen 3.8", available)
	}
}

func TestProviderPrefixedModelMapping(t *testing.T) {
	for _, m := range Models {
		if m.Namespace == "" {
			t.Errorf("model %q has no provider namespace", m.ID)
		}
	}

	cases := map[string]string{
		"anthropic/claude-sonnet-5":   "Claude Sonnet 5",
		"anthropic/claude-fable-5-1":  "Claude Fable 5.1",
		"anthropic/claude-mythos-5-1": "Claude Mythos 5.1",
		"openai/gpt-5.6-sol":          "GPT 5.6 Sol",
		"google/gemini-3.1-pro":       "Gemini 3.1 Pro",
		"z-ai/glm-5.3":                "GLM 5.3",
		"deepseek/deepseek-v4-pro":    "DeepSeek V4 Pro",
		"mistralai/mistral-medium":    "Mistral Medium 3.5",
		"moonshotai/kimi-k3":          "Kimi K3",
		"minimax/minimax-m3":          "MiniMax M3",
		"x-ai/grok-4.6":               "Grok 4.6",
		"qwen/qwen3.8-27b":            "Qwen 3.8",
	}

	for id, name := range cases {
		got, ok := Find(id)
		if !ok || got.ID != id || got.Name != name {
			t.Errorf("Find(%q) = %+v, %v; want name %q", id, got, ok, name)
		}
	}

	for _, id := range []string{"anthropic/gpt-5.6-sol", "openai/claude-sonnet-5", "unknown/qwen3.8-27b"} {
		if got, ok := Find(id); ok {
			t.Errorf("Find(%q) = %+v; want no cross-provider match", id, got)
		}
	}
}

func TestCurrentProviderModelClassification(t *testing.T) {
	cases := []struct {
		id     string
		family string
		class  Class
	}{
		{"kimi-k3", "kimi", ClassLarge},
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

func TestRemovedModelsAreNotInCatalog(t *testing.T) {
	for _, id := range []string{
		"gpt-5.4-nano",
		"glm-5",
		"glm-5.1",
		"glm-4.7",
		"glm-4.7-flash",
		"kimi-k2.6",
		"kimi-k2.7-code",
		"kimi-k2.7-code-highspeed",
		"qwen3.6",
		"qwen3.6-plus",
		"qwen3.6-flash",
		"qwen3",
		"qwen3-next",
		"qwen3-coder",
		"qwen3-coder-next",
		"qwen3-coder-plus",
		"qwen3-coder-flash",
	} {
		if _, ok := Find(id); ok {
			t.Errorf("removed model %q is still in the catalog", id)
		}
	}
}

func TestKimiK3HasNoCustomEffortProtocol(t *testing.T) {
	m, ok := Find("kimi-k3")
	if !ok {
		t.Fatal("Kimi K3 missing from catalog")
	}
	if m.Effort != "" || len(m.Efforts) != 0 {
		t.Fatalf("Kimi K3 effort configuration = %q/%v, want none", m.Effort, m.Efforts)
	}
}
