package pi

import (
	"encoding/json"
	"strings"

	"github.com/coder/acp-go-sdk"
)

const (
	modelConfigID  = "model"
	effortConfigID = "effort"
)

var fallbackThinkingLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

const defaultThinkingLevel = "medium"

type modelEntry struct {
	ID   string
	Name string
}

func parseAvailableModels(data json.RawMessage) []modelEntry {
	var d struct {
		Models []struct {
			Provider string `json:"provider"`
			ID       string `json:"id"`
			Name     string `json:"name"`
		} `json:"models"`
	}
	if json.Unmarshal(data, &d) != nil {
		return nil
	}

	out := make([]modelEntry, 0, len(d.Models))
	for _, m := range d.Models {
		provider := strings.TrimSpace(m.Provider)
		id := strings.TrimSpace(m.ID)
		if provider == "" || id == "" {
			continue
		}
		name := m.Name
		if name == "" {
			name = id
		}
		out = append(out, modelEntry{
			ID:   provider + "/" + id,
			Name: provider + "/" + name,
		})
	}
	return out
}

type piState struct {
	SessionID     string `json:"sessionId"`
	ThinkingLevel string `json:"thinkingLevel"`
	Model         struct {
		Provider string `json:"provider"`
		ID       string `json:"id"`
	} `json:"model"`
}

func parseState(data json.RawMessage) piState {
	var s piState
	_ = json.Unmarshal(data, &s)
	return s
}

func parseAvailableThinkingLevels(data json.RawMessage) []string {
	var d struct {
		Levels []string `json:"levels"`
	}
	if json.Unmarshal(data, &d) != nil {
		return nil
	}

	out := make([]string, 0, len(d.Levels))
	seen := make(map[string]bool, len(d.Levels))
	for _, level := range d.Levels {
		level = strings.TrimSpace(level)
		if level == "" || seen[level] {
			continue
		}
		seen[level] = true
		out = append(out, level)
	}
	return out
}

func (s piState) currentModel() string {
	if s.Model.Provider == "" || s.Model.ID == "" {
		return ""
	}
	return s.Model.Provider + "/" + s.Model.ID
}

func (s piState) thinking() string {
	if s.ThinkingLevel != "" {
		return s.ThinkingLevel
	}
	return defaultThinkingLevel
}

func isThinkingLevel(levels []string, v string) bool {
	for _, l := range levels {
		if l == v {
			return true
		}
	}
	return false
}

func findModel(models []modelEntry, id string) *modelEntry {
	for i := range models {
		if models[i].ID == id {
			return &models[i]
		}
	}
	return nil
}

func buildConfigOptions(models []modelEntry, currentModel string, thinkingLevels []string, thinking string) []acp.SessionConfigOption {
	opts := make([]acp.SessionConfigOption, 0, 2)
	if len(models) > 0 {
		opts = append(opts, modelConfigOption(models, currentModel))
	}
	if len(thinkingLevels) > 0 {
		opts = append(opts, effortConfigOption(thinkingLevels, thinking))
	}
	return opts
}

func modelConfigOption(models []modelEntry, currentID string) acp.SessionConfigOption {
	ungrouped := make(acp.SessionConfigSelectOptionsUngrouped, 0, len(models))
	for _, m := range models {
		ungrouped = append(ungrouped, acp.SessionConfigSelectOption{
			Value: acp.SessionConfigValueId(m.ID),
			Name:  m.Name,
		})
	}
	if findModel(models, currentID) == nil && len(models) > 0 {
		currentID = models[0].ID
	}
	opt := acp.NewSessionConfigOptionSelect(
		acp.SessionConfigValueId(currentID),
		acp.SessionConfigSelectOptions{Ungrouped: &ungrouped},
	)
	opt.Select.Id = modelConfigID
	opt.Select.Name = "Model"
	return opt
}

func effortConfigOption(levels []string, thinking string) acp.SessionConfigOption {
	if !isThinkingLevel(levels, thinking) {
		thinking = levels[0]
	}
	ungrouped := make(acp.SessionConfigSelectOptionsUngrouped, 0, len(levels))
	for _, l := range levels {
		ungrouped = append(ungrouped, acp.SessionConfigSelectOption{
			Value: acp.SessionConfigValueId(l),
			Name:  titleCase(l),
		})
	}
	opt := acp.NewSessionConfigOptionSelect(
		acp.SessionConfigValueId(thinking),
		acp.SessionConfigSelectOptions{Ungrouped: &ungrouped},
	)
	desc := "Reasoning effort for this session"
	cat := acp.SessionConfigOptionCategoryThoughtLevel
	opt.Select.Id = effortConfigID
	opt.Select.Name = "Thinking"
	opt.Select.Description = &desc
	opt.Select.Category = &cat
	return opt
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	if b[0] >= 'a' && b[0] <= 'z' {
		b[0] -= 0x20
	}
	return string(b)
}
