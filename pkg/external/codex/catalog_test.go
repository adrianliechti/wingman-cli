package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBuildModelCatalogFiltersAndSynthesizesModels(t *testing.T) {
	data, err := buildModelCatalog([]string{
		"gpt-6-astra",
		"gpt-5.6-terra",
		"gpt-5.4",
		"gpt-5.3-codex",
		"gpt-5.3-codex",
		"claude-sonnet-5",
	})
	if err != nil {
		t.Fatal(err)
	}

	var catalog struct {
		Models []struct {
			Slug              string `json:"slug"`
			DisplayName       string `json:"display_name"`
			Description       string `json:"description"`
			Visibility        string `json:"visibility"`
			Priority          int    `json:"priority"`
			ContextWindow     int    `json:"context_window"`
			DefaultEffort     string `json:"default_reasoning_level"`
			ShellType         string `json:"shell_type"`
			MultiAgentVersion string `json:"multi_agent_version"`
			ModelMessages     *struct {
				Instructions string `json:"instructions_template"`
			} `json:"model_messages"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}

	gotIDs := make([]string, 0, len(catalog.Models))
	for i, entry := range catalog.Models {
		gotIDs = append(gotIDs, entry.Slug)
		if entry.Visibility != "list" {
			t.Errorf("model %q visibility = %q, want list", entry.Slug, entry.Visibility)
		}
		if entry.Priority != i+1 {
			t.Errorf("model %q priority = %d, want %d", entry.Slug, entry.Priority, i+1)
		}
		if entry.ModelMessages == nil || entry.ModelMessages.Instructions == "" {
			t.Errorf("model %q has no embedded instructions", entry.Slug)
		}
		if entry.MultiAgentVersion != "v1" {
			t.Errorf("model %q multi-agent version = %q, want v1", entry.Slug, entry.MultiAgentVersion)
		}
	}

	if want := []string{"gpt-6-astra", "gpt-5.6-terra", "gpt-5.4", "gpt-5.3-codex"}; !slices.Equal(gotIDs, want) {
		t.Fatalf("model ids = %q, want %q", gotIDs, want)
	}

	astra := catalog.Models[0]
	if astra.DefaultEffort != "low" || astra.ShellType != "unified_exec" {
		t.Errorf("Astra catalog metadata = effort %q, shell %q", astra.DefaultEffort, astra.ShellType)
	}
	if !strings.Contains(astra.ModelMessages.Instructions, "You are Codex, an agent based on GPT-6") {
		t.Error("Astra catalog is missing its GPT-6 instructions")
	}

	synthesized := catalog.Models[3]
	if synthesized.DisplayName != "GPT 5.3 Codex" {
		t.Errorf("synthesized display name = %q", synthesized.DisplayName)
	}
	if synthesized.Description != "OpenAI model available through Wingman." {
		t.Errorf("synthesized description = %q", synthesized.Description)
	}
	if synthesized.ContextWindow != 400_000 {
		t.Errorf("synthesized context window = %d, want 400000", synthesized.ContextWindow)
	}
}

func TestPrepareModelCatalogCleansUp(t *testing.T) {
	cfg := &CodexConfig{Models: []string{"gpt-5.4"}}
	cleanup, err := PrepareModelCatalog(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := cfg.ModelCatalog

	if !filepath.IsAbs(path) {
		t.Fatalf("catalog path = %q, want absolute", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Errorf("catalog permissions = %o, want private", info.Mode().Perm())
	}

	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("catalog file still exists after cleanup: %v", err)
	}
}

func TestBuildModelCatalogRequiresOpenAIModel(t *testing.T) {
	_, err := buildModelCatalog([]string{"claude-sonnet-5"})
	if err == nil {
		t.Fatal("buildModelCatalog succeeded without an OpenAI model")
	}
}

func TestEmbeddedCatalogSupportsEveryKnownOpenAIModel(t *testing.T) {
	models := resolveModels(nil)
	data, err := buildModelCatalog(models)
	if err != nil {
		t.Fatal(err)
	}

	var catalog modelCatalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != len(models) {
		t.Fatalf("catalog contains %d models, want %d", len(catalog.Models), len(models))
	}
}
