package acp

import (
	"slices"

	acpsdk "github.com/coder/acp-go-sdk"
)

const EffortConfigID = "effort"

// ModeInfo is the client-facing half of a session mode. Each bridge keeps its
// own mode table because the backend half (permission mode, approval and
// sandbox policy) differs, and so do the descriptions that report it.
type ModeInfo struct {
	ID          string
	Name        string
	Description string
}

func SessionModeState(modes []ModeInfo, currentID, defaultID string) *acpsdk.SessionModeState {
	if currentID == "" {
		currentID = defaultID
	}
	available := make([]acpsdk.SessionMode, 0, len(modes))
	for _, m := range modes {
		desc := m.Description
		available = append(available, acpsdk.SessionMode{
			Id:          acpsdk.SessionModeId(m.ID),
			Name:        m.Name,
			Description: &desc,
		})
	}
	return &acpsdk.SessionModeState{
		AvailableModes: available,
		CurrentModeId:  acpsdk.SessionModeId(currentID),
	}
}

func IsValidEffort(levels []string, level string) bool {
	if level == "" || level == "default" {
		return true
	}
	return slices.Contains(levels, level)
}

// EffortConfigOption renders the reasoning-effort selector for a model, or nil
// when the model exposes no effort levels.
func EffortConfigOption(levels []string, currentEffort string) *acpsdk.SessionConfigOption {
	if len(levels) == 0 {
		return nil
	}

	ungrouped := acpsdk.SessionConfigSelectOptionsUngrouped{
		{Value: "default", Name: "Default"},
	}
	for _, lvl := range levels {
		ungrouped = append(ungrouped, acpsdk.SessionConfigSelectOption{
			Value: acpsdk.SessionConfigValueId(lvl),
			Name:  TitleCase(lvl),
		})
	}

	current := currentEffort
	if !IsValidEffort(levels, current) || current == "" {
		current = "default"
	}
	opt := acpsdk.NewSessionConfigOptionSelect(
		acpsdk.SessionConfigValueId(current),
		acpsdk.SessionConfigSelectOptions{Ungrouped: &ungrouped},
	)
	desc := "Reasoning effort for the selected model"
	cat := acpsdk.SessionConfigOptionCategoryThoughtLevel
	opt.Select.Id = EffortConfigID
	opt.Select.Name = "Effort"
	opt.Select.Description = &desc
	opt.Select.Category = &cat
	return &opt
}

func TitleCase(s string) string {
	if s == "" {
		return s
	}
	out := []byte(s)
	if out[0] >= 'a' && out[0] <= 'z' {
		out[0] -= 0x20
	}
	return string(out)
}
