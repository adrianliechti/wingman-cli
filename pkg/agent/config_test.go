package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestContextWindowFor(t *testing.T) {
	unsetEnv(t, "WINGMAN_LARGE_CONTEXT")

	cases := []struct {
		model string
		want  int
	}{
		{"claude-sonnet-5", 1_000_000},
		{"claude-opus-4-8", 1_000_000},
		{"claude-fable-5", 1_000_000},
		{"claude-haiku-4-5", 200_000},
		{"gpt-5.6-sol", 272_000},
		{"gpt-5.6-terra", 272_000},
		{"gpt-5.5", 272_000},
		{"gpt-5.4", 272_000},
		{"gpt-5.4-mini", 400_000},
		{"gpt-5.4-nano", DefaultContextWindow},
		{"gpt-5.3-codex", 400_000},
		{"gpt-5.3-codex-spark", 128_000},
		{"gpt-5.2-codex", 400_000},
		{"gpt-4.1-mini", DefaultContextWindow},
		{"gemini-2.5-pro", DefaultContextWindow},
		{"glm-5.3", 1_000_000},
		{"glm-5.2", 1_000_000},
		{"kimi-k3", 1_048_576},
		{"minimax-m3", 512_000},
		{"grok-4.6", 200_000},
		{"deepseek-v4-pro", 1_000_000},
		{"mistral-medium-latest", 262_144},
		{"qwen3.7-plus", 1_000_000},
		{"GPT-5.5", 272_000},
		{"some-unknown-model", DefaultContextWindow},
		{"", DefaultContextWindow},
	}

	for _, tc := range cases {
		if got := ContextWindowFor(tc.model); got != tc.want {
			t.Errorf("ContextWindowFor(%q) = %d, want %d", tc.model, got, tc.want)
		}
	}

	t.Setenv("WINGMAN_LARGE_CONTEXT", "1")
	for modelID, want := range map[string]int{"gpt-5.6-sol": 1_050_000, "minimax-m3": 1_000_000, "grok-4.6": 500_000} {
		if got := ContextWindowFor(modelID); got != want {
			t.Errorf("ContextWindowFor(%q) with WINGMAN_LARGE_CONTEXT = %d, want %d", modelID, got, want)
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

func TestCreateClientUsesOpenAIBeforeOllama(t *testing.T) {
	expectedAuth := "Bearer openai-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("request path = %q, want /v1/models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != expectedAuth {
			t.Errorf("Authorization = %q, want %q", got, expectedAuth)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	t.Cleanup(server.Close)

	unsetEnv(t, "WINGMAN_URL")
	t.Setenv("OLLAMA_HOST", "http://127.0.0.1:1")
	t.Setenv("OLLAMA_API_KEY", "ollama-secret")
	t.Setenv("OPENROUTER_API_KEY", "openrouter-secret")
	t.Setenv("OPENAI_API_KEY", "openai-secret")
	t.Setenv("OPENAI_BASE_URL", server.URL+"/v1")

	client := createClient()
	if _, err := client.Models.List(context.Background()); err != nil {
		t.Fatalf("list OpenAI models: %v", err)
	}
}

func TestCreateClientUsesOllamaWithoutOpenAIKey(t *testing.T) {
	expectedAuth := "Bearer ollama-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("request path = %q, want /v1/models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != expectedAuth {
			t.Errorf("Authorization = %q, want %q", got, expectedAuth)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	t.Cleanup(server.Close)

	unsetEnv(t, "WINGMAN_URL")
	unsetEnv(t, "OPENAI_API_KEY")
	unsetEnv(t, "OPENROUTER_API_KEY")
	t.Setenv("OLLAMA_HOST", server.URL)
	t.Setenv("OLLAMA_API_KEY", "ollama-secret")

	client := createClient()
	if _, err := client.Models.List(context.Background()); err != nil {
		t.Fatalf("list Ollama models: %v", err)
	}

	t.Setenv("OLLAMA_API_KEY", "")
	expectedAuth = "Bearer -"
	client = createClient()
	if _, err := client.Models.List(context.Background()); err != nil {
		t.Fatalf("list Ollama models with fallback key: %v", err)
	}
}

func TestClientConfigProviderPriorityAndFallback(t *testing.T) {
	for _, name := range []string{
		"WINGMAN_URL", "WINGMAN_TOKEN", "OPENAI_API_KEY", "OPENAI_BASE_URL",
		"OPENROUTER_API_KEY", "OLLAMA_HOST", "OLLAMA_API_KEY",
	} {
		unsetEnv(t, name)
	}

	t.Setenv("OLLAMA_HOST", "ollama.test")
	t.Setenv("OLLAMA_API_KEY", "ollama-secret")
	t.Setenv("OPENROUTER_API_KEY", "openrouter-secret")
	t.Setenv("OPENAI_API_KEY", "openai-secret")
	t.Setenv("OPENAI_BASE_URL", "https://openai.test/v1")
	t.Setenv("WINGMAN_URL", "https://wingman.test/")
	t.Setenv("WINGMAN_TOKEN", "wingman-secret")

	assertClientConfig(t, "https://wingman.test/v1", "wingman-secret")
	unsetEnv(t, "WINGMAN_URL")
	assertClientConfig(t, "https://openai.test/v1", "openai-secret")
	unsetEnv(t, "OPENAI_API_KEY")
	assertClientConfig(t, "https://openrouter.ai/api/v1", "openrouter-secret")
	unsetEnv(t, "OPENROUTER_API_KEY")
	assertClientConfig(t, "http://ollama.test/v1", "ollama-secret")
	unsetEnv(t, "OLLAMA_HOST")
	unsetEnv(t, "OLLAMA_API_KEY")
	assertClientConfig(t, "http://localhost:4242/v1", "-")
}

func assertClientConfig(t *testing.T, wantBaseURL, wantToken string) {
	t.Helper()
	baseURL, token := clientConfig()
	if baseURL != wantBaseURL || token != wantToken {
		t.Fatalf("clientConfig() = (%q, %q), want (%q, %q)", baseURL, token, wantBaseURL, wantToken)
	}
}

func TestOllamaAPIKeyDefaultsToCloudHost(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "")
	t.Setenv("OLLAMA_API_KEY", "ollama-secret")

	baseURL, token, ok := ollamaConfig()
	if !ok || baseURL != "https://ollama.com/v1" || token != "ollama-secret" {
		t.Fatalf("ollamaConfig() = (%q, %q, %v)", baseURL, token, ok)
	}
}

func TestOpenRouterConfig(t *testing.T) {
	unsetEnv(t, "OPENROUTER_API_KEY")
	if _, _, ok := openRouterConfig(); ok {
		t.Fatal("openRouterConfig() succeeded without OPENROUTER_API_KEY")
	}

	t.Setenv("OPENROUTER_API_KEY", "openrouter-secret")
	baseURL, token, ok := openRouterConfig()
	if !ok || baseURL != "https://openrouter.ai/api/v1" || token != "openrouter-secret" {
		t.Fatalf("openRouterConfig() = (%q, %q, %v)", baseURL, token, ok)
	}
}

func unsetEnv(t *testing.T, name string) {
	t.Helper()

	old, existed := os.LookupEnv(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatalf("unset %s: %v", name, err)
	}

	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(name, old)
		} else {
			_ = os.Unsetenv(name)
		}
	})
}
