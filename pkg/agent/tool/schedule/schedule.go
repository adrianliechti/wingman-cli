package schedule

import (
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

const (
	StatusActive = "active"
	StatusPaused = "paused"

	// MinInterval is a floor against runaway wake loops, not a technical
	// limit: every fire is a model turn, so tighter polling belongs in a
	// pre-check script or a backgrounded watcher, not the schedule itself.
	// Checked when a task is created, not when an existing one is evaluated.
	MinInterval = 10 * time.Second

	MaxJitter = 15 * time.Minute
)

type Task struct {
	ID        string     `yaml:"id"`
	Prompt    string     `yaml:"prompt"`
	Schedule  string     `yaml:"schedule"`
	Script    string     `yaml:"script,omitempty"`
	Status    string     `yaml:"status"`
	CreatedAt time.Time  `yaml:"created_at"`
	LastRun   *time.Time `yaml:"last_run,omitempty"`

	Failures    int        `yaml:"failures,omitempty"`
	LastAttempt *time.Time `yaml:"last_attempt,omitempty"`
}

func NewTask(prompt, sched string) (Task, error) {
	prompt = strings.TrimSpace(prompt)
	sched = strings.TrimSpace(sched)

	if prompt == "" {
		return Task{}, fmt.Errorf("prompt is required")
	}
	if sched == "" {
		return Task{}, fmt.Errorf("schedule is required")
	}

	p, err := parseSchedule(sched)
	if err != nil {
		return Task{}, err
	}
	// Enforced at creation only: existing task files may hold shorter intervals
	// from before this floor, and those must keep parsing (they effectively run
	// at tick granularity) rather than silently never run again.
	if p.interval > 0 && p.interval < MinInterval {
		return Task{}, fmt.Errorf("invalid interval %q: the shortest supported interval is %s", sched, MinInterval)
	}

	return Task{
		ID:        uuid.NewString()[:8],
		Prompt:    prompt,
		Schedule:  sched,
		Status:    StatusActive,
		CreatedAt: time.Now().UTC(),
	}, nil
}

// Find resolves a task reference the model typed back: an exact id, or an
// unambiguous prefix of one. Ambiguous prefixes are an error rather than a
// coin flip on which task gets paused or removed.
func Find(tasks []Task, id string) (int, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return -1, fmt.Errorf("id is required")
	}

	for i := range tasks {
		if tasks[i].ID == id {
			return i, nil
		}
	}

	match := -1
	for i := range tasks {
		if !strings.HasPrefix(tasks[i].ID, id) {
			continue
		}
		if match >= 0 {
			return -1, fmt.Errorf("task id %s is ambiguous", id)
		}
		match = i
	}

	if match < 0 {
		return -1, fmt.Errorf("task %s not found", id)
	}

	return match, nil
}

var cronParser = cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

type parsedSchedule struct {
	interval time.Duration
	once     time.Time
	cron     cron.Schedule
}

func parseSchedule(sched string) (parsedSchedule, error) {
	if rest, ok := strings.CutPrefix(sched, "every "); ok {
		d, err := time.ParseDuration(rest)
		if err != nil {
			return parsedSchedule{}, fmt.Errorf("invalid interval %q: %w", sched, err)
		}
		if d <= 0 {
			return parsedSchedule{}, fmt.Errorf("invalid interval %q: duration must be positive", sched)
		}
		return parsedSchedule{interval: d}, nil
	}

	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04"} {
		if ts, err := time.ParseInLocation(layout, sched, time.Local); err == nil {
			return parsedSchedule{once: ts}, nil
		}
	}

	if s, err := cronParser.Parse(sched); err == nil {
		return parsedSchedule{cron: s}, nil
	}

	return parsedSchedule{}, fmt.Errorf("invalid schedule %q: must be \"every <duration>\", a cron expression, or a timestamp (\"2026-04-15T09:00\" local or RFC 3339)", sched)
}

func NextRun(t Task, now time.Time) time.Time {
	if t.Status != StatusActive {
		return time.Time{}
	}

	p, err := parseSchedule(t.Schedule)
	if err != nil {
		return time.Time{}
	}

	switch {
	case p.interval > 0:
		return t.anchor().Add(p.interval + jitter(t.ID, p.interval))
	case !p.once.IsZero():
		if t.LastRun != nil {
			return time.Time{}
		}
		return p.once
	default:
		return p.cron.Next(t.anchor())
	}
}

func (t Task) anchor() time.Time {
	if t.LastRun != nil {
		return *t.LastRun
	}
	return t.CreatedAt
}

// jitter spreads interval tasks deterministically. An interval is approximate
// by construction ("every 1h", not "at :00"), so shifting it is safe — while
// cron expressions and timestamps name an exact wall clock the user asked for
// and are never shifted. Without this, tasks created in the same turn come due
// on the same tick forever.
func jitter(id string, interval time.Duration) time.Duration {
	span := min(interval/10, MaxJitter)
	if span <= 0 {
		return 0
	}

	h := fnv.New64a()
	h.Write([]byte(id))

	return time.Duration(h.Sum64() % uint64(span))
}

func IsDue(t Task, now time.Time) bool {
	if t.Failures > 0 && t.LastAttempt != nil {
		backoff := min(time.Hour, time.Duration(1<<min(t.Failures, 6))*time.Minute)
		if now.Before(t.LastAttempt.Add(backoff)) {
			return false
		}
	}

	next := NextRun(t, now)
	return !next.IsZero() && !next.After(now)
}

func OnceTime(sched string) (time.Time, bool) {
	p, err := parseSchedule(sched)
	if err != nil || p.once.IsZero() {
		return time.Time{}, false
	}
	return p.once, true
}

func IsOneTime(sched string) bool {
	_, ok := OnceTime(sched)
	return ok
}
