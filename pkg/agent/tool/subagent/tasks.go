package subagent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/task"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func taskTools(tasks *task.Registry) []tool.Tool {
	return []tool.Tool{
		{
			Name:        "task_output",
			Description: "Check on this session's agents, background and synchronous. Without `id`, lists them all with their status. With `id`, returns that agent's status and, once finished, its full result. Results also arrive automatically as task notifications — use this to peek early or to re-read a result. Never call it in a loop to wait: keep working, the notification arrives on its own.",
			Effect:      tool.StaticEffect(tool.EffectReadOnly),
			Hidden:      true,

			Parameters: map[string]any{
				"type": "object",

				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "Task id of a background agent. Omit to list all."},
				},

				"additionalProperties": false,
			},

			Execute: func(_ context.Context, args map[string]any) (string, error) {
				id, _ := args["id"].(string)
				id = strings.TrimSpace(id)

				if id == "" {
					return formatTaskList(tasks), nil
				}

				t := tasks.Get(id)
				if t == nil {
					return "", fmt.Errorf("no background agent with id %s", id)
				}

				if t.Status() == task.StatusRunning {
					out := fmt.Sprintf("%s — still running (%s elapsed).", formatTaskLine(t), formatTaskElapsed(t))
					if activity := t.Activity(); activity != "" {
						out += fmt.Sprintf(" Currently: %s.", activity)
					}
					return out + " The result will arrive as a task notification.", nil
				}
				return fmt.Sprintf("%s\n\n%s", formatTaskLine(t), t.Result()), nil
			},
		},
		{
			Name:        "task_send",
			Description: "Send a follow-up message to a finished agent — background or synchronous; every agent result carries its task id. It resumes in the background with its full prior context — no re-briefing needed — and the reply arrives as a task notification. Use this instead of launching a fresh agent when the question builds on work an agent already did. Never use it to have an agent confirm its own findings — verification needs a fresh agent without the finder's context.",
			Effect:      tool.StaticEffect(tool.EffectMutates),

			Parameters: map[string]any{
				"type": "object",

				"properties": map[string]any{
					"id":      map[string]any{"type": "string", "description": "Task id of the background agent to continue."},
					"message": map[string]any{"type": "string", "description": "The follow-up task or question. The agent keeps its earlier context; only state what is new."},
				},

				"required":             []string{"id", "message"},
				"additionalProperties": false,
			},

			Execute: func(_ context.Context, args map[string]any) (string, error) {
				id, _ := args["id"].(string)
				id = strings.TrimSpace(id)
				if id == "" {
					return "", fmt.Errorf("id is required")
				}
				message, _ := args["message"].(string)
				if strings.TrimSpace(message) == "" {
					return "", fmt.Errorf("message is required")
				}

				t := tasks.Get(id)
				if t == nil {
					return "", fmt.Errorf("no background agent with id %s", id)
				}
				if err := t.Resume(message); err != nil {
					return "", err
				}
				return fmt.Sprintf("Follow-up sent to background agent %s; it resumed with its prior context. The reply arrives as a task notification — never assume or invent it.", id), nil
			},
		},
		{
			Name:        "task_stop",
			Description: "Stop a running background agent. Its partial output is discarded; a stopped-task notification confirms the cancellation.",
			Effect:      tool.StaticEffect(tool.EffectMutates),

			Parameters: map[string]any{
				"type": "object",

				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "Task id of the background agent to stop."},
				},

				"required":             []string{"id"},
				"additionalProperties": false,
			},

			Execute: func(_ context.Context, args map[string]any) (string, error) {
				id, _ := args["id"].(string)
				id = strings.TrimSpace(id)
				if id == "" {
					return "", fmt.Errorf("id is required")
				}
				if err := tasks.Stop(id); err != nil {
					return "", err
				}
				return fmt.Sprintf("Stop requested for background agent %s.", id), nil
			},
		},
	}
}

func formatTaskList(tasks *task.Registry) string {
	list := tasks.List()
	if len(list) == 0 {
		return "No background agents in this session."
	}

	var b strings.Builder
	for _, t := range list {
		line := formatTaskLine(t)
		if t.Status() == task.StatusRunning {
			if activity := t.Activity(); activity != "" {
				line += " — " + activity
			}
		}
		fmt.Fprintf(&b, "%s\n", line)
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatTaskLine(t *task.Task) string {
	return fmt.Sprintf("%s [%s] %s (%s, %s)", t.ID, t.Status(), t.Description, t.AgentType, formatTaskElapsed(t))
}

func formatTaskElapsed(t *task.Task) string {
	return t.Elapsed().Round(time.Second).String()
}
