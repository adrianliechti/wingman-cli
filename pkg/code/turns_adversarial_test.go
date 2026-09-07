package code

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type faultQueueAgent struct {
	*turnManagerTestAgent
	readFailure  atomic.Bool
	writeFailure atomic.Bool
	writes       atomic.Int32
	loadStarted  chan struct{}
	loadRelease  chan struct{}
}

func (a *faultQueueAgent) LoadTurnQueue(id string) (TurnQueueState, error) {
	if id == "slow" && a.loadStarted != nil {
		close(a.loadStarted)
		<-a.loadRelease
	}
	if a.readFailure.Load() {
		return TurnQueueState{}, errors.New("queue temporarily unreadable")
	}
	return a.turnManagerTestAgent.LoadTurnQueue(id)
}

func (a *faultQueueAgent) SaveTurnQueue(id string, state TurnQueueState) error {
	a.writes.Add(1)
	if a.writeFailure.Load() {
		return errors.New("disk full")
	}
	return a.turnManagerTestAgent.SaveTurnQueue(id, state)
}

func TestTurnManagerUnreadableQueueCannotBeOverwrittenAndCanRecover(t *testing.T) {
	for _, mutation := range []string{"clear", "cancel", "resume", "replace", "submit"} {
		t.Run(mutation, func(t *testing.T) {
			a := &faultQueueAgent{turnManagerTestAgent: newTurnManagerTestAgent()}
			a.queues = map[string]TurnQueueState{"s": {Inputs: []TurnInput{turnInput("saved", "valuable queued work", TurnInputFollowUp)}}}
			a.readFailure.Store(true)
			m := NewTurnManager(t.Context(), a, nil)
			defer m.Close()
			if m.Snapshot("s").Error == nil {
				t.Fatal("missing restoration error")
			}
			switch mutation {
			case "clear":
				_ = m.ClearQueue("s")
			case "cancel":
				_ = m.CancelAll("s")
			case "resume":
				m.Resume("s")
			case "replace":
				_ = m.ReplaceQueued("s", "saved", turnInput("saved", "edit", TurnInputFollowUp))
			case "submit":
				_, _ = m.Submit(t.Context(), "s", turnInput("new", "new work", TurnInputFollowUp))
			}
			if a.writes.Load() != 0 {
				t.Fatal("mutation overwrote a queue that could not be read")
			}
			a.readFailure.Store(false)
			snapshot := m.Snapshot("s")
			if snapshot.Error != nil || !snapshot.Paused || len(snapshot.Inputs) != 1 || snapshot.Inputs[0].ID != "saved" {
				t.Fatalf("queue did not recover intact and paused: %+v", snapshot)
			}
		})
	}
}

func TestTurnManagerClearRecoversFailedQueuePromotion(t *testing.T) {
	a := &faultQueueAgent{turnManagerTestAgent: newTurnManagerTestAgent()}
	a.queues = map[string]TurnQueueState{"s": {Inputs: []TurnInput{turnInput("saved", "saved", TurnInputFollowUp)}}}
	m := NewTurnManager(t.Context(), a, nil)
	defer m.Close()
	a.writeFailure.Store(true)
	if m.Resume("s") || m.Snapshot("s").Error == nil {
		t.Fatal("failed promotion was accepted")
	}
	a.writeFailure.Store(false)
	if err := m.ClearQueue("s"); err != nil {
		t.Fatal(err)
	}
	if snapshot := m.Snapshot("s"); snapshot.Error != nil || snapshot.Paused || len(snapshot.Inputs) != 0 {
		t.Fatalf("cleared queue remains poisoned: %+v", snapshot)
	}
	if _, err := m.Submit(t.Context(), "s", turnInput("new", "new", TurnInputFollowUp)); err != nil {
		t.Fatal(err)
	}
	if got := waitValue(t, a.starts); got != "new" {
		t.Fatalf("started %q", got)
	}
}

func TestTurnManagerSlowQueueLoadDoesNotBlockOtherSessions(t *testing.T) {
	a := &faultQueueAgent{turnManagerTestAgent: newTurnManagerTestAgent(), loadStarted: make(chan struct{}), loadRelease: make(chan struct{})}
	m := NewTurnManager(t.Context(), a, nil)
	defer m.Close()
	done := make(chan struct{})
	go func() { m.Snapshot("slow"); close(done) }()
	<-a.loadStarted
	defer func() { close(a.loadRelease); <-done }()
	accepted := make(chan error, 1)
	go func() {
		_, err := m.Submit(t.Context(), "fast", turnInput("new", "independent", TurnInputFollowUp))
		accepted <- err
	}()
	select {
	case err := <-accepted:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("another session's disk read blocked admission")
	}
}

func TestTurnManagerCloseJoinsAdmissionBeforeWorkerStarts(t *testing.T) {
	a := newTurnManagerTestAgent()
	publishing, release := make(chan struct{}), make(chan struct{})
	m := NewTurnManager(context.Background(), a, func(ev TurnEvent) {
		if ev.State == TurnInputActive {
			close(publishing)
			<-release
		}
	})
	submitted := make(chan struct{})
	go func() {
		_, _ = m.Submit(context.Background(), "s", turnInput("one", "one", TurnInputFollowUp))
		close(submitted)
	}()
	<-publishing
	closed := make(chan struct{})
	go func() { m.Close(); close(closed) }()
	<-m.ctx.Done()
	select {
	case <-closed:
		t.Error("Close returned while an accepted input was still being published")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-submitted
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not join the cancelled admission")
	}
	if snapshot := m.Snapshot("s"); len(snapshot.Inputs) != 0 {
		t.Fatalf("orphaned active input after Close: %+v", snapshot)
	}
	select {
	case <-a.starts:
		t.Fatal("agent executed after shutdown")
	default:
	}
}

func TestTurnManagerFinalizationPrecedesNextExecution(t *testing.T) {
	a := newTurnManagerTestAgent()
	finalizing, release := make(chan struct{}), make(chan struct{})
	m := NewTurnManager(t.Context(), a, func(ev TurnEvent) {
		if ev.InputID == "one" && ev.Executed {
			close(finalizing)
			<-release
		}
	})
	defer m.Close()
	if _, err := m.Submit(t.Context(), "s", turnInput("one", "one", TurnInputFollowUp)); err != nil {
		t.Fatal(err)
	}
	_ = waitValue(t, a.starts)
	a.releases <- struct{}{}
	<-finalizing
	next, err := m.Submit(t.Context(), "s", turnInput("two", "two", TurnInputFollowUp))
	if err != nil || next.State != TurnInputQueued {
		t.Errorf("submission during finalization: %+v %v", next, err)
	}
	select {
	case <-a.starts:
		t.Error("second execution overtook finalization")
	default:
	}
	close(release)
	if got := waitValue(t, a.starts); got != "two" {
		t.Fatalf("started %q", got)
	}
}
