package agent

import (
	"context"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/task"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/schedule"
)

const (
	// scheduleWaitMax bounds the idle sleep so drift in long waits self-heals.
	scheduleWaitMax = time.Minute
	// scheduleWaitMin keeps an overdue task from busy-spinning the loop.
	scheduleWaitMin = time.Second
)

// runSchedules fires this session's due tasks into its own conversation. The
// schedules are persisted with the session, and a task that comes due while a
// turn is running is queued by the turn manager rather than dropped. The loop
// sleeps until the earliest next run instead of a
// fixed tick, so sub-minute intervals fire on time; store changes wake it to
// re-plan.
func (s *sessionState) runSchedules() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		<-s.watchStop
		cancel()
	}()

	for {
		timer := time.NewTimer(s.nextScheduleWait())
		select {
		case <-s.watchStop:
			timer.Stop()
			return
		case <-s.schedules.Changed():
			timer.Stop()
		case <-timer.C:
			s.fireDueSchedules(ctx)
		}
	}
}

func (s *sessionState) nextScheduleWait() time.Duration {
	tasks, err := s.schedules.List()
	if err != nil {
		return scheduleWaitMax
	}

	now := time.Now()
	wait := scheduleWaitMax

	for _, t := range tasks {
		if next := schedule.NextAttempt(t, now); !next.IsZero() {
			wait = min(wait, next.Sub(now))
		}
	}

	return max(wait, scheduleWaitMin)
}

func (s *sessionState) fireDueSchedules(ctx context.Context) {
	tasks, err := s.schedules.List()
	if err != nil || len(tasks) == 0 {
		return
	}

	now := time.Now()

	for _, t := range tasks {
		if ctx.Err() != nil {
			return
		}
		if !schedule.IsDue(t, now) {
			continue
		}
		s.fireSchedule(ctx, t)
	}
}

func (s *sessionState) fireSchedule(ctx context.Context, t schedule.Task) {
	wake := true
	gateOutput := ""
	var gateErr error

	if t.Script != "" {
		wake, gateOutput, gateErr = schedule.RunGate(ctx, s.parent.workspace.RootPath, t.Script)
	}

	if ctx.Err() != nil {
		return
	}

	now := time.Now()

	var fireSeq uint64
	if err := s.schedules.Mutate(func(tasks []schedule.Task) ([]schedule.Task, error) {
		var kept []schedule.Task
		for i := range tasks {
			if tasks[i].ID == t.ID {
				tasks[i].FireSeq++
				fireSeq = tasks[i].FireSeq
				if schedule.IsOneTime(tasks[i].Schedule) {
					continue
				}
				tasks[i].LastRun = &now
				if gateErr != nil {
					tasks[i].Failures++
					tasks[i].LastAttempt = &now
				} else {
					tasks[i].Failures = 0
					tasks[i].LastAttempt = nil
				}
			}
			kept = append(kept, tasks[i])
		}
		return kept, nil
	}); err != nil || fireSeq == 0 {
		return
	}

	if !wake {
		return
	}

	prompt := t.Prompt
	if gateOutput != "" {
		prompt += "\n\nPre-check script output:\n\n" + gateOutput
	}

	s.tasks.Publish(task.Event{
		ID:          t.ID,
		Kind:        task.KindSchedule,
		AgentType:   "schedule",
		Description: t.Schedule,
		Result:      prompt,
		Status:      task.StatusDone,
		// Seq distinguishes successive fires of the same task: UIs derive the
		// turn input id from it, and a repeated id is dropped as a duplicate.
		Seq: int(fireSeq),
	})
}
