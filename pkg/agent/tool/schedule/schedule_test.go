package schedule

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func TestScheduleTaskRejectsNonPositiveInterval(t *testing.T) {
	scheduleTool := findTool(t, "schedule_task")

	_, err := scheduleTool.Execute(context.Background(), map[string]any{
		"prompt":   "run",
		"schedule": "every 0s",
	})
	if err == nil || !strings.Contains(err.Error(), "duration must be positive") {
		t.Fatalf("expected positive duration error, got: %v", err)
	}
}

func TestScheduleTaskRejectsBelowFloorInterval(t *testing.T) {
	scheduleTool := findTool(t, "schedule_task")

	_, err := scheduleTool.Execute(context.Background(), map[string]any{
		"prompt":   "run",
		"schedule": "every 5s",
	})
	if err == nil || !strings.Contains(err.Error(), "shortest supported interval") {
		t.Fatalf("expected minimum interval error, got: %v", err)
	}

	if _, err := scheduleTool.Execute(context.Background(), map[string]any{
		"prompt":   "run",
		"schedule": "every 15s",
	}); err != nil {
		t.Fatalf("a 15s interval should be accepted, got: %v", err)
	}
}

func TestBelowFloorIntervalStillEvaluates(t *testing.T) {
	// Task files may hold intervals below the creation-time floor (written
	// before the floor, or by another tool); those must keep evaluating
	// instead of silently going dead.
	created := time.Date(2026, 6, 12, 8, 0, 0, 0, time.UTC)
	task := Task{
		ID:        "legacy",
		Prompt:    "poll",
		Schedule:  "every 5s",
		Status:    StatusActive,
		CreatedAt: created,
	}

	if next := NextRun(task, created); next.IsZero() {
		t.Fatal("below-floor interval must still yield a next run")
	}
	if !IsDue(task, created.Add(time.Minute)) {
		t.Fatal("below-floor task should be due after its interval elapsed")
	}
}

func TestScheduleToolsExposeEffects(t *testing.T) {
	tests := map[string]tool.Effect{
		"schedule_task": tool.EffectMutates,
		"list_tasks":    tool.EffectReadOnly,
		"pause_task":    tool.EffectMutates,
		"resume_task":   tool.EffectMutates,
		"remove_task":   tool.EffectMutates,
	}

	for _, tl := range Tools(NewMemoryStore()) {
		want, ok := tests[tl.Name]
		if !ok {
			t.Fatalf("unexpected tool %q", tl.Name)
		}
		if tl.Effect == nil {
			t.Fatalf("%s effect is nil", tl.Name)
		}
		if got := tl.Effect(nil); got != want {
			t.Fatalf("%s effect = %v, want %v", tl.Name, got, want)
		}
	}
}

func TestToolsResolveIDPrefix(t *testing.T) {
	store := NewMemoryStore()
	tools := toolSet(t, store)

	if _, err := tools["schedule_task"].Execute(context.Background(), map[string]any{
		"prompt":   "check the build",
		"schedule": "every 1h",
	}); err != nil {
		t.Fatalf("schedule_task failed: %v", err)
	}

	tasks, _ := store.List()
	if len(tasks) != 1 {
		t.Fatalf("tasks = %#v, want one", tasks)
	}
	id := tasks[0].ID
	if len(id) != 8 {
		t.Fatalf("id = %q, want a short id", id)
	}

	if _, err := tools["pause_task"].Execute(context.Background(), map[string]any{"id": id[:4]}); err != nil {
		t.Fatalf("pause_task by prefix failed: %v", err)
	}
	if tasks, _ = store.List(); tasks[0].Status != StatusPaused {
		t.Fatalf("status = %q, want paused", tasks[0].Status)
	}
	if next := NextRun(tasks[0], time.Now()); !next.IsZero() {
		t.Fatalf("paused task next run = %v, want none", next)
	}

	if _, err := tools["remove_task"].Execute(context.Background(), map[string]any{"id": id}); err != nil {
		t.Fatalf("remove_task failed: %v", err)
	}
	if tasks, _ = store.List(); len(tasks) != 0 {
		t.Fatalf("tasks = %#v, want empty", tasks)
	}
}

func TestFindRejectsAmbiguousPrefix(t *testing.T) {
	tasks := []Task{{ID: "abc12345"}, {ID: "abc98765"}}

	if _, err := Find(tasks, "abc"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous error, got %v", err)
	}
	if i, err := Find(tasks, "abc98765"); err != nil || i != 1 {
		t.Fatalf("Find exact = %d, %v, want 1, nil", i, err)
	}
	if _, err := Find(tasks, "zz"); err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestIntervalTaskAnchorsAtCreation(t *testing.T) {
	created := time.Date(2026, 6, 12, 8, 0, 0, 0, time.UTC)
	task := Task{
		ID:        "int-1",
		Prompt:    "poll",
		Schedule:  "every 1h",
		Status:    StatusActive,
		CreatedAt: created,
	}

	if IsDue(task, created) {
		t.Fatal("a fresh interval task should not fire immediately")
	}
	if IsDue(task, created.Add(50*time.Minute)) {
		t.Fatal("interval task fired before its first interval elapsed")
	}
	if !IsDue(task, created.Add(70*time.Minute)) {
		t.Fatal("interval task should be due after the interval plus jitter")
	}

	lastRun := created.Add(70 * time.Minute)
	task.LastRun = &lastRun
	if IsDue(task, lastRun.Add(30*time.Minute)) {
		t.Fatal("interval task re-fired before the next interval elapsed")
	}
}

func TestJitterIsDeterministicAndBounded(t *testing.T) {
	for _, interval := range []time.Duration{time.Minute, time.Hour, 24 * time.Hour, 30 * 24 * time.Hour} {
		got := jitter("some-id", interval)
		if got != jitter("some-id", interval) {
			t.Fatalf("jitter is not deterministic for %s", interval)
		}
		if got < 0 || got >= min(interval/10, MaxJitter) {
			t.Fatalf("jitter(%s) = %s, out of bounds", interval, got)
		}
	}

	if jitter("a", time.Hour) == jitter("b", time.Hour) {
		t.Fatal("jitter should differ per task id")
	}
}

func TestCronScheduleIsNotJittered(t *testing.T) {
	created := time.Date(2026, 6, 12, 8, 0, 0, 0, time.UTC)
	task := Task{
		ID:        "cron-jitter",
		Schedule:  "0 9 * * *",
		Status:    StatusActive,
		CreatedAt: created,
	}

	want := time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)
	if got := NextRun(task, created); !got.Equal(want) {
		t.Fatalf("NextRun = %v, want exactly %v", got, want)
	}
}

func TestCronTaskFiresWithoutLastRun(t *testing.T) {
	created := time.Date(2026, 6, 12, 8, 0, 0, 0, time.UTC)
	task := Task{
		ID:        "cron-1",
		Prompt:    "daily standup",
		Schedule:  "0 9 * * *",
		Status:    StatusActive,
		CreatedAt: created,
	}

	if IsDue(task, created.Add(30*time.Minute)) {
		t.Fatal("task should not be due before its first cron slot")
	}
	if !IsDue(task, created.Add(2*time.Hour)) {
		t.Fatal("task should be due after its first cron slot passed")
	}

	lastRun := time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)
	task.LastRun = &lastRun
	if IsDue(task, lastRun.Add(2*time.Hour)) {
		t.Fatal("task should not be due again before the next cron slot")
	}
	if !IsDue(task, lastRun.Add(25*time.Hour)) {
		t.Fatal("task should be due after the next cron slot passed")
	}
}

func TestOneTimeTaskRunsOnlyOnce(t *testing.T) {
	at := time.Now().Add(-time.Minute)
	task := Task{
		ID:        "once-1",
		Schedule:  at.Format("2006-01-02T15:04:05"),
		Status:    StatusActive,
		CreatedAt: at.Add(-time.Hour),
	}

	if !IsOneTime(task.Schedule) {
		t.Fatalf("%q should parse as a one-time schedule", task.Schedule)
	}
	if !IsDue(task, time.Now()) {
		t.Fatal("one-time task should be due once its timestamp passed")
	}

	ran := time.Now()
	task.LastRun = &ran
	if IsDue(task, time.Now()) {
		t.Fatal("one-time task should not fire again after it ran")
	}
}

func TestParseScheduleAcceptsLocalTimestamp(t *testing.T) {
	for _, sched := range []string{"2026-04-15T09:00", "2026-04-15T09:00:00"} {
		p, err := parseSchedule(sched)
		if err != nil {
			t.Fatalf("parseSchedule(%q): %v", sched, err)
		}
		want := time.Date(2026, 4, 15, 9, 0, 0, 0, time.Local)
		if !p.once.Equal(want) {
			t.Fatalf("parseSchedule(%q) = %v, want %v", sched, p.once, want)
		}
	}
}

func TestRunGate(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	if wake, _, err := RunGate(ctx, dir, `echo '{"wake": false}'`); wake || err != nil {
		t.Fatalf("wake=false output should skip the run, got wake=%v err=%v", wake, err)
	}
	if wake, _, err := RunGate(ctx, dir, `echo '{"wake": true, "data": [1]}'`); !wake || err != nil {
		t.Fatalf("wake=true output should wake the agent, got wake=%v err=%v", wake, err)
	}
	if wake, out, _ := RunGate(ctx, dir, `echo checking; echo '{"wake": false}'`); wake || !strings.Contains(out, "checking") {
		t.Fatalf("trailing JSON line should be honored, got wake=%v out=%q", wake, out)
	}
	if wake, _, err := RunGate(ctx, dir, `echo not-json`); !wake || err != nil {
		t.Fatalf("non-JSON output should fail open without an error, got wake=%v err=%v", wake, err)
	}
	if wake, out, err := RunGate(ctx, dir, `exit 3`); !wake || err == nil || !strings.Contains(out, "failed") {
		t.Fatalf("script failure should fail open with a note and an error, got wake=%v out=%q err=%v", wake, out, err)
	}
}

func TestFailureBackoffDelaysRetries(t *testing.T) {
	last := time.Date(2026, 6, 12, 8, 0, 0, 0, time.UTC)
	task := Task{
		ID:          "flaky",
		Schedule:    "every 15s",
		Status:      StatusActive,
		CreatedAt:   last.Add(-time.Hour),
		LastRun:     &last,
		Failures:    1,
		LastAttempt: &last,
	}

	if IsDue(task, last.Add(time.Minute)) {
		t.Fatal("a failed task should back off, not retry on its own interval")
	}
	if !IsDue(task, last.Add(3*time.Minute)) {
		t.Fatal("a failed task should retry once the backoff elapsed")
	}

	task.Failures = 10
	if got := NextAttempt(task, last.Add(time.Minute)); !got.Equal(last.Add(time.Hour)) {
		t.Fatalf("NextAttempt = %v, want the backoff capped at %v", got, last.Add(time.Hour))
	}

	task.Failures = 0
	task.LastAttempt = nil
	if !IsDue(task, last.Add(time.Minute)) {
		t.Fatal("a recovered task should follow its schedule again")
	}
}

func toolSet(t *testing.T, store Store) map[string]tool.Tool {
	t.Helper()
	out := map[string]tool.Tool{}
	for _, tl := range Tools(store) {
		out[tl.Name] = tl
	}
	return out
}

func findTool(t *testing.T, name string) tool.Tool {
	t.Helper()
	tl, ok := toolSet(t, NewMemoryStore())[name]
	if !ok {
		t.Fatalf("tool %q not found", name)
	}
	return tl
}
