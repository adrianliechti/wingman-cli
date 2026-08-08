package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/task"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	codeagent "github.com/adrianliechti/wingman-agent/pkg/code/agent"
)

type TaskEntry struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	AgentType   string `json:"agent_type"`
	Status      string `json:"status"`
	Activity    string `json:"activity,omitempty"`
	Elapsed     int64  `json:"elapsed_seconds"`
	Seq         int    `json:"seq"`
}

func taskEntry(t *task.Task) TaskEntry {
	return TaskEntry{
		ID:          t.ID,
		Description: t.Description,
		AgentType:   t.AgentType,
		Status:      string(t.Status()),
		Activity:    t.Activity(),
		Elapsed:     int64(t.Elapsed() / time.Second),
		Seq:         t.Seq(),
	}
}

func (s *Server) sessionTasks(sessionID string) *task.Registry {
	ca, ok := s.activeAgent().(*codeagent.Agent)
	if !ok {
		return nil
	}
	return ca.Tasks(sessionID)
}

// ensureTaskPump starts (once per registry) the goroutine that forwards
// background-agent completions of a session into its turn queue — the web
// counterpart of the TUI pump.
func (s *Server) ensureTaskPump(sessionID string) {
	reg := s.sessionTasks(sessionID)
	if reg == nil {
		return
	}

	s.taskPumpMu.Lock()
	if s.taskPumps == nil {
		s.taskPumps = map[*task.Registry]bool{}
	}
	if s.taskPumps[reg] {
		s.taskPumpMu.Unlock()
		return
	}
	s.taskPumps[reg] = true
	s.taskPumpMu.Unlock()
	serverCtx := s.ctx
	if serverCtx == nil {
		serverCtx = context.Background()
	}

	go func() {
		defer func() {
			s.taskPumpMu.Lock()
			delete(s.taskPumps, reg)
			s.taskPumpMu.Unlock()
		}()

		for {
			select {
			case <-serverCtx.Done():
				return
			case <-reg.Done():
				return
			case ev := <-reg.Events():
				batch := []task.Event{ev}
				for {
					select {
					case more := <-reg.Events():
						batch = append(batch, more)
						continue
					default:
					}
					break
				}
				// Events are exactly-once: on delivery failure (agent being
				// swapped, transient steer error) retry with backoff instead
				// of dropping the notification.
				for attempt := 0; !s.deliverTaskResults(sessionID, batch); attempt++ {
					if attempt >= 24 {
						fmt.Fprintf(os.Stderr, "giving up delivering %d background agent result(s) for session %s\n", len(batch), sessionID)
						break
					}
					select {
					case <-s.ctx.Done():
						return
					case <-reg.Done():
						return
					case <-time.After(5 * time.Second):
					}
				}
			}
		}
	}()
}

func (s *Server) deliverTaskResults(sessionID string, batch []task.Event) bool {
	s.sendSession(sessionID, Frame{Type: EvtTasksChanged})

	var blocks []string
	for _, ev := range batch {
		blocks = append(blocks, ev.Notification())
	}

	_, turns := s.activeRuntime()
	if turns == nil {
		return false
	}

	first := batch[0]
	_, err := turns.Submit(s.ctx, sessionID, code.TurnInput{
		ID:     fmt.Sprintf("task-%s-%d", first.ID, first.Seq),
		Intent: code.TurnInputSteer,
		Content: []agent.Content{{
			Text:   strings.Join(blocks, "\n\n"),
			Hidden: true,
		}},
	})
	if err != nil {
		if errors.Is(err, code.ErrDuplicateInput) {
			return true
		}
		fmt.Fprintf(os.Stderr, "deliver background agent results (%s): %v\n", sessionID, err)
		return false
	}
	return true
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	reg := s.sessionTasks(r.PathValue("id"))
	if reg == nil {
		writeJSON(w, []TaskEntry{})
		return
	}

	out := []TaskEntry{}
	for _, t := range reg.List() {
		out = append(out, taskEntry(t))
	}
	writeJSON(w, out)
}

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	reg := s.sessionTasks(r.PathValue("id"))
	if reg == nil {
		http.Error(w, "background agents unavailable", http.StatusNotFound)
		return
	}
	t := reg.Get(r.PathValue("taskID"))
	if t == nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}

	writeJSON(w, struct {
		TaskEntry
		Result     string                `json:"result,omitempty"`
		Transcript []ConversationMessage `json:"transcript"`
	}{
		TaskEntry:  taskEntry(t),
		Result:     t.Result(),
		Transcript: convertMessages(t.PeekMessages()),
	})
}

func (s *Server) handleTaskStop(w http.ResponseWriter, r *http.Request) {
	reg := s.sessionTasks(r.PathValue("id"))
	if reg == nil {
		http.Error(w, "background agents unavailable", http.StatusNotFound)
		return
	}
	if err := reg.Stop(r.PathValue("taskID")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
