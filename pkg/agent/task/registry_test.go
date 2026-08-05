package task_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/task"
)

func waitEvent(t *testing.T, r *task.Registry) task.Event {
	t.Helper()
	select {
	case ev := <-r.Events():
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no completion event")
		return task.Event{}
	}
}

func TestLaunchDeliversCompletionEvent(t *testing.T) {
	r := task.NewRegistry()
	defer r.Close()

	launched, err := r.Launch("map auth flow", "explore", func(context.Context, *task.Task) (string, error) {
		return "the report", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if launched.ID == "" || launched.Description != "map auth flow" || launched.AgentType != "explore" {
		t.Fatalf("task = %+v", launched)
	}

	done := waitEvent(t, r)
	if done.Task != launched {
		t.Fatal("event delivered a different task")
	}
	if done.Status != task.StatusDone || done.Result != "the report" {
		t.Fatalf("status = %s, result = %q", done.Status, done.Result)
	}
}

func TestLaunchErrorMarksFailed(t *testing.T) {
	r := task.NewRegistry()
	defer r.Close()

	if _, err := r.Launch("d", "explore", func(context.Context, *task.Task) (string, error) {
		return "", errors.New("boom")
	}); err != nil {
		t.Fatal(err)
	}

	done := waitEvent(t, r)
	if done.Status != task.StatusFailed {
		t.Fatalf("status = %s, want failed", done.Status)
	}
	if done.Result != "error: boom" {
		t.Fatalf("result = %q", done.Result)
	}
}

func TestStopCancelsRun(t *testing.T) {
	r := task.NewRegistry()
	defer r.Close()

	launched, err := r.Launch("d", "explore", func(ctx context.Context, _ *task.Task) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := r.Stop(launched.ID); err != nil {
		t.Fatal(err)
	}

	done := waitEvent(t, r)
	if done.Status != task.StatusStopped {
		t.Fatalf("status = %s, want stopped", done.Status)
	}

	if err := r.Stop(launched.ID); err == nil {
		t.Fatal("stopping a finished task should error")
	}
	if err := r.Stop("nope"); err == nil {
		t.Fatal("stopping an unknown task should error")
	}
}

func TestConcurrencyCapRejectsLaunch(t *testing.T) {
	r := task.NewRegistry()
	defer r.Close()

	release := make(chan struct{})
	for range task.MaxConcurrent {
		if _, err := r.Launch("d", "explore", func(context.Context, *task.Task) (string, error) {
			<-release
			return "ok", nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := r.Launch("d", "explore", func(context.Context, *task.Task) (string, error) { return "", nil }); err == nil {
		t.Fatal("launch above the concurrency cap should error")
	}

	close(release)
	for range task.MaxConcurrent {
		waitEvent(t, r)
	}

	if _, err := r.Launch("d", "explore", func(context.Context, *task.Task) (string, error) { return "", nil }); err != nil {
		t.Fatalf("launch after slots freed = %v", err)
	}
}

func TestCloseCancelsRunningAndRejectsLaunch(t *testing.T) {
	r := task.NewRegistry()

	canceled := make(chan struct{})
	if _, err := r.Launch("d", "explore", func(ctx context.Context, _ *task.Task) (string, error) {
		<-ctx.Done()
		close(canceled)
		return "", ctx.Err()
	}); err != nil {
		t.Fatal(err)
	}

	r.Close()

	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("running task was not canceled by Close")
	}

	if _, err := r.Launch("d", "explore", func(context.Context, *task.Task) (string, error) { return "", nil }); err == nil {
		t.Fatal("launch after Close should error")
	}
}

func TestRelaunchRunsAgainAndKeepsIdentity(t *testing.T) {
	r := task.NewRegistry()
	defer r.Close()

	launched, err := r.Launch("d", "explore", func(context.Context, *task.Task) (string, error) {
		return "first", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if waitEvent(t, r).Result != "first" {
		t.Fatal("first run result missing")
	}
	if launched.Seq() != 1 {
		t.Fatalf("seq = %d, want 1", launched.Seq())
	}

	if err := r.Relaunch(launched, func(context.Context, *task.Task) (string, error) {
		return "second", nil
	}); err != nil {
		t.Fatal(err)
	}

	done := waitEvent(t, r)
	if done.Task != launched {
		t.Fatal("relaunch created a different task")
	}
	if done.Status != task.StatusDone || done.Result != "second" || done.Seq != 2 {
		t.Fatalf("status = %s, result = %q, seq = %d", done.Status, done.Result, done.Seq)
	}

	if _, total := r.Counts(); total != 1 {
		t.Fatalf("total tasks = %d, want 1", total)
	}
}

func TestRelaunchRejectsRunningTask(t *testing.T) {
	r := task.NewRegistry()
	defer r.Close()

	release := make(chan struct{})
	launched, err := r.Launch("d", "explore", func(context.Context, *task.Task) (string, error) {
		<-release
		return "ok", nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := r.Relaunch(launched, func(context.Context, *task.Task) (string, error) { return "", nil }); err == nil {
		t.Fatal("relaunch of a running task should error")
	}

	close(release)
	waitEvent(t, r)
}

func TestResumeWithoutHookErrors(t *testing.T) {
	r := task.NewRegistry()
	defer r.Close()

	launched, err := r.Launch("d", "explore", func(context.Context, *task.Task) (string, error) { return "", nil })
	if err != nil {
		t.Fatal(err)
	}
	waitEvent(t, r)

	if err := launched.Resume("more"); err == nil {
		t.Fatal("resume without a hook should error")
	}
}

func TestAdoptRegistersFinishedRun(t *testing.T) {
	r := task.NewRegistry()
	defer r.Close()

	adopted := r.Adopt("sync run", "explore", "the report", 3*time.Second)
	if adopted == nil {
		t.Fatal("adopt returned nil")
	}
	if adopted.Status() != task.StatusDone || adopted.Result() != "the report" {
		t.Fatalf("status = %s, result = %q", adopted.Status(), adopted.Result())
	}
	if adopted.Seq() != 1 {
		t.Fatalf("seq = %d, want 1", adopted.Seq())
	}
	if got := adopted.Elapsed().Round(time.Second); got != 3*time.Second {
		t.Fatalf("elapsed = %s, want 3s", got)
	}
	if r.Get(adopted.ID) != adopted {
		t.Fatal("adopted task not listed in registry")
	}

	select {
	case ev := <-r.Events():
		t.Fatalf("adoption must not emit an event, got %+v", ev)
	default:
	}

	resumed := ""
	adopted.SetResume(func(prompt string) error {
		resumed = prompt
		return nil
	})
	if err := adopted.Resume("follow up"); err != nil || resumed != "follow up" {
		t.Fatalf("resume: err = %v, prompt = %q", err, resumed)
	}
}

func TestAdoptRejectsClosedRegistry(t *testing.T) {
	r := task.NewRegistry()
	r.Close()

	if r.Adopt("d", "explore", "out", time.Second) != nil {
		t.Fatal("closed registry must not adopt")
	}
}

func TestEventDeliveryEvictsOldest(t *testing.T) {
	r := task.NewRegistry()
	defer r.Close()

	for i := range task.MaxPerSession + 1 {
		r.Publish(task.Event{ID: string(rune('A' + i%26)), Seq: i})
	}

	first := waitEvent(t, r)
	if first.Seq != 1 {
		t.Fatalf("first buffered event seq = %d, want oldest evicted (seq 1 kept)", first.Seq)
	}

	drained := 1
	for {
		select {
		case ev := <-r.Events():
			drained++
			if drained == task.MaxPerSession && ev.Seq != task.MaxPerSession {
				t.Fatalf("last event seq = %d, want %d", ev.Seq, task.MaxPerSession)
			}
		default:
			if drained != task.MaxPerSession {
				t.Fatalf("drained %d events, want %d", drained, task.MaxPerSession)
			}
			return
		}
	}
}
