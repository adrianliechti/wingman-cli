package schedule

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

const tasksFile = "tasks.yaml"

type Task struct {
	ID        string     `yaml:"id"`
	Prompt    string     `yaml:"prompt"`
	Schedule  string     `yaml:"schedule"`
	Status    string     `yaml:"status"`
	CreatedAt time.Time  `yaml:"created_at"`
	LastRun   *time.Time `yaml:"last_run,omitempty"`
}

type taskFile struct {
	Tasks []Task `yaml:"tasks"`
}

func Tools(agentDir string) []tool.Tool {
	return []tool.Tool{
		{
			Name: "schedule_task",
			Description: strings.Join([]string{
				"Schedule a recurring or one-time task.",
				"",
				"Schedule formats:",
				"- Interval: \"every 15m\", \"every 2h\", \"every 24h\"",
				"- Cron: \"0 9 * * 1-5\" (weekdays at 9am), \"*/15 * * * *\" (every 15 min)",
				"- One-time: ISO 8601 timestamp (e.g. \"2026-04-15T09:00:00Z\")",
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
						"description": "Schedule expression: \"every 15m\", cron expression, or ISO 8601 timestamp.",
					},
				},
				"required": []string{"prompt", "schedule"},
			},
			Execute: func(ctx context.Context, args map[string]any) (string, error) {
				prompt, _ := args["prompt"].(string)
				sched, _ := args["schedule"].(string)

				if prompt == "" {
					return "", fmt.Errorf("prompt is required")
				}

				if sched == "" {
					return "", fmt.Errorf("schedule is required")
				}

				if err := validateSchedule(sched); err != nil {
					return "", err
				}

				task := Task{
					ID:        newID(),
					Prompt:    prompt,
					Schedule:  sched,
					Status:    "active",
					CreatedAt: time.Now().UTC(),
				}

				tasks := LoadTasks(agentDir)
				tasks = append(tasks, task)

				if err := SaveTasks(agentDir, tasks); err != nil {
					return "", err
				}

				return fmt.Sprintf("Task %s scheduled (%s): %s", task.ID, sched, prompt), nil
			},
		},
		{
			Name:        "list_tasks",
			Description: "List all scheduled tasks with their status and next run time.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			Execute: func(ctx context.Context, args map[string]any) (string, error) {
				tasks := LoadTasks(agentDir)

				if len(tasks) == 0 {
					return "No tasks scheduled.", nil
				}

				now := time.Now()
				var b strings.Builder

				for _, t := range tasks {
					next := NextRun(t, now)
					nextStr := "n/a"
					if !next.IsZero() {
						nextStr = next.Format(time.RFC3339)
					}

					fmt.Fprintf(&b, "- [%s] %s (schedule: %s, status: %s, next: %s)\n",
						t.ID, t.Prompt, t.Schedule, t.Status, nextStr)
				}

				return b.String(), nil
			},
		},
		{
			Name:        "pause_task",
			Description: "Pause a scheduled task by ID.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Task ID to pause.",
					},
				},
				"required": []string{"id"},
			},
			Execute: func(ctx context.Context, args map[string]any) (string, error) {
				id, _ := args["id"].(string)
				return updateStatus(agentDir, id, "paused")
			},
		},
		{
			Name:        "resume_task",
			Description: "Resume a paused task by ID.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Task ID to resume.",
					},
				},
				"required": []string{"id"},
			},
			Execute: func(ctx context.Context, args map[string]any) (string, error) {
				id, _ := args["id"].(string)
				return updateStatus(agentDir, id, "active")
			},
		},
		{
			Name:        "remove_task",
			Description: "Remove a scheduled task by ID.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Task ID to remove.",
					},
				},
				"required": []string{"id"},
			},
			Execute: func(ctx context.Context, args map[string]any) (string, error) {
				id, _ := args["id"].(string)
				if id == "" {
					return "", fmt.Errorf("id is required")
				}

				tasks := LoadTasks(agentDir)
				var kept []Task

				for _, t := range tasks {
					if t.ID != id {
						kept = append(kept, t)
					}
				}

				if len(kept) == len(tasks) {
					return "", fmt.Errorf("task %s not found", id)
				}

				if err := SaveTasks(agentDir, kept); err != nil {
					return "", err
				}

				return fmt.Sprintf("Task %s removed.", id), nil
			},
		},
		{
			Name:        "run_task",
			Description: "Run a scheduled task immediately, regardless of its schedule. Useful for testing.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Task ID to run now.",
					},
				},
				"required": []string{"id"},
			},
			Execute: func(ctx context.Context, args map[string]any) (string, error) {
				id, _ := args["id"].(string)
				if id == "" {
					return "", fmt.Errorf("id is required")
				}

				tasks := LoadTasks(agentDir)

				for i := range tasks {
					if tasks[i].ID == id {
						now := time.Now()
						tasks[i].LastRun = &now
						SaveTasks(agentDir, tasks)

						return fmt.Sprintf("Task triggered. Execute now:\n\n%s", tasks[i].Prompt), nil
					}
				}

				return "", fmt.Errorf("task %s not found", id)
			},
		},
	}
}

// LoadTasks reads tasks from the agent's tasks.yaml file.
func LoadTasks(agentDir string) []Task {
	data, err := os.ReadFile(filepath.Join(agentDir, tasksFile))
	if err != nil {
		return nil
	}

	var f taskFile
	yaml.Unmarshal(data, &f)

	return f.Tasks
}

// SaveTasks writes tasks to the agent's tasks.yaml file.
func SaveTasks(agentDir string, tasks []Task) error {
	out, err := yaml.Marshal(taskFile{Tasks: tasks})
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(agentDir, tasksFile), out, 0644)
}

// NextRun computes when a task should next run.
func NextRun(t Task, now time.Time) time.Time {
	if t.Status != "active" {
		return time.Time{}
	}

	sched := t.Schedule

	// Interval: "every 15m"
	if strings.HasPrefix(sched, "every ") {
		d, err := time.ParseDuration(strings.TrimPrefix(sched, "every "))
		if err != nil {
			return time.Time{}
		}

		if t.LastRun == nil {
			return now
		}

		return t.LastRun.Add(d)
	}

	// One-time: ISO 8601
	if ts, err := time.Parse(time.RFC3339, sched); err == nil {
		if t.LastRun != nil {
			return time.Time{} // already ran
		}

		return ts
	}

	// Cron expression
	parser := cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

	s, err := parser.Parse(sched)
	if err != nil {
		return time.Time{}
	}

	anchor := now
	if t.LastRun != nil {
		anchor = *t.LastRun
	}

	return s.Next(anchor)
}

// IsDue returns true if the task should run now.
func IsDue(t Task, now time.Time) bool {
	next := NextRun(t, now)
	return !next.IsZero() && !next.After(now)
}

func validateSchedule(sched string) error {
	// Interval
	if strings.HasPrefix(sched, "every ") {
		_, err := time.ParseDuration(strings.TrimPrefix(sched, "every "))
		if err != nil {
			return fmt.Errorf("invalid interval %q: %w", sched, err)
		}

		return nil
	}

	// ISO 8601 timestamp
	if _, err := time.Parse(time.RFC3339, sched); err == nil {
		return nil
	}

	// Cron expression
	parser := cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

	if _, err := parser.Parse(sched); err != nil {
		return fmt.Errorf("invalid schedule %q: must be \"every <duration>\", a cron expression, or an ISO 8601 timestamp", sched)
	}

	return nil
}

func updateStatus(agentDir, id, status string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("id is required")
	}

	tasks := LoadTasks(agentDir)
	found := false

	for i := range tasks {
		if tasks[i].ID == id {
			tasks[i].Status = status
			found = true
			break
		}
	}

	if !found {
		return "", fmt.Errorf("task %s not found", id)
	}

	if err := SaveTasks(agentDir, tasks); err != nil {
		return "", err
	}

	return fmt.Sprintf("Task %s %s.", id, status), nil
}

func newID() string {
	return uuid.NewString()
}
