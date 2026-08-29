package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/session"
)

const registryVersion = 1

var errRegistryClosed = errors.New("task registry is closed")

type taskSnapshot struct {
	ID          string          `json:"id"`
	AgentID     string          `json:"agent_id,omitempty"`
	Description string          `json:"description"`
	AgentType   string          `json:"agent_type"`
	Started     time.Time       `json:"started"`
	Finished    time.Time       `json:"finished,omitempty"`
	Status      Status          `json:"status"`
	Result      string          `json:"result,omitempty"`
	Activity    string          `json:"activity,omitempty"`
	Seq         int             `json:"seq"`
	AgentState  *agent.State    `json:"agent_state,omitempty"`
	ResumeData  json.RawMessage `json:"resume_data,omitempty"`
}

type registryFile struct {
	Version int            `json:"version"`
	Tasks   []taskSnapshot `json:"tasks,omitempty"`
}

// NewFileRegistry restores a session registry. Runs that were active when the
// process stopped become failed terminal snapshots and emit one notification;
// they are never replayed automatically.
func NewFileRegistry(path string) (*Registry, error) {
	r := newRegistry(path)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return r, nil
		}
		r.Close()
		return nil, err
	}
	var file registryFile
	if err := json.Unmarshal(data, &file); err != nil {
		r.Close()
		return nil, fmt.Errorf("parse task registry: %w", err)
	}
	if file.Version != registryVersion {
		r.Close()
		return nil, fmt.Errorf("unsupported task registry version %d", file.Version)
	}

	seen := map[string]bool{}
	var interrupted []Event
	for _, snapshot := range file.Tasks {
		if snapshot.ID == "" || seen[snapshot.ID] {
			r.Close()
			return nil, fmt.Errorf("task registry contains an empty or duplicate id %q", snapshot.ID)
		}
		seen[snapshot.ID] = true
		state := agent.State{}
		if snapshot.AgentState != nil {
			state = cloneAgentState(*snapshot.AgentState)
		}
		if loaded, loadErr := r.loadAgentState(snapshot.ID); loadErr == nil {
			state = loaded
		} else if !errors.Is(loadErr, os.ErrNotExist) {
			r.Close()
			return nil, loadErr
		}
		t := &Task{
			ID: snapshot.ID, AgentID: snapshot.AgentID,
			Description: snapshot.Description, AgentType: snapshot.AgentType,
			Started: snapshot.Started, finished: snapshot.Finished,
			status: snapshot.Status, result: snapshot.Result, activity: snapshot.Activity,
			seq: snapshot.Seq, registry: r,
			agentState: state,
			resumeData: append(json.RawMessage(nil), snapshot.ResumeData...),
		}
		if t.seq < 1 {
			t.seq = 1
		}
		if t.status == StatusRunning {
			t.status = StatusFailed
			t.finished = time.Now().UTC()
			t.activity = ""
			t.result = "error: process restarted before the background agent recorded a terminal result"
			interrupted = append(interrupted, Event{
				Task: t, ID: t.ID, Description: t.Description, AgentType: t.AgentType,
				Seq: t.seq, Status: t.status, Result: t.result, Elapsed: t.finished.Sub(t.Started),
			})
		}
		r.tasks = append(r.tasks, t)
		r.launched += t.seq
	}
	if err := r.persist(); err != nil {
		r.Close()
		return nil, err
	}
	for _, event := range interrupted {
		r.send(event)
	}
	return r, nil
}

func (r *Registry) persist() error {
	if r == nil || r.path == "" {
		return nil
	}
	r.persistGate.RLock()
	defer r.persistGate.RUnlock()
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return errRegistryClosed
	}
	r.persistMu.Lock()
	defer r.persistMu.Unlock()

	r.mu.Lock()
	tasks := append([]*Task(nil), r.tasks...)
	r.mu.Unlock()
	file := registryFile{Version: registryVersion, Tasks: make([]taskSnapshot, 0, len(tasks))}
	for _, task := range tasks {
		file.Tasks = append(file.Tasks, task.snapshot())
	}
	data, err := json.Marshal(file)
	if err != nil {
		return err
	}
	dir := filepath.Dir(r.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(r.path)+".tmp-")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), r.path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

func (t *Task) snapshot() taskSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	return taskSnapshot{
		ID: t.ID, AgentID: t.AgentID, Description: t.Description, AgentType: t.AgentType,
		Started: t.Started, Finished: t.finished, Status: t.status, Result: t.result,
		Activity: t.activity, Seq: t.seq,
		ResumeData: append(json.RawMessage(nil), t.resumeData...),
	}
}

func (r *Registry) agentLedgerDir() string {
	return filepath.Join(filepath.Dir(r.path), "agents")
}

func (r *Registry) childJournal(id string) (*session.Journal, error) {
	if r.path == "" {
		return nil, nil
	}
	if existing, ok := r.agentJournals.Load(id); ok {
		return existing.(*session.Journal), nil
	}
	journal, err := session.OpenJournal(r.agentLedgerDir(), id)
	if err != nil {
		return nil, err
	}
	actual, _ := r.agentJournals.LoadOrStore(id, journal)
	return actual.(*session.Journal), nil
}

func (r *Registry) appendAgentEvents(id string, events []agent.RuntimeEvent) error {
	r.persistGate.RLock()
	defer r.persistGate.RUnlock()
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return errRegistryClosed
	}
	journal, err := r.childJournal(id)
	if err != nil || journal == nil {
		return err
	}
	return journal.AppendEvents(events)
}

func (r *Registry) saveAgentState(id string, state agent.State) error {
	if r.path == "" {
		return nil
	}
	r.persistGate.RLock()
	defer r.persistGate.RUnlock()
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return errRegistryClosed
	}
	return session.Save(r.agentLedgerDir(), id, state)
}

func (r *Registry) loadAgentState(id string) (agent.State, error) {
	if r.path == "" {
		return agent.State{}, os.ErrNotExist
	}
	loaded, err := session.Load(r.agentLedgerDir(), id)
	if err != nil {
		return agent.State{}, err
	}
	return loaded.State, nil
}

func cloneAgentState(state agent.State) agent.State {
	data, err := json.Marshal(state)
	if err != nil {
		return state
	}
	var cloned agent.State
	if err := json.Unmarshal(data, &cloned); err != nil {
		return state
	}
	cloned.Revision = state.Revision
	return cloned
}
