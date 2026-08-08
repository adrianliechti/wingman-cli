package claude

import (
	"slices"
	"strings"

	"github.com/coder/acp-go-sdk"
)

type ModelEntry struct {
	ID            string
	Name          string
	Description   string
	ResolvedModel string
	EffortLevels  []string
}

func findModel(models []ModelEntry, id string) *ModelEntry {
	for i := range models {
		if models[i].ID == id {
			return &models[i]
		}
	}
	return nil
}

func resolveModel(models []ModelEntry, id string) *ModelEntry {
	if m := findModel(models, id); m != nil {
		return m
	}
	want := strings.ToLower(strings.TrimSpace(id))
	if want == "" {
		return nil
	}
	for i := range models {
		if strings.EqualFold(models[i].Name, id) {
			return &models[i]
		}
	}
	canonical := canonicalModelID(id)
	for i := range models {
		if canonicalModelID(models[i].ID) == canonical {
			return &models[i]
		}
	}
	for i := range models {
		if models[i].ID != "default" && models[i].ResolvedModel != "" && canonicalModelID(models[i].ResolvedModel) == canonical {
			return &models[i]
		}
	}
	for i := range models {
		if models[i].ResolvedModel != "" && canonicalModelID(models[i].ResolvedModel) == canonical {
			return &models[i]
		}
	}
	for i := range models {
		lid, lname := strings.ToLower(models[i].ID), strings.ToLower(models[i].Name)
		if modelContextHint(models[i].ID) == modelContextHint(id) &&
			(strings.Contains(lid, want) || strings.Contains(lname, want) || strings.Contains(want, lid)) {
			return &models[i]
		}
	}
	return nil
}

func resolveResumedModel(models []ModelEntry, live string) *ModelEntry {
	canonical := canonicalModelID(live)
	for i := range models {
		if models[i].ID == "default" && models[i].ResolvedModel != "" && canonicalModelID(models[i].ResolvedModel) == canonical {
			return &models[i]
		}
	}
	return resolveModel(models, live)
}

func canonicalModelID(id string) string {
	s := strings.ToLower(strings.TrimSpace(id))
	if base, ok := strings.CutSuffix(s, "-1m"); ok {
		s = base + "[1m]"
	}
	return s
}

func modelContextHint(id string) string {
	s := canonicalModelID(id)
	if i := strings.LastIndex(s, "["); i >= 0 && strings.HasSuffix(s, "]") {
		return s[i:]
	}
	return ""
}

const (
	modelConfigID  = "model"
	effortConfigID = "effort"
)

func buildConfigOptions(models []ModelEntry, currentModelID, currentEffort string) []acp.SessionConfigOption {
	opts := []acp.SessionConfigOption{modelConfigOption(models, currentModelID)}
	if effort := effortConfigOption(models, currentModelID, currentEffort); effort != nil {
		opts = append(opts, *effort)
	}
	return opts
}

func modelConfigOption(models []ModelEntry, currentID string) acp.SessionConfigOption {
	ungrouped := make(acp.SessionConfigSelectOptionsUngrouped, 0, len(models))
	for _, m := range models {
		desc := m.Description
		opt := acp.SessionConfigSelectOption{
			Value: acp.SessionConfigValueId(m.ID),
			Name:  m.Name,
		}
		if desc != "" {
			opt.Description = &desc
		}
		ungrouped = append(ungrouped, opt)
	}
	if currentID == "" && len(models) > 0 {
		currentID = models[0].ID
	}
	if currentID != "" && findModel(models, currentID) == nil {
		ungrouped = append(acp.SessionConfigSelectOptionsUngrouped{
			{Value: acp.SessionConfigValueId(currentID), Name: currentID},
		}, ungrouped...)
	}
	opt := acp.NewSessionConfigOptionSelect(
		acp.SessionConfigValueId(currentID),
		acp.SessionConfigSelectOptions{Ungrouped: &ungrouped},
	)
	opt.Select.Id = modelConfigID
	opt.Select.Name = "Model"
	return opt
}

func effortConfigOption(models []ModelEntry, currentModelID, currentEffort string) *acp.SessionConfigOption {
	m := findModel(models, currentModelID)
	if m == nil || len(m.EffortLevels) == 0 {
		return nil
	}

	ungrouped := acp.SessionConfigSelectOptionsUngrouped{
		{Value: "default", Name: "Default"},
	}
	for _, lvl := range m.EffortLevels {
		ungrouped = append(ungrouped, acp.SessionConfigSelectOption{
			Value: acp.SessionConfigValueId(lvl),
			Name:  titleCase(lvl),
		})
	}

	current := currentEffort
	if current == "" || !isValidEffort(m, current) {
		current = "default"
	}
	opt := acp.NewSessionConfigOptionSelect(
		acp.SessionConfigValueId(current),
		acp.SessionConfigSelectOptions{Ungrouped: &ungrouped},
	)
	desc := "Reasoning effort for the selected model"
	cat := acp.SessionConfigOptionCategoryThoughtLevel
	opt.Select.Id = effortConfigID
	opt.Select.Name = "Effort"
	opt.Select.Description = &desc
	opt.Select.Category = &cat
	return &opt
}

func isValidEffort(m *ModelEntry, level string) bool {
	if level == "" || level == "default" {
		return true
	}
	return slices.Contains(m.EffortLevels, level)
}

func normalizeSessionConfig(models []ModelEntry, modelID, effort string) (string, string) {
	if m := resolveModel(models, modelID); m != nil {
		modelID = m.ID
		if !isValidEffort(m, effort) {
			effort = "default"
		}
	}
	return modelID, effort
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	out := []byte(s)
	if out[0] >= 'a' && out[0] <= 'z' {
		out[0] -= 0x20
	}
	return string(out)
}

const defaultModeID = "agent"

const exitPlanPermissionMode = "auto"

type sessionMode struct {
	id             string
	name           string
	description    string
	permissionMode string
}

var sessionModes = []sessionMode{
	{id: "agent", name: "Agent", description: "Runs routine actions automatically with background safety checks.", permissionMode: "auto"},
	{id: "plan", name: "Plan", description: "Read-only — proposes a plan, doesn't edit code.", permissionMode: "plan"},
	{id: "unattended", name: "Unattended", description: "Runs with full access and without permission prompts.", permissionMode: "bypassPermissions"},
}

func findMode(id string) *sessionMode {
	for i := range sessionModes {
		if sessionModes[i].id == id {
			return &sessionModes[i]
		}
	}
	return nil
}

func buildSessionModeState(currentID string) *acp.SessionModeState {
	if currentID == "" {
		currentID = defaultModeID
	}
	available := make([]acp.SessionMode, 0, len(sessionModes))
	for _, m := range sessionModes {
		desc := m.description
		available = append(available, acp.SessionMode{
			Id:          acp.SessionModeId(m.id),
			Name:        m.name,
			Description: &desc,
		})
	}
	return &acp.SessionModeState{
		AvailableModes: available,
		CurrentModeId:  acp.SessionModeId(currentID),
	}
}
