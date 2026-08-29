package task

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

type Status string

const (
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
	StatusStopped Status = "stopped"
)

const (
	// MaxConcurrent guards provider rate limits, not local resources — the
	// runs are IO-bound model streams, so CPU count is irrelevant.
	MaxConcurrent = 16
	MaxPerSession = 50

	// MaxRunDuration bounds one background run so a hung model stream cannot
	// occupy a concurrency slot forever.
	MaxRunDuration = 30 * time.Minute
)

type Task struct {
	ID          string
	AgentID     string
	Description string
	AgentType   string
	Started     time.Time

	mu       sync.Mutex
	status   Status
	result   string
	finished time.Time
	stopped  bool
	cancel   context.CancelFunc
	activity string
	seq      int
	peek     func() []agent.Message
	resume   func(ctx context.Context, prompt string) error
	registry *Registry

	agentState agent.State
	resumeData json.RawMessage
}

// Seq counts the task's runs: 1 for the initial launch, +1 per resume. It
// distinguishes the completion notifications of successive runs.
func (t *Task) Seq() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.seq
}

// SetResume installs the follow-up hook used by task_send: it restarts the
// finished agent with a new prompt and its full prior context.
func (t *Task) SetResume(fn func(prompt string) error) {
	if fn == nil {
		t.SetResumeContext(nil)
		return
	}
	t.SetResumeContext(func(_ context.Context, prompt string) error { return fn(prompt) })
}

func (t *Task) SetResumeContext(fn func(context.Context, string) error) {
	t.mu.Lock()
	t.resume = fn
	t.mu.Unlock()
}

// SetDurableAgent binds the stable child identity, its current event ledger,
// and opaque resume specification. The subagent package owns the spec format;
// Registry only guarantees atomic persistence.
func (t *Task) SetDurableAgent(agentID string, state agent.State, resumeData json.RawMessage) error {
	t.mu.Lock()
	t.AgentID = agentID
	t.agentState = cloneAgentState(state)
	t.resumeData = append(json.RawMessage(nil), resumeData...)
	registry := t.registry
	t.mu.Unlock()
	if registry != nil {
		if err := registry.saveAgentState(t.ID, state); err != nil {
			return err
		}
		return registry.persist()
	}
	return nil
}

func (t *Task) DurableAgent() (string, agent.State, json.RawMessage) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.AgentID, cloneAgentState(t.agentState), append(json.RawMessage(nil), t.resumeData...)
}

// AppendAgentEvents implements the child's live durability boundary. Events
// have already been assigned monotonically by that child Agent.
func (t *Task) AppendAgentEvents(events []agent.RuntimeEvent) error {
	if len(events) == 0 {
		return nil
	}
	t.mu.Lock()
	last := uint64(0)
	if existing := t.agentState.Events; len(existing) > 0 {
		last = existing[len(existing)-1].Sequence
	}
	for _, event := range events {
		if event.Sequence != last+1 {
			t.mu.Unlock()
			return fmt.Errorf("child event sequence %d is not the next sequence %d", event.Sequence, last+1)
		}
		last = event.Sequence
	}
	registry := t.registry
	t.mu.Unlock()
	if registry != nil {
		if err := registry.appendAgentEvents(t.ID, events); err != nil {
			return err
		}
	}
	t.mu.Lock()
	t.agentState.Events = append(t.agentState.Events, events...)
	t.mu.Unlock()
	return nil
}

func (t *Task) SetAgentState(state agent.State) error {
	registry := t.registry
	if registry != nil {
		if err := registry.saveAgentState(t.ID, state); err != nil {
			return err
		}
	}
	t.mu.Lock()
	t.agentState = cloneAgentState(state)
	t.mu.Unlock()
	return nil
}

func (t *Task) Resume(prompt string) error {
	return t.ResumeContext(context.Background(), prompt)
}

func (t *Task) ResumeContext(ctx context.Context, prompt string) error {
	t.mu.Lock()
	fn := t.resume
	t.mu.Unlock()
	if fn == nil {
		return fmt.Errorf("agent %s does not support follow-ups", t.ID)
	}
	return fn(ctx, prompt)
}

func (t *Task) SupportsResume() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.resume != nil
}

// SetActivity records what the agent is currently doing (e.g. its running
// tool call) for live status displays.
func (t *Task) SetActivity(text string) {
	t.mu.Lock()
	t.activity = text
	registry := t.registry
	t.mu.Unlock()
	if registry != nil {
		_ = registry.persist()
	}
}

func (t *Task) Activity() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.activity
}

// SetPeek installs a snapshot function for the agent's transcript so UIs can
// watch a running task and re-read a finished one.
func (t *Task) SetPeek(fn func() []agent.Message) {
	t.mu.Lock()
	t.peek = fn
	t.mu.Unlock()
}

func (t *Task) PeekMessages() []agent.Message {
	t.mu.Lock()
	fn := t.peek
	t.mu.Unlock()
	if fn == nil {
		t.mu.Lock()
		state := cloneAgentState(t.agentState)
		t.mu.Unlock()
		var restored agent.Agent
		if err := restored.Restore(state); err != nil {
			return nil
		}
		return restored.MessagesSnapshot()
	}
	return fn()
}

// KindCommand marks events published for backgrounded shell commands rather
// than subagent runs; those have no Task in the registry.
const KindCommand = "command"

// KindSchedule marks a scheduled task coming due. Unlike the other kinds it
// carries work to do rather than a finished result: Description holds the
// schedule expression and Result the task prompt.
const KindSchedule = "schedule"

// Event is an immutable snapshot of one finished run, taken before the task
// can be resumed — delivery must never read the live task, which a relaunch
// may already have reset.
type Event struct {
	Task        *Task
	ID          string
	Kind        string
	Description string
	AgentType   string
	Seq         int
	Status      Status
	Result      string
	Elapsed     time.Duration
}

// Label names the event source for status lines and notifications.
func (e Event) Label() string {
	switch e.Kind {
	case KindCommand:
		return "Background command"
	case KindSchedule:
		return "Scheduled task"
	default:
		return "Background agent"
	}
}

// Verb describes how the run ended, for status lines: "finished", "replied"
// (a resumed run), "failed", or "was stopped" — and "is due" for a schedule,
// which announces work rather than reporting it.
func (e Event) Verb() string {
	if e.Kind == KindSchedule {
		return "is due"
	}
	switch e.Status {
	case StatusFailed:
		return "failed"
	case StatusStopped:
		return "was stopped"
	default:
		if e.Seq > 1 {
			return "replied"
		}
		return "finished"
	}
}

// Notice renders the one-line user-facing announcement shown in the chat when
// the event is delivered.
func (e Event) Notice() string {
	if e.Kind == KindSchedule {
		return fmt.Sprintf("%s %s %s (%s)", e.Label(), e.ID, e.Verb(), e.Description)
	}
	return fmt.Sprintf("%s %s %s (%s, %s)", e.Label(), e.ID, e.Verb(), e.Description, e.Elapsed.Round(time.Second))
}

// Notification renders the model-facing completion block delivered as hidden
// context by every UI surface.
func (e Event) Notification() string {
	if e.Kind == KindSchedule {
		return fmt.Sprintf(
			"<scheduled-task>\nScheduled task %s (%s) is due.\nThis is an automated trigger, not user input — no human has reviewed or approved anything since the last real user message.\nRun it now, then report only what needs the user's attention.\n\nTask:\n%s\n</scheduled-task>",
			e.ID, e.Description, e.Result,
		)
	}

	return fmt.Sprintf(
		"<task-notification>\n%s %s (%s: %s) %s after %s.\nThis is an automated notification, not user input — no human has reviewed or approved anything since the last real user message.\nThe user cannot see this result. Use it to continue your work and relay what matters in your response.\n\nResult:\n%s\n</task-notification>",
		e.Label(), e.ID, e.AgentType, e.Description, e.Verb(), e.Elapsed.Round(time.Second), e.Result,
	)
}

func (t *Task) Status() Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status
}

// Result returns the agent's final report; empty while the task is running.
func (t *Task) Result() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.result
}

func (t *Task) Elapsed() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished.IsZero() {
		return time.Since(t.Started)
	}
	return t.finished.Sub(t.Started)
}

// Registry tracks the background subagents of one session. Completed tasks
// stay listed for task_output until the session ends; completion events are
// buffered so a consumer that attaches late still receives them.
type Registry struct {
	ctx    context.Context
	cancel context.CancelFunc

	events chan Event

	mu       sync.Mutex
	tasks    []*Task
	running  int
	launched int
	closed   bool

	path          string
	persistMu     sync.Mutex
	persistGate   sync.RWMutex
	agentJournals sync.Map
}

func NewRegistry() *Registry {
	return newRegistry("")
}

func newRegistry(path string) *Registry {
	// Background tasks deliberately do not inherit the foreground turn's
	// progress or stream lifecycle callbacks: their output is represented by
	// Task activity/results, and sub-agent retries install their own local reset
	// handler around the buffered run.
	ctx, cancel := context.WithCancel(context.Background())
	return &Registry{
		ctx:    ctx,
		cancel: cancel,
		events: make(chan Event, MaxPerSession),
		path:   path,
	}
}

// Events delivers one snapshot per finished run.
func (r *Registry) Events() <-chan Event { return r.events }

// Publish delivers an external background event (e.g. an exec_command exit)
// through the session's notification channel alongside subagent completions.
func (r *Registry) Publish(ev Event) {
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return
	}
	r.send(ev)
}

// send delivers ev, evicting the oldest buffered event rather than dropping
// the newest when the channel is full.
func (r *Registry) send(ev Event) {
	for {
		select {
		case r.events <- ev:
			return
		default:
			select {
			case <-r.events:
			default:
			}
		}
	}
}

// Done is closed when the registry shuts down, so event consumers can exit.
func (r *Registry) Done() <-chan struct{} { return r.ctx.Done() }

// Launch starts run in a goroutine detached from the launching tool call; it
// is canceled only by Stop or Close. The returned error reports cap or
// shutdown rejections, never run failures — those surface via the task. run
// receives the task so it can publish activity and a transcript peek.
func (r *Registry) Launch(description, agentType string, run func(ctx context.Context, t *Task) (string, error)) (*Task, error) {
	return r.LaunchAgent(uuid.NewString(), description, agentType, nil, run)
}

// LaunchAgent is Launch with a stable child identity and a preparation step
// that is persisted before the goroutine can start.
func (r *Registry) LaunchAgent(agentID, description, agentType string, prepare func(*Task) error, run func(ctx context.Context, t *Task) (string, error)) (*Task, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, fmt.Errorf("session is shutting down")
	}
	if r.running >= MaxConcurrent {
		r.mu.Unlock()
		return nil, fmt.Errorf("too many background agents running (max %d); wait for one to finish or run this agent synchronously", MaxConcurrent)
	}
	if r.launched >= MaxPerSession {
		r.mu.Unlock()
		return nil, fmt.Errorf("background agent limit reached for this session (max %d)", MaxPerSession)
	}

	ctx, cancel := context.WithTimeout(r.ctx, MaxRunDuration)
	ctx = tool.WithBackgroundOrigin(ctx)
	if agentID == "" {
		agentID = uuid.NewString()
	}
	if r.hasAgentIDLocked(agentID) {
		r.mu.Unlock()
		cancel()
		return nil, fmt.Errorf("background agent identity %s already exists", agentID)
	}
	taskID := r.uniqueTaskIDLocked(agentID)
	t := &Task{
		ID:          taskID,
		AgentID:     agentID,
		Description: description,
		AgentType:   agentType,
		Started:     time.Now(),
		status:      StatusRunning,
		cancel:      cancel,
		seq:         1,
		registry:    r,
	}
	r.tasks = append(r.tasks, t)
	r.running++
	r.launched++
	r.mu.Unlock()
	if prepare != nil {
		if err := prepare(t); err != nil {
			r.mu.Lock()
			r.removeTaskLocked(t)
			r.running--
			r.launched--
			r.mu.Unlock()
			cancel()
			return nil, err
		}
	}
	if err := r.persist(); err != nil {
		r.mu.Lock()
		r.removeTaskLocked(t)
		r.running--
		r.launched--
		r.mu.Unlock()
		cancel()
		return nil, err
	}
	r.mu.Lock()
	if r.closed {
		r.removeTaskLocked(t)
		r.running--
		r.launched--
		r.mu.Unlock()
		cancel()
		return nil, fmt.Errorf("session is shutting down")
	}
	r.mu.Unlock()

	go r.execute(ctx, cancel, t, run)

	return t, nil
}

// Adopt registers an already-finished run (a synchronous agent call) so it can
// be inspected and resumed like a background task. It emits no event — the
// result was already delivered inline. Returns nil when the registry is closed
// or at capacity; the caller then skips follow-up support.
func (r *Registry) Adopt(description, agentType, result string, elapsed time.Duration) *Task {
	t, _ := r.AdoptAgent(uuid.NewString(), description, agentType, result, elapsed, nil)
	return t
}

// AdoptAgent is Adopt with durable child state installed before it becomes
// visible to task_send or UI readers.
func (r *Registry) AdoptAgent(agentID, description, agentType, result string, elapsed time.Duration, prepare func(*Task) error) (*Task, error) {
	r.mu.Lock()
	if r.closed || r.launched >= MaxPerSession {
		r.mu.Unlock()
		return nil, nil
	}

	now := time.Now()
	if agentID == "" {
		agentID = uuid.NewString()
	}
	if r.hasAgentIDLocked(agentID) {
		r.mu.Unlock()
		return nil, fmt.Errorf("background agent identity %s already exists", agentID)
	}
	taskID := r.uniqueTaskIDLocked(agentID)
	t := &Task{
		ID:          taskID,
		AgentID:     agentID,
		Description: description,
		AgentType:   agentType,
		Started:     now.Add(-elapsed),
		status:      StatusDone,
		result:      result,
		finished:    now,
		seq:         1,
		registry:    r,
	}
	r.tasks = append(r.tasks, t)
	r.launched++
	r.mu.Unlock()
	if prepare != nil {
		if err := prepare(t); err != nil {
			r.mu.Lock()
			r.removeTaskLocked(t)
			r.launched--
			r.mu.Unlock()
			return nil, err
		}
	}
	if err := r.persist(); err != nil {
		r.mu.Lock()
		r.removeTaskLocked(t)
		r.launched--
		r.mu.Unlock()
		return nil, err
	}
	r.mu.Lock()
	if r.closed {
		r.removeTaskLocked(t)
		r.launched--
		r.mu.Unlock()
		return nil, fmt.Errorf("session is shutting down")
	}
	r.mu.Unlock()
	return t, nil
}

func (r *Registry) removeTaskLocked(target *Task) {
	for i, current := range r.tasks {
		if current == target {
			r.tasks = append(r.tasks[:i], r.tasks[i+1:]...)
			return
		}
	}
}

func (r *Registry) hasAgentIDLocked(agentID string) bool {
	for _, task := range r.tasks {
		if task.AgentID == agentID {
			return true
		}
	}
	return false
}

func (r *Registry) uniqueTaskIDLocked(agentID string) string {
	base := agentID
	if len(base) > 8 {
		base = base[:8]
	}
	candidate := base
	for suffix := 2; ; suffix++ {
		available := true
		for _, task := range r.tasks {
			if task.ID == candidate {
				available = false
				break
			}
		}
		if available {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", base, suffix)
	}
}

// Relaunch restarts a finished task with a new run — the task_send follow-up
// path. The task keeps its id and history; status, timing, and cancellation
// reset for the new run, and completion fires another event.
func (r *Registry) Relaunch(t *Task, run func(ctx context.Context, t *Task) (string, error)) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return fmt.Errorf("session is shutting down")
	}
	if r.running >= MaxConcurrent {
		r.mu.Unlock()
		return fmt.Errorf("too many background agents running (max %d); wait for one to finish", MaxConcurrent)
	}
	if r.launched >= MaxPerSession {
		r.mu.Unlock()
		return fmt.Errorf("background agent limit reached for this session (max %d)", MaxPerSession)
	}

	t.mu.Lock()
	if t.status == StatusRunning {
		t.mu.Unlock()
		r.mu.Unlock()
		return fmt.Errorf("agent %s is still running; its result arrives as a task notification", t.ID)
	}
	ctx, cancel := context.WithTimeout(r.ctx, MaxRunDuration)
	ctx = tool.WithBackgroundOrigin(ctx)
	t.status = StatusRunning
	t.stopped = false
	t.result = ""
	t.finished = time.Time{}
	t.cancel = cancel
	t.Started = time.Now()
	t.seq++
	t.mu.Unlock()

	r.running++
	r.launched++
	r.mu.Unlock()
	if err := r.persist(); err != nil {
		t.mu.Lock()
		t.status = StatusFailed
		t.result = fmt.Sprintf("error: could not persist relaunch: %v", err)
		t.finished = time.Now()
		t.cancel = nil
		t.mu.Unlock()
		r.mu.Lock()
		r.running--
		r.launched--
		r.mu.Unlock()
		cancel()
		return err
	}
	r.mu.Lock()
	if r.closed {
		r.running--
		r.launched--
		r.mu.Unlock()
		t.mu.Lock()
		t.status = StatusStopped
		t.result = "error: session shut down before the background agent restarted"
		t.finished = time.Now()
		t.cancel = nil
		t.mu.Unlock()
		cancel()
		return fmt.Errorf("session is shutting down")
	}
	r.mu.Unlock()

	go r.execute(ctx, cancel, t, run)

	return nil
}

func (r *Registry) execute(ctx context.Context, cancel context.CancelFunc, t *Task, run func(ctx context.Context, t *Task) (string, error)) {
	defer cancel()
	result, err := func() (out string, runErr error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				runErr = fmt.Errorf("background agent panicked: %v", recovered)
			}
		}()
		return run(ctx, t)
	}()

	t.mu.Lock()
	t.finished = time.Now()
	switch {
	case t.stopped:
		t.status = StatusStopped
	case err != nil:
		t.status = StatusFailed
		result = fmt.Sprintf("error: %v", err)
	default:
		t.status = StatusDone
	}
	t.result = result
	ev := Event{
		Task:        t,
		ID:          t.ID,
		Description: t.Description,
		AgentType:   t.AgentType,
		Seq:         t.seq,
		Status:      t.status,
		Result:      t.result,
		Elapsed:     t.finished.Sub(t.Started),
	}
	t.mu.Unlock()

	r.mu.Lock()
	r.running--
	closed := r.closed
	r.mu.Unlock()
	_ = r.persist()

	if !closed {
		r.send(ev)
	}
}

func (r *Registry) Get(id string) *Task {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.tasks {
		if t.ID == id {
			return t
		}
	}
	return nil
}

func (r *Registry) List() []*Task {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*Task(nil), r.tasks...)
}

func (r *Registry) Counts() (running, total int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running, len(r.tasks)
}

func (r *Registry) Stop(id string) error {
	t := r.Get(id)
	if t == nil {
		return fmt.Errorf("no background agent with id %s", id)
	}
	t.mu.Lock()
	if t.status != StatusRunning {
		status := t.status
		t.mu.Unlock()
		return fmt.Errorf("agent %s already finished (%s)", id, status)
	}
	t.stopped = true
	cancel := t.cancel
	t.mu.Unlock()
	_ = r.persist()
	cancel()
	return nil
}

// Close cancels all running tasks and stops event delivery.
func (r *Registry) Close() {
	if r == nil {
		return
	}
	// Exclude new writes and wait for an append or atomic snapshot already in
	// progress before returning. Canceled runs may unwind later, but they can no
	// longer recreate durable state after their session directory is deleted.
	r.persistGate.Lock()
	defer r.persistGate.Unlock()
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	r.mu.Unlock()
	r.cancel()
}
