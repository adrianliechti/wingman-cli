package codex

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/model"
)

// models.json is the unmodified codex-rs/models-manager/models.json asset.
// Keeping the full upstream entries preserves Codex's model-specific
// instructions and tool metadata. Run `task generate:codex-models` to refresh
// it; Wingman-specific fields are changed only in the temporary runtime copy.
//
//go:embed models.json
var embeddedModelCatalog []byte

type modelCatalog struct {
	Models []map[string]any `json:"models"`
}

// PrepareModelCatalog writes the Wingman-filtered catalog to a temporary file.
// The caller must keep the file alive while Codex runs and call cleanup after it exits.
func PrepareModelCatalog(cfg *CodexConfig) (cleanup func(), err error) {
	data, err := buildModelCatalog(cfg.Models)
	if err != nil {
		return nil, err
	}

	file, err := os.CreateTemp("", "wingman-codex-models-*.json")
	if err != nil {
		return nil, err
	}
	path := file.Name()
	cleanup = func() { _ = os.Remove(path) }

	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		cleanup()
		return nil, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return nil, err
	}

	cfg.ModelCatalog = path
	return cleanup, nil
}

func buildModelCatalog(modelIDs []string) ([]byte, error) {
	var embedded modelCatalog
	if err := json.Unmarshal(embeddedModelCatalog, &embedded); err != nil {
		return nil, fmt.Errorf("parse embedded Codex model catalog: %w", err)
	}

	templates := make(map[string]map[string]any, len(embedded.Models))
	for _, entry := range embedded.Models {
		slug, ok := entry["slug"].(string)
		if !ok || slug == "" {
			return nil, fmt.Errorf("embedded Codex model is missing a slug")
		}
		templates[slug] = entry
	}

	selected := make([]map[string]any, 0, len(modelIDs))
	seen := make(map[string]bool, len(modelIDs))

	for _, id := range modelIDs {
		if id == "" || seen[id] || !strings.HasPrefix(id, "gpt-") {
			continue
		}
		seen[id] = true

		templateID := modelTemplateID(id, templates)
		template, ok := templates[templateID]
		if !ok {
			return nil, fmt.Errorf("embedded Codex model catalog is missing template %q", templateID)
		}

		entry := maps.Clone(template)
		entry["slug"] = id
		entry["display_name"] = model.Name(id)
		entry["visibility"] = "list"
		entry["priority"] = len(selected) + 1
		entry["availability_nux"] = nil
		entry["upgrade"] = nil
		entry["multi_agent_version"] = "v1"

		if id != templateID && !(id == "gpt-5.6" && templateID == "gpt-5.6-sol") {
			entry["description"] = "OpenAI model available through Wingman."
			if m, ok := model.Find(id); ok {
				entry["context_window"] = model.ProfileFor(id).ContextWindow(false)
				entry["max_context_window"] = m.ContextTokens()
			}
		}

		selected = append(selected, entry)
	}

	if len(selected) == 0 {
		return nil, fmt.Errorf("no OpenAI models are available for the Codex launcher")
	}

	return json.MarshalIndent(modelCatalog{Models: selected}, "", "  ")
}

func modelTemplateID(id string, templates map[string]map[string]any) string {
	if _, ok := templates[id]; ok {
		return id
	}

	if id == "gpt-5.6" {
		return "gpt-5.6-sol"
	}

	if strings.Contains(id, "mini") || strings.Contains(id, "spark") {
		return "gpt-5.4-mini"
	}

	return "gpt-5.2"
}
