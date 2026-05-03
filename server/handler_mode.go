package server

import (
	"encoding/json"
	"net/http"

	"github.com/adrianliechti/wingman-agent/pkg/code"
)

// currentInstructions returns the active system prompt, switching between the
// default agent prompt and the planning-only prompt based on the current mode.
// This is wired into agent.Config.Instructions so the agent invokes it lazily
// on every Send — toggling mode takes effect on the next turn.
func (s *Server) currentInstructions() string {
	data := s.agent.InstructionsData()
	s.mu.Lock()
	data.PlanMode = s.planMode
	s.mu.Unlock()
	return code.BuildInstructions(data)
}

func (s *Server) modeString() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.planMode {
		return "plan"
	}
	return "agent"
}

func (s *Server) setMode(mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.planMode = mode == "plan"
	// Keep the underlying agent's PlanMode in sync so prompt construction and
	// mode-aware tool filtering read the same value.
	s.agent.PlanMode = s.planMode
}

func (s *Server) handleMode(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"mode": s.modeString()})
}

func (s *Server) handleSetMode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if body.Mode != "agent" && body.Mode != "plan" {
		http.Error(w, "mode must be \"agent\" or \"plan\"", http.StatusBadRequest)
		return
	}

	s.setMode(body.Mode)

	writeJSON(w, map[string]string{"mode": s.modeString()})
}
