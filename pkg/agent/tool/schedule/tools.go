package schedule

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

var idParams = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"id": map[string]any{
			"type":        "string",
			"description": "Task ID.",
		},
	},
	"required":             []string{"id"},
	"additionalProperties": false,
}

func Tools(store Store) []tool.Tool {
	return []tool.Tool{
		{
			Name:   "schedule_task",
			Effect: tool.StaticEffect(tool.EffectMutates),
			Description: strings.Join([]string{
				"Schedule a recurring or one-time task. When it comes due you are woken with its prompt.",
				"",
				"Schedule formats (always local time, never converted):",
				"- Interval: \"every 15m\", \"every 2h\", \"every 24h\" — counted from the last run, not aligned to the clock. Intervals can go down to 10s, but every fire wakes you and costs a model turn: for tight polling, pair a short interval with a pre-check script (or watch the resource instead) so most fires skip silently.",
				"- Cron: standard 5 fields, \"0 9 * * 1-5\" (weekdays at 9am), \"*/15 * * * *\" (every 15 min)",
				"- One-time: a timestamp, local (\"2026-04-15T09:00\") or RFC 3339 with offset. It is removed once it has run.",
				"",
				"When the requested time is approximate, avoid the :00 and :30 marks — schedule \"around 9\" as \"57 8 * * *\" rather than \"0 9 * * *\".",
				"",
				"For frequent monitoring tasks, add a pre-check script so you are only woken when something actually changed. To react the moment something happens rather than on a fixed schedule, watch it instead of scheduling a poll.",
			}, "\n"),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{
						"type":        "string",
						"description": "What the task should do when it runs.",
					},
					"schedule": map[string]any{
						"type":        "string",
						"description": "Schedule expression: \"every 15m\", cron expression, or timestamp.",
					},
					"script": map[string]any{
						"type":        "string",
						"description": "Optional pre-check script that runs before each invocation, using the same interpreter as the shell tool. Print {\"wake\": false} to skip the run silently; any other output (or a failure) wakes you with the output attached. Test it with the shell tool first.",
					},
				},
				"required":             []string{"prompt", "schedule"},
				"additionalProperties": false,
			},
			Execute: func(ctx context.Context, args map[string]any) (string, error) {
				prompt, _ := args["prompt"].(string)
				sched, _ := args["schedule"].(string)
				script, _ := args["script"].(string)

				task, err := NewTask(prompt, sched)
				if err != nil {
					return "", err
				}

				task.Script = strings.TrimSpace(script)

				err = store.Mutate(func(tasks []Task) ([]Task, error) {
					return append(tasks, task), nil
				})
				if err != nil {
					return "", err
				}

				now := time.Now()
				return fmt.Sprintf("Task %s scheduled (%s), next run %s: %s", task.ID, task.Schedule, formatNext(NextRun(task, now), now), task.Prompt), nil
			},
		},
		{
			Name:        "list_tasks",
			Description: "List all scheduled tasks with their status and next run time.",
			Effect:      tool.StaticEffect(tool.EffectReadOnly),
			Parameters: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": false,
			},
			Execute: func(ctx context.Context, args map[string]any) (string, error) {
				tasks, err := store.List()
				if err != nil {
					return "", err
				}

				if len(tasks) == 0 {
					return "No tasks scheduled.", nil
				}

				now := time.Now()
				var b strings.Builder

				for _, t := range tasks {
					fmt.Fprintf(&b, "- [%s] %s (schedule: %s, status: %s, next: %s",
						t.ID, t.Prompt, t.Schedule, t.Status, formatNext(NextRun(t, now), now))
					if t.Script != "" {
						b.WriteString(", pre-check script")
					}
					if t.LastRun != nil {
						fmt.Fprintf(&b, ", last run: %s", t.LastRun.Local().Format(time.RFC3339))
					}
					if t.Failures > 0 {
						fmt.Fprintf(&b, ", consecutive failures: %d (retrying with backoff)", t.Failures)
					}
					b.WriteString(")\n")
				}

				return b.String(), nil
			},
		},
		{
			Name:        "pause_task",
			Description: "Pause a scheduled task by ID.",
			Effect:      tool.StaticEffect(tool.EffectMutates),
			Parameters:  idParams,
			Execute: func(ctx context.Context, args map[string]any) (string, error) {
				return updateStatus(store, args, StatusPaused)
			},
		},
		{
			Name:        "resume_task",
			Description: "Resume a paused task by ID.",
			Effect:      tool.StaticEffect(tool.EffectMutates),
			Parameters:  idParams,
			Execute: func(ctx context.Context, args map[string]any) (string, error) {
				return updateStatus(store, args, StatusActive)
			},
		},
		{
			Name:        "remove_task",
			Description: "Remove a scheduled task by ID.",
			Effect:      tool.StaticEffect(tool.EffectMutates),
			Parameters:  idParams,
			Execute: func(ctx context.Context, args map[string]any) (string, error) {
				id, _ := args["id"].(string)

				var removed string
				err := store.Mutate(func(tasks []Task) ([]Task, error) {
					i, err := Find(tasks, id)
					if err != nil {
						return nil, err
					}
					removed = tasks[i].ID
					return append(tasks[:i:i], tasks[i+1:]...), nil
				})
				if err != nil {
					return "", err
				}

				return fmt.Sprintf("Task %s removed.", removed), nil
			},
		},
		{
			Name:        "run_task",
			Description: "Run a scheduled task immediately, regardless of its schedule. Useful for testing.",
			Effect:      tool.StaticEffect(tool.EffectMutates),
			Parameters:  idParams,
			Execute: func(ctx context.Context, args map[string]any) (string, error) {
				id, _ := args["id"].(string)

				var prompt string
				err := store.Mutate(func(tasks []Task) ([]Task, error) {
					i, err := Find(tasks, id)
					if err != nil {
						return nil, err
					}
					now := time.Now()
					tasks[i].LastRun = &now
					prompt = tasks[i].Prompt
					return tasks, nil
				})
				if err != nil {
					return "", err
				}

				return fmt.Sprintf("Task triggered. Execute now:\n\n%s", prompt), nil
			},
		},
	}
}

func updateStatus(store Store, args map[string]any, status string) (string, error) {
	id, _ := args["id"].(string)

	var updated string
	err := store.Mutate(func(tasks []Task) ([]Task, error) {
		i, err := Find(tasks, id)
		if err != nil {
			return nil, err
		}
		tasks[i].Status = status
		updated = tasks[i].ID
		return tasks, nil
	})
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("Task %s %s.", updated, status), nil
}

func formatNext(next, now time.Time) string {
	if next.IsZero() {
		return "n/a"
	}
	return fmt.Sprintf("%s (%s)", next.Local().Format(time.RFC3339), Relative(next, now))
}

// Relative renders a next-run time the way every surface shows it: "overdue",
// "in 42m", "in 3h5m".
func Relative(next, now time.Time) string {
	if next.IsZero() {
		return "never"
	}

	d := next.Sub(now)
	switch {
	case d < 0:
		return "overdue"
	case d < time.Minute:
		return "in <1m"
	case d < time.Hour:
		return fmt.Sprintf("in %dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("in %dh%dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("in %dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}
