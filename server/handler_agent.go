package server

import (
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/adrianliechti/wingman-agent/pkg/code/agents"
)

type AgentEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type agentState struct {
	Agent     string `json:"agent"`
	CanDelete bool   `json:"can_delete"`
}

func (s *Server) handleAgents(w http.ResponseWriter, _ *http.Request) {
	available, err := agents.Available()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	result := []AgentEntry{
		{ID: code.BuiltinAgentName, Name: "Wingman"},
	}
	for _, r := range available {
		result = append(result, AgentEntry{ID: r.ID, Name: r.Name})
	}
	writeJSON(w, result)
}

func (s *Server) handleAgent(w http.ResponseWriter, _ *http.Request) {
	a := s.activeAgent()
	name := code.BuiltinAgentName
	canDelete := false
	if a != nil {
		name = a.Name()
		canDelete = supportsDelete(a)
	}
	writeJSON(w, agentState{Agent: name, CanDelete: canDelete})
}

func supportsDelete(a code.Agent) bool {
	if d, ok := a.(interface{ SupportsDelete() bool }); ok {
		return d.SupportsDelete()
	}
	return true
}

func (s *Server) handleSetAgent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Agent string `json:"agent"`
	}
	if err := decodeJSONRequest(w, r, &body, 1<<10); err != nil {
		return
	}

	name := strings.TrimSpace(body.Agent)
	if name == "" {
		name = code.BuiltinAgentName
	}

	current := ""
	if a := s.activeAgent(); a != nil {
		current = a.Name()
	}
	if current == name {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	next, err := s.constructBackend(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.swapAgent(next)

	s.broadcast(Frame{Type: EvtAgentChanged})
	w.WriteHeader(http.StatusNoContent)
}
