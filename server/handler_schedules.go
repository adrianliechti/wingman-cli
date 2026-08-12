package server

import (
	"net/http"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/schedule"
	codeagent "github.com/adrianliechti/wingman-agent/pkg/code/agent"
)

type ScheduleEntry struct {
	ID       string `json:"id"`
	Prompt   string `json:"prompt"`
	Schedule string `json:"schedule"`
	Status   string `json:"status"`
	Script   bool   `json:"script,omitempty"`
	NextRun  string `json:"next_run,omitempty"`
	NextIn   string `json:"next_in,omitempty"`
	LastRun  string `json:"last_run,omitempty"`
	Failures int    `json:"failures,omitempty"`
}

func (s *Server) sessionSchedules(sessionID string) *schedule.MemoryStore {
	ca, ok := s.activeAgent().(*codeagent.Agent)
	if !ok {
		return nil
	}
	return ca.Schedules(sessionID)
}

func (s *Server) handleSchedules(w http.ResponseWriter, r *http.Request) {
	store := s.sessionSchedules(r.PathValue("id"))
	if store == nil {
		writeJSON(w, []ScheduleEntry{})
		return
	}

	tasks, err := store.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	now := time.Now()
	out := []ScheduleEntry{}

	for _, t := range tasks {
		entry := ScheduleEntry{
			ID:       t.ID,
			Prompt:   t.Prompt,
			Schedule: t.Schedule,
			Status:   t.Status,
			Script:   t.Script != "",
			Failures: t.Failures,
		}

		if next := schedule.NextAttempt(t, now); !next.IsZero() {
			entry.NextRun = next.Local().Format(time.RFC3339)
			entry.NextIn = schedule.Relative(next, now)
		}

		if t.LastRun != nil {
			entry.LastRun = t.LastRun.Local().Format(time.RFC3339)
		}

		out = append(out, entry)
	}

	writeJSON(w, out)
}

func (s *Server) handleScheduleDelete(w http.ResponseWriter, r *http.Request) {
	store := s.sessionSchedules(r.PathValue("id"))
	if store == nil {
		http.Error(w, "scheduled tasks unavailable", http.StatusNotFound)
		return
	}

	id := r.PathValue("scheduleID")

	err := store.Mutate(func(tasks []schedule.Task) ([]schedule.Task, error) {
		i, err := schedule.Find(tasks, id)
		if err != nil {
			return nil, err
		}
		return append(tasks[:i:i], tasks[i+1:]...), nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	s.sendSession(r.PathValue("id"), Frame{Type: EvtTasksChanged})
	w.WriteHeader(http.StatusNoContent)
}
