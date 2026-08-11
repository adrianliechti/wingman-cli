package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/task"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/schedule"
	"github.com/adrianliechti/wingman-agent/pkg/code"
)

func newScheduleSession(t *testing.T) *sessionState {
	t.Helper()

	return &sessionState{
		parent:    &Agent{workspace: &code.Workspace{RootPath: t.TempDir()}},
		tasks:     task.NewRegistry(),
		schedules: schedule.NewMemoryStore(),
	}
}

func seedTask(t *testing.T, s *sessionState, task schedule.Task) {
	t.Helper()

	err := s.schedules.Mutate(func(tasks []schedule.Task) ([]schedule.Task, error) {
		return append(tasks, task), nil
	})
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}
}

func waitEvent(t *testing.T, s *sessionState) (task.Event, bool) {
	t.Helper()

	select {
	case ev := <-s.tasks.Events():
		return ev, true
	case <-time.After(time.Second):
		return task.Event{}, false
	}
}

func TestFireScheduleNotifiesSession(t *testing.T) {
	s := newScheduleSession(t)
	defer s.tasks.Close()

	seedTask(t, s, schedule.Task{
		ID:        "job-1",
		Prompt:    "check the deploy",
		Schedule:  "every 1h",
		Status:    schedule.StatusActive,
		CreatedAt: time.Now().Add(-2 * time.Hour),
	})

	tasks, _ := s.schedules.List()
	s.fireSchedule(context.Background(), tasks[0])

	ev, ok := waitEvent(t, s)
	if !ok {
		t.Fatal("expected a schedule notification")
	}
	if ev.Kind != task.KindSchedule || ev.ID != "job-1" {
		t.Fatalf("event = %+v, want a schedule event for job-1", ev)
	}
	if ev.Description != "every 1h" || ev.Result != "check the deploy" {
		t.Fatalf("event carries %q / %q, want the schedule and the prompt", ev.Description, ev.Result)
	}
	if note := ev.Notification(); !strings.Contains(note, "<scheduled-task>") || !strings.Contains(note, "check the deploy") {
		t.Fatalf("notification = %q", note)
	}

	after, _ := s.schedules.List()
	if len(after) != 1 || after[0].LastRun == nil {
		t.Fatalf("tasks = %#v, want the recurring task kept with a last run", after)
	}
	if after[0].Status != schedule.StatusActive {
		t.Fatalf("task status = %q, want it left active", after[0].Status)
	}
}

func TestFireScheduleDropsOneTimeTask(t *testing.T) {
	s := newScheduleSession(t)
	defer s.tasks.Close()

	at := time.Now().Add(-time.Minute)
	seedTask(t, s, schedule.Task{
		ID:        "once-1",
		Prompt:    "remind me",
		Schedule:  at.Format("2006-01-02T15:04:05"),
		Status:    schedule.StatusActive,
		CreatedAt: at.Add(-time.Hour),
	})

	tasks, _ := s.schedules.List()
	s.fireSchedule(context.Background(), tasks[0])

	if _, ok := waitEvent(t, s); !ok {
		t.Fatal("expected a schedule notification")
	}

	after, _ := s.schedules.List()
	if len(after) != 0 {
		t.Fatalf("tasks = %#v, want the one-time task removed", after)
	}
}

func TestFireScheduleHonorsPreCheckScript(t *testing.T) {
	s := newScheduleSession(t)
	defer s.tasks.Close()

	seedTask(t, s, schedule.Task{
		ID:        "gated",
		Prompt:    "only when changed",
		Schedule:  "every 1h",
		Script:    `echo '{"wake": false}'`,
		Status:    schedule.StatusActive,
		CreatedAt: time.Now().Add(-2 * time.Hour),
	})

	tasks, _ := s.schedules.List()
	s.fireSchedule(context.Background(), tasks[0])

	select {
	case ev := <-s.tasks.Events():
		t.Fatalf("wake=false should not notify, got %+v", ev)
	case <-time.After(200 * time.Millisecond):
	}

	after, _ := s.schedules.List()
	if after[0].LastRun == nil {
		t.Fatal("a skipped run still counts as a run for scheduling purposes")
	}
}

func TestFireScheduleAttachesGateOutput(t *testing.T) {
	s := newScheduleSession(t)
	defer s.tasks.Close()

	seedTask(t, s, schedule.Task{
		ID:        "gated-wake",
		Prompt:    "investigate",
		Schedule:  "every 1h",
		Script:    `echo build-broken`,
		Status:    schedule.StatusActive,
		CreatedAt: time.Now().Add(-2 * time.Hour),
	})

	tasks, _ := s.schedules.List()
	s.fireSchedule(context.Background(), tasks[0])

	ev, ok := waitEvent(t, s)
	if !ok {
		t.Fatal("non-JSON gate output should fail open and notify")
	}
	if !strings.Contains(ev.Result, "investigate") || !strings.Contains(ev.Result, "build-broken") {
		t.Fatalf("result = %q, want the prompt plus the gate output", ev.Result)
	}
}

func TestNextScheduleWaitTracksShortIntervals(t *testing.T) {
	s := newScheduleSession(t)
	defer s.tasks.Close()

	if wait := s.nextScheduleWait(); wait != scheduleWaitMax {
		t.Fatalf("idle wait = %v, want %v", wait, scheduleWaitMax)
	}

	seedTask(t, s, schedule.Task{
		ID:        "fast",
		Prompt:    "poll",
		Schedule:  "every 15s",
		Status:    schedule.StatusActive,
		CreatedAt: time.Now(),
	})

	wait := s.nextScheduleWait()
	// 15s plus at most 10% jitter, minus the instant since creation.
	if wait < scheduleWaitMin || wait > 17*time.Second {
		t.Fatalf("wait = %v, want roughly the 15s interval", wait)
	}

	seedTask(t, s, schedule.Task{
		ID:        "overdue",
		Prompt:    "poll",
		Schedule:  "every 15s",
		Status:    schedule.StatusActive,
		CreatedAt: time.Now().Add(-time.Hour),
	})

	if wait := s.nextScheduleWait(); wait != scheduleWaitMin {
		t.Fatalf("overdue wait = %v, want the %v floor", wait, scheduleWaitMin)
	}
}

func TestFireScheduleSeqIsUnique(t *testing.T) {
	s := newScheduleSession(t)
	defer s.tasks.Close()

	job := schedule.Task{
		ID:        "repeat",
		Prompt:    "poll",
		Schedule:  "every 1h",
		Status:    schedule.StatusActive,
		CreatedAt: time.Now().Add(-2 * time.Hour),
	}
	seedTask(t, s, job)

	s.fireSchedule(context.Background(), job)
	first, _ := waitEvent(t, s)
	s.fireSchedule(context.Background(), job)
	second, _ := waitEvent(t, s)

	if first.Seq == second.Seq {
		t.Fatalf("both fires used seq %d; UIs would drop the second as a duplicate", first.Seq)
	}
}
