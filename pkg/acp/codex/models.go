package codex

import (
	"slices"

	"github.com/coder/acp-go-sdk"
)

type modelEntry struct {
	ID           string
	Name         string
	Description  string
	EffortLevels []string
	Default      bool
}

func modelsFromCodex(list []codexModel) []modelEntry {
	out := make([]modelEntry, 0, len(list))
	for _, m := range list {
		if m.Hidden {
			continue
		}
		efforts := make([]string, 0, len(m.SupportedReasoningEfforts))
		for _, e := range m.SupportedReasoningEfforts {
			efforts = append(efforts, e.ReasoningEffort)
		}
		name := m.DisplayName
		if name == "" {
			name = m.ID
		}
		out = append(out, modelEntry{
			ID:           m.ID,
			Name:         name,
			Description:  m.Description,
			EffortLevels: efforts,
			Default:      m.IsDefault,
		})
	}
	return out
}

func findModel(models []modelEntry, id string) *modelEntry {
	for i := range models {
		if models[i].ID == id {
			return &models[i]
		}
	}
	return nil
}

func resolveModel(models []modelEntry, id string) *modelEntry {
	if id != "" && id != "default" {
		return findModel(models, id)
	}
	for i := range models {
		if models[i].Default {
			return &models[i]
		}
	}
	return nil
}

const (
	modelConfigID             = "model"
	effortConfigID            = "effort"
	collaborationModeConfigID = "collaboration_mode"

	defaultCollaborationMode = "default"
	planCollaborationMode    = "plan"
)

func buildConfigOptions(models []modelEntry, currentModelID, currentEffort, collaborationMode string) []acp.SessionConfigOption {
	opts := []acp.SessionConfigOption{modelConfigOption(models, currentModelID)}
	if effort := effortConfigOption(models, currentModelID, currentEffort); effort != nil {
		opts = append(opts, *effort)
	}
	opts = append(opts, collaborationModeConfigOption(collaborationMode))
	return opts
}

func collaborationModeConfigOption(current string) acp.SessionConfigOption {
	if current != planCollaborationMode {
		current = defaultCollaborationMode
	}
	ungrouped := acp.SessionConfigSelectOptionsUngrouped{
		{Value: defaultCollaborationMode, Name: "Default"},
		{
			Value:       planCollaborationMode,
			Name:        "Plan",
			Description: new("Plan before making changes"),
		},
	}
	opt := acp.NewSessionConfigOptionSelect(
		acp.SessionConfigValueId(current),
		acp.SessionConfigSelectOptions{Ungrouped: &ungrouped},
	)
	desc := "How Codex collaborates for subsequent turns"
	category := acp.SessionConfigOptionCategory("_codex_collaboration_mode")
	opt.Select.Id = collaborationModeConfigID
	opt.Select.Name = "Collaboration mode"
	opt.Select.Description = &desc
	opt.Select.Category = &category
	return opt
}

func isValidCollaborationMode(mode string) bool {
	return mode == defaultCollaborationMode || mode == planCollaborationMode
}

func modelConfigOption(models []modelEntry, currentID string) acp.SessionConfigOption {
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

func effortConfigOption(models []modelEntry, currentModelID, currentEffort string) *acp.SessionConfigOption {
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

func isValidEffort(m *modelEntry, level string) bool {
	if level == "" || level == "default" {
		return true
	}
	return slices.Contains(m.EffortLevels, level)
}

func normalizeSessionConfig(models []modelEntry, modelID, effort string) (string, string) {
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

type sessionMode struct {
	id             string
	name           string
	description    string
	approvalPolicy any
	sandboxPolicy  any
}

var sessionModes = []sessionMode{
	{
		id:             "agent",
		name:           "Agent",
		description:    "Read and edit files and run commands. Asks before acting outside the workspace.",
		approvalPolicy: "on-request",
		sandboxPolicy: map[string]any{
			"type":                "workspaceWrite",
			"writableRoots":       []string{},
			"networkAccess":       false,
			"excludeTmpdirEnvVar": false,
			"excludeSlashTmp":     false,
		},
	},
	{
		id:             "plan",
		name:           "Plan",
		description:    "Read-only — proposes a plan, doesn't edit code.",
		approvalPolicy: "on-request",
		sandboxPolicy:  map[string]any{"type": "readOnly", "networkAccess": false},
	},
	{
		id:             "unattended",
		name:           "Unattended",
		description:    "Runs with full access and without permission prompts.",
		approvalPolicy: "never",
		sandboxPolicy:  map[string]any{"type": "dangerFullAccess"},
	},
}

func findMode(id string) *sessionMode {
	for i := range sessionModes {
		if sessionModes[i].id == id {
			return &sessionModes[i]
		}
	}
	return nil
}

func modeFor(id string) sessionMode {
	if m := findMode(id); m != nil {
		return *m
	}
	return *findMode(defaultModeID)
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
