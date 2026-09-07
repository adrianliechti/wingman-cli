package server

import (
	"github.com/adrianliechti/wingman-agent/pkg/code"
)

type modeOption struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type modeState struct {
	Current string       `json:"current"`
	Modes   []modeOption `json:"modes"`
}

func toModeState(available []code.Mode, current string) modeState {
	modes := make([]modeOption, 0, len(available))
	for _, m := range available {
		modes = append(modes, modeOption{ID: m.ID, Name: m.Name, Description: m.Description})
	}
	return modeState{Current: current, Modes: modes}
}
