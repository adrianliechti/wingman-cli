package code

import (
	"context"
	"errors"
	"iter"
	"sync"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/model"
)

type turnManagerTestAgent struct {
	mu sync.Mutex

	starts     chan string
	sessionIDs chan string
	releases   chan struct{}
	cancels    int
	steers     []string
	steerErr   error
	steerPanic bool
	steer      bool
}

type blockingSteerAgent struct {
	*turnManagerTestAgent
	started chan struct{}
	release chan struct{}
}

func (a *blockingSteerAgent) Steer(ctx context.Context, sessionID string, input TurnInput) error {
	if err := a.turnManagerTestAgent.Steer(ctx, sessionID, input); err != nil {
		return err
	}
	close(a.started)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-a.release:
		return nil
	}
}

func newTurnManagerTestAgent() *turnManagerTestAgent {
	return &turnManagerTestAgent{
		starts: make(chan string, 16), sessionIDs: make(chan string, 16), releases: make(chan struct{}, 16),
	}
}

func (a *turnManagerTestAgent) Name() string                                        { return "test" }
func (a *turnManagerTestAgent) Workspace() *Workspace                               { return nil }
func (a *turnManagerTestAgent) Models(string) ([]model.Model, string)               { return nil, "" }
func (a *turnManagerTestAgent) SetModel(context.Context, string, string) error      { return nil }
func (a *turnManagerTestAgent) Effort(string) (string, []string)                    { return "", nil }
func (a *turnManagerTestAgent) SetEffort(context.Context, string, string) error     { return nil }
func (a *turnManagerTestAgent) Modes(string) ([]Mode, string)                       { return nil, "" }
func (a *turnManagerTestAgent) SetMode(context.Context, string, string) error       { return nil }
func (a *turnManagerTestAgent) ListSessions(context.Context) ([]SessionInfo, error) { return nil, nil }
func (a *turnManagerTestAgent) NewSession(context.Context) (string, error)          { return "s", nil }
func (a *turnManagerTestAgent) LoadSession(context.Context, string) error           { return nil }
func (a *turnManagerTestAgent) DeleteSession(context.Context, string) error         { return nil }
func (a *turnManagerTestAgent) Messages(string) []agent.Message                     { return nil }
func (a *turnManagerTestAgent) Usage(string) agent.Usage                            { return agent.Usage{} }
func (a *turnManagerTestAgent) Close() error                                        { return nil }

func (a *turnManagerTestAgent) Send(ctx context.Context, _ string, input []agent.Content) (iter.Seq2[agent.Message, error], error) {
	text := input[0].Text
	if text == "panic" {
		panic("test panic")
	}
	return func(yield func(agent.Message, error) bool) {
		a.starts <- text
		a.sessionIDs <- SessionIDFromContext(ctx)
		if text == "reset" {
			agent.EmitStreamEvent(ctx, agent.StreamEventReset)
		}
		select {
		case <-ctx.Done():
			yield(agent.Message{}, ctx.Err())
			return
		case <-a.releases:
		}
		yield(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{Text: "done " + text}}}, nil)
	}, nil
}

func TestTurnManagerInstallsSessionIDOnRunContext(t *testing.T) {
	a := newTurnManagerTestAgent()
	m := NewTurnManager(context.Background(), a, nil)
	defer m.Close()

	if _, err := m.Submit(context.Background(), "session-1", turnInput("1", "context", TurnInputFollowUp)); err != nil {
		t.Fatal(err)
	}
	waitValue(t, a.starts)
	if got := waitValue(t, a.sessionIDs); got != "session-1" {
		t.Fatalf("session ID = %q, want session-1", got)
	}
	a.releases <- struct{}{}
}

func TestTurnManagerForwardsStreamLifecycleEvents(t *testing.T) {
	a := newTurnManagerTestAgent()
	events := make(chan TurnEvent, 32)
	m := NewTurnManager(context.Background(), a, func(ev TurnEvent) { events <- ev })
	defer m.Close()

	if _, err := m.Submit(context.Background(), "s", turnInput("1", "reset", TurnInputFollowUp)); err != nil {
		t.Fatal(err)
	}
	waitValue(t, a.starts)

	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev.StreamEvent == agent.StreamEventReset {
				if ev.SessionID != "s" || ev.InputID != "1" || ev.Message != nil {
					t.Fatalf("stream event metadata = %+v", ev)
				}
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for stream reset event")
		}
	}
}

func (a *turnManagerTestAgent) Cancel(string) {
	a.mu.Lock()
	a.cancels++
	a.mu.Unlock()
}

func (a *turnManagerTestAgent) TurnFeatures(string) TurnFeatures {
	return TurnFeatures{Steer: a.steer}
}

func (a *turnManagerTestAgent) Steer(_ context.Context, _ string, input TurnInput) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.steerPanic {
		panic("test steer panic")
	}
	if a.steerErr != nil {
		return a.steerErr
	}
	a.steers = append(a.steers, input.Content[0].Text)
	return nil
}

func turnInput(id, text string, intent TurnInputIntent) TurnInput {
	return TurnInput{ID: id, Intent: intent, Content: []agent.Content{{Text: text}}}
}

func waitValue[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for value")
		var zero T
		return zero
	}
}

func waitForState(t *testing.T, events <-chan TurnEvent, id string, state TurnInputState) TurnEvent {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev.InputID == id && ev.State == state {
				return ev
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s=%s", id, state)
		}
	}
}

func TestTurnManagerQueuesFIFO(t *testing.T) {
	a := newTurnManagerTestAgent()
	events := make(chan TurnEvent, 32)
	m := NewTurnManager(context.Background(), a, func(ev TurnEvent) { events <- ev })
	defer m.Close()

	first, err := m.Submit(context.Background(), "s", turnInput("1", "one", TurnInputFollowUp))
	if err != nil || first.State != TurnInputActive {
		t.Fatalf("first submit = %+v, %v", first, err)
	}
	second, _ := m.Submit(context.Background(), "s", turnInput("2", "two", TurnInputFollowUp))
	third, _ := m.Submit(context.Background(), "s", turnInput("3", "three", TurnInputFollowUp))
	if second.State != TurnInputQueued || second.Position != 1 || third.Position != 2 {
		t.Fatalf("queue positions = %+v, %+v", second, third)
	}

	if got := waitValue(t, a.starts); got != "one" {
		t.Fatalf("first start = %q", got)
	}
	a.releases <- struct{}{}
	waitForState(t, events, "1", TurnInputCompleted)
	if got := waitValue(t, a.starts); got != "two" {
		t.Fatalf("second start = %q", got)
	}
	a.releases <- struct{}{}
	waitForState(t, events, "2", TurnInputCompleted)
	if got := waitValue(t, a.starts); got != "three" {
		t.Fatalf("third start = %q", got)
	}
	a.releases <- struct{}{}
	waitForState(t, events, "3", TurnInputCompleted)
	if snap := m.Snapshot("s"); len(snap.Inputs) != 0 || snap.Paused {
		t.Fatalf("final snapshot = %+v", snap)
	}
}

func TestTurnManagerTerminalHandlerCanInspectPromotedFollowUp(t *testing.T) {
	a := newTurnManagerTestAgent()
	snapshots := make(chan TurnSnapshot, 1)
	var m *TurnManager
	m = NewTurnManager(context.Background(), a, func(ev TurnEvent) {
		if ev.InputID == "1" && ev.State == TurnInputCompleted {
			snapshots <- m.Snapshot(ev.SessionID)
		}
	})
	defer m.Close()

	_, _ = m.Submit(context.Background(), "s", turnInput("1", "one", TurnInputFollowUp))
	_, _ = m.Submit(context.Background(), "s", turnInput("2", "two", TurnInputFollowUp))
	_ = waitValue(t, a.starts)
	a.releases <- struct{}{}

	select {
	case snapshot := <-snapshots:
		if len(snapshot.Inputs) != 1 || snapshot.Inputs[0].ID != "2" || snapshot.Inputs[0].State != TurnInputActive {
			t.Fatalf("terminal snapshot = %+v", snapshot)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("terminal handler did not receive a snapshot")
	}

	if got := waitValue(t, a.starts); got != "two" {
		t.Fatalf("promoted start = %q", got)
	}
	a.releases <- struct{}{}
}

func TestTurnManagerCancelPausesAndResumesQueue(t *testing.T) {
	a := newTurnManagerTestAgent()
	events := make(chan TurnEvent, 32)
	m := NewTurnManager(context.Background(), a, func(ev TurnEvent) { events <- ev })
	defer m.Close()

	_, _ = m.Submit(context.Background(), "s", turnInput("1", "one", TurnInputFollowUp))
	_, _ = m.Submit(context.Background(), "s", turnInput("2", "two", TurnInputFollowUp))
	if got := waitValue(t, a.starts); got != "one" {
		t.Fatalf("first start = %q", got)
	}
	m.CancelCurrent("s")
	waitForState(t, events, "1", TurnInputCancelled)
	snap := m.Snapshot("s")
	if !snap.Paused || len(snap.Inputs) != 1 || snap.Inputs[0].ID != "2" {
		t.Fatalf("paused snapshot = %+v", snap)
	}
	if !m.Resume("s") {
		t.Fatal("resume returned false")
	}
	if got := waitValue(t, a.starts); got != "two" {
		t.Fatalf("resumed start = %q", got)
	}
	a.releases <- struct{}{}
	waitForState(t, events, "2", TurnInputCompleted)
}

func TestTurnManagerSteersAndFallsBackToQueue(t *testing.T) {
	a := newTurnManagerTestAgent()
	a.steer = true
	events := make(chan TurnEvent, 32)
	m := NewTurnManager(context.Background(), a, func(ev TurnEvent) { events <- ev })
	defer m.Close()

	_, _ = m.Submit(context.Background(), "s", turnInput("1", "one", TurnInputFollowUp))
	_ = waitValue(t, a.starts)
	steered, err := m.Submit(context.Background(), "s", turnInput("2", "guide", TurnInputSteer))
	if err != nil || steered.State != TurnInputSteered {
		t.Fatalf("steer = %+v, %v", steered, err)
	}
	a.mu.Lock()
	if len(a.steers) != 1 || a.steers[0] != "guide" {
		t.Fatalf("steers = %v", a.steers)
	}
	a.steerErr = ErrTurnNotSteerable
	a.mu.Unlock()
	fallback, err := m.Submit(context.Background(), "s", turnInput("3", "later", TurnInputSteer))
	if err != nil || fallback.State != TurnInputQueued || fallback.Intent != TurnInputFollowUp {
		t.Fatalf("fallback = %+v, %v", fallback, err)
	}
	a.releases <- struct{}{}
	waitForState(t, events, "1", TurnInputCompleted)
	waitForState(t, events, "2", TurnInputCompleted)
	if got := waitValue(t, a.starts); got != "later" {
		t.Fatalf("fallback start = %q", got)
	}
	a.releases <- struct{}{}
	waitForState(t, events, "3", TurnInputCompleted)
}

func TestTurnManagerSteeredInputFollowsCancellation(t *testing.T) {
	a := newTurnManagerTestAgent()
	a.steer = true
	events := make(chan TurnEvent, 32)
	m := NewTurnManager(context.Background(), a, func(ev TurnEvent) { events <- ev })
	defer m.Close()

	_, _ = m.Submit(context.Background(), "s", turnInput("1", "one", TurnInputFollowUp))
	_ = waitValue(t, a.starts)
	steered, err := m.Submit(context.Background(), "s", turnInput("2", "guide", TurnInputSteer))
	if err != nil || steered.State != TurnInputSteered {
		t.Fatalf("steer = %+v, %v", steered, err)
	}
	m.CancelAll("s")
	waitForState(t, events, "1", TurnInputCancelled)
	waitForState(t, events, "2", TurnInputCancelled)
	if snapshot := m.Snapshot("s"); len(snapshot.Inputs) != 0 {
		t.Fatalf("cancelled steering remained live: %+v", snapshot)
	}

	if _, err := m.Submit(context.Background(), "s", turnInput("2", "reused", TurnInputFollowUp)); err != nil {
		t.Fatalf("cancelled steer id was not released: %v", err)
	}
	_ = waitValue(t, a.starts)
	a.releases <- struct{}{}
	waitForState(t, events, "2", TurnInputCompleted)
}

func TestTurnManagerDoesNotAttachLateSteerToPromotedTurn(t *testing.T) {
	base := newTurnManagerTestAgent()
	base.steer = true
	a := &blockingSteerAgent{
		turnManagerTestAgent: base,
		started:              make(chan struct{}),
		release:              make(chan struct{}),
	}
	events := make(chan TurnEvent, 32)
	m := NewTurnManager(context.Background(), a, func(ev TurnEvent) { events <- ev })
	defer m.Close()

	_, _ = m.Submit(context.Background(), "s", turnInput("1", "one", TurnInputFollowUp))
	_ = waitValue(t, a.starts)
	_, _ = m.Submit(context.Background(), "s", turnInput("2", "two", TurnInputFollowUp))

	type submitResult struct {
		snapshot TurnInputSnapshot
		err      error
	}
	result := make(chan submitResult, 1)
	go func() {
		snapshot, err := m.Submit(context.Background(), "s", turnInput("steer", "guide", TurnInputSteer))
		result <- submitResult{snapshot: snapshot, err: err}
	}()
	select {
	case <-a.started:
	case <-time.After(2 * time.Second):
		t.Fatal("steer did not start")
	}

	a.releases <- struct{}{}
	waitForState(t, events, "1", TurnInputCompleted)
	if got := waitValue(t, a.starts); got != "two" {
		t.Fatalf("promoted start = %q", got)
	}
	close(a.release)

	got := waitValue(t, result)
	if got.err != nil || got.snapshot.State != TurnInputCompleted {
		t.Fatalf("late steer = %+v, %v", got.snapshot, got.err)
	}
	if snapshot := m.Snapshot("s"); len(snapshot.Inputs) != 1 || snapshot.Inputs[0].ID != "2" {
		t.Fatalf("late steer attached to promoted turn: %+v", snapshot)
	}

	a.releases <- struct{}{}
	waitForState(t, events, "2", TurnInputCompleted)
}

func TestTurnManagerSurfacesUnexpectedSteerFailure(t *testing.T) {
	a := newTurnManagerTestAgent()
	a.steer = true
	m := NewTurnManager(context.Background(), a, nil)
	defer m.Close()

	_, _ = m.Submit(context.Background(), "s", turnInput("1", "one", TurnInputFollowUp))
	_ = waitValue(t, a.starts)
	a.mu.Lock()
	a.steerErr = errors.New("transport failed")
	a.mu.Unlock()
	if _, err := m.Submit(context.Background(), "s", turnInput("2", "guide", TurnInputSteer)); err == nil || err.Error() != "steer active turn: transport failed" {
		t.Fatalf("steer error = %v", err)
	}
	if snap := m.Snapshot("s"); len(snap.Inputs) != 1 || snap.Inputs[0].ID != "1" {
		t.Fatalf("unexpected steer was queued: %+v", snap)
	}
	a.releases <- struct{}{}
}

func TestTurnManagerContainsSteerPanic(t *testing.T) {
	a := newTurnManagerTestAgent()
	a.steer = true
	a.steerPanic = true
	m := NewTurnManager(context.Background(), a, nil)
	defer m.Close()

	_, _ = m.Submit(context.Background(), "s", turnInput("1", "one", TurnInputFollowUp))
	_ = waitValue(t, a.starts)
	_, err := m.Submit(context.Background(), "s", turnInput("2", "guide", TurnInputSteer))
	if err == nil || err.Error() != "steer active turn: agent steer panicked: test steer panic" {
		t.Fatalf("steer panic error = %v", err)
	}
	if snapshot := m.Snapshot("s"); len(snapshot.Inputs) != 1 || snapshot.Inputs[0].ID != "1" {
		t.Fatalf("steer panic changed queue: %+v", snapshot)
	}
	a.releases <- struct{}{}
}

func TestTurnManagerNormalizesSteerWithoutActiveTurn(t *testing.T) {
	a := newTurnManagerTestAgent()
	a.steer = true
	m := NewTurnManager(context.Background(), a, nil)
	defer m.Close()

	snapshot, err := m.Submit(context.Background(), "s", turnInput("1", "new turn", TurnInputSteer))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != TurnInputActive || snapshot.Intent != TurnInputFollowUp {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if got := waitValue(t, a.starts); got != "new turn" {
		t.Fatalf("start = %q", got)
	}
	a.releases <- struct{}{}
}

func TestTurnManagerRejectsInvalidIntent(t *testing.T) {
	a := newTurnManagerTestAgent()
	m := NewTurnManager(context.Background(), a, nil)
	defer m.Close()

	input := turnInput("1", "one", TurnInputIntent("later"))
	if _, err := m.Submit(context.Background(), "s", input); !errors.Is(err, ErrInvalidIntent) {
		t.Fatalf("invalid intent error = %v", err)
	}
	if snapshot := m.Snapshot("s"); len(snapshot.Inputs) != 0 {
		t.Fatalf("invalid input was accepted: %+v", snapshot)
	}
}

func TestTurnManagerRemoveAndReplaceQueued(t *testing.T) {
	a := newTurnManagerTestAgent()
	m := NewTurnManager(context.Background(), a, nil)
	defer m.Close()
	_, _ = m.Submit(context.Background(), "s", turnInput("1", "one", TurnInputFollowUp))
	_ = waitValue(t, a.starts)
	_, _ = m.Submit(context.Background(), "s", turnInput("2", "old", TurnInputFollowUp))
	_, _ = m.Submit(context.Background(), "s", turnInput("3", "remove", TurnInputFollowUp))
	if err := m.ReplaceQueued("s", "2", turnInput("ignored", "new", TurnInputFollowUp)); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveQueued("s", "3"); err != nil {
		t.Fatal(err)
	}
	a.releases <- struct{}{}
	if got := waitValue(t, a.starts); got != "new" {
		t.Fatalf("replacement start = %q", got)
	}
	a.releases <- struct{}{}
}

func TestTurnManagerRejectsDuplicateLiveInputIDs(t *testing.T) {
	a := newTurnManagerTestAgent()
	m := NewTurnManager(context.Background(), a, nil)
	defer m.Close()

	_, err := m.Submit(context.Background(), "s", turnInput("same", "one", TurnInputFollowUp))
	if err != nil {
		t.Fatal(err)
	}
	_ = waitValue(t, a.starts)
	if _, err := m.Submit(context.Background(), "s", turnInput("same", "duplicate", TurnInputFollowUp)); !errors.Is(err, ErrDuplicateInput) {
		t.Fatalf("duplicate submit error = %v", err)
	}

	a.releases <- struct{}{}
	deadline := time.Now().Add(2 * time.Second)
	for len(m.Snapshot("s").Inputs) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if _, err := m.Submit(context.Background(), "s", turnInput("same", "reused", TurnInputFollowUp)); err != nil {
		t.Fatalf("completed id should be reusable: %v", err)
	}
	if got := waitValue(t, a.starts); got != "reused" {
		t.Fatalf("reused start = %q", got)
	}
	a.releases <- struct{}{}
}

func TestTurnManagerCancelCanClearQueue(t *testing.T) {
	a := newTurnManagerTestAgent()
	events := make(chan TurnEvent, 32)
	m := NewTurnManager(context.Background(), a, func(ev TurnEvent) { events <- ev })
	defer m.Close()

	_, _ = m.Submit(context.Background(), "s", turnInput("1", "one", TurnInputFollowUp))
	_, _ = m.Submit(context.Background(), "s", turnInput("2", "two", TurnInputFollowUp))
	_ = waitValue(t, a.starts)
	m.CancelAll("s")
	waitForState(t, events, "2", TurnInputCancelled)
	waitForState(t, events, "1", TurnInputCancelled)
	if snap := m.Snapshot("s"); snap.Paused || len(snap.Inputs) != 0 {
		t.Fatalf("cleared snapshot = %+v", snap)
	}
}

func TestTurnManagerContainsAgentPanicAndContinuesQueue(t *testing.T) {
	a := newTurnManagerTestAgent()
	events := make(chan TurnEvent, 32)
	m := NewTurnManager(context.Background(), a, func(ev TurnEvent) { events <- ev })
	defer m.Close()

	_, _ = m.Submit(context.Background(), "s", turnInput("1", "panic", TurnInputFollowUp))
	_, _ = m.Submit(context.Background(), "s", turnInput("2", "after", TurnInputFollowUp))
	failed := waitForState(t, events, "1", TurnInputFailed)
	if failed.Err == nil || failed.Err.Error() != "agent turn panicked: test panic" {
		t.Fatalf("panic error = %v", failed.Err)
	}
	if got := waitValue(t, a.starts); got != "after" {
		t.Fatalf("next start = %q", got)
	}
	a.releases <- struct{}{}
}
