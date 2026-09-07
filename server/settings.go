package server

import (
	"encoding/json"
	"net/http"

	"github.com/adrianliechti/wingman-agent/pkg/settings"
)

func (s *Server) handleWindowTerminalSettings(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Position *settings.WindowTerminalPosition `json:"window.terminal.position"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if request.Position == nil || !request.Position.Valid() {
		http.Error(w, "window.terminal.position must be tab or bottom", http.StatusBadRequest)
		return
	}

	s.settingsMu.Lock()
	updated, err := settings.Update(func(value *settings.Settings) {
		value.WindowTerminalPosition = *request.Position
	})
	if err == nil {
		s.terminalPosition.Store(updated.WindowTerminalPosition)
	}
	s.settingsMu.Unlock()
	if err != nil {
		http.Error(w, "could not save window.terminal.position", http.StatusInternalServerError)
		return
	}

	s.broadcast(Frame{Type: EvtCapabilitiesChanged})
	writeJSON(w, map[string]settings.WindowTerminalPosition{
		"window.terminal.position": updated.WindowTerminalPosition,
	})
}
