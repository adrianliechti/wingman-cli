package manage

import (
	"context"
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/schedule"
	"github.com/adrianliechti/wingman-agent/pkg/claw/memory"
)

type AgentManager interface {
	CreateAgent(name string) error
	DeleteAgent(name string) error
}

func Tools(mgr AgentManager, store *memory.Store) []tool.Tool {
	return []tool.Tool{
		{
			Name:   "create_agent",
			Effect: tool.StaticEffect(tool.EffectMutates),
			Description: strings.Join([]string{
				"Create a new agent with its own isolated workspace and optional scheduled tasks.",
				"",
				"Parameters:",
				"- name: unique identifier (required)",
				"- instructions: written to the agent's AGENTS.md (its identity and behavior)",
				"- tasks: list of scheduled tasks",
				"",
				"Task format: {\"prompt\": \"...\", \"schedule\": \"every 15m\"} with an optional \"script\" pre-check (print {\"wake\": false} to skip a run)",
			}, "\n"),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Unique name for the agent (lowercase, no spaces).",
					},
					"instructions": map[string]any{
						"type":        "string",
						"description": "Agent instructions written to AGENTS.md.",
					},
					"tasks": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"prompt": map[string]any{
									"type":        "string",
									"description": "What the task should do when it runs.",
								},
								"schedule": map[string]any{
									"type":        "string",
									"description": "Schedule: \"every 15m\", cron expression, or timestamp.",
								},
								"script": map[string]any{
									"type":        "string",
									"description": "Optional pre-check script (same interpreter as the shell tool); print {\"wake\": false} to skip a run silently.",
								},
							},
							"required":             []string{"prompt", "schedule"},
							"additionalProperties": false,
						},
						"description": "Scheduled tasks for the agent.",
					},
				},
				"required":             []string{"name"},
				"additionalProperties": false,
			},
			Execute: func(ctx context.Context, args map[string]any) (string, error) {
				name, _ := args["name"].(string)
				name = strings.TrimSpace(name)
				if name == "" {
					return "", fmt.Errorf("name is required")
				}

				tasks, err := parseTasksArg(args)
				if err != nil {
					return "", err
				}

				if err := mgr.CreateAgent(name); err != nil {
					return "", err
				}

				if instructions, ok := args["instructions"].(string); ok && instructions != "" {
					if err := store.WriteAgent(name, instructions); err != nil {
						return "", fmt.Errorf("agent created but failed to write AGENTS.md: %w", err)
					}
				}

				if len(tasks) > 0 {
					err := schedule.NewFileStore(store.AgentDir(name)).Mutate(func(existing []schedule.Task) ([]schedule.Task, error) {
						return append(existing, tasks...), nil
					})
					if err != nil {
						return "", fmt.Errorf("agent created but failed to save tasks: %w", err)
					}
				}

				var result strings.Builder
				fmt.Fprintf(&result, "Agent %q created.\n", name)
				fmt.Fprintf(&result, "Workspace: %s\n", store.WorkspaceDir(name))

				if instructions, ok := args["instructions"].(string); ok && instructions != "" {
					fmt.Fprintf(&result, "AGENTS.md: written (%d bytes)\n", len(instructions))
				}

				if len(tasks) > 0 {
					fmt.Fprintf(&result, "tasks.yaml: %d task(s) scheduled\n", len(tasks))
				}

				return result.String(), nil
			},
		},
		{
			Name:        "delete_agent",
			Description: "Unregister an agent, stop its scheduled task, and delete all its data. Cannot delete the main agent.",
			Effect:      tool.StaticEffect(tool.EffectDangerous),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Name of the agent to delete.",
					},
				},
				"required":             []string{"name"},
				"additionalProperties": false,
			},
			Execute: func(ctx context.Context, args map[string]any) (string, error) {
				name, _ := args["name"].(string)
				name = strings.TrimSpace(name)
				if name == "" {
					return "", fmt.Errorf("name is required")
				}

				if err := mgr.DeleteAgent(name); err != nil {
					return "", err
				}

				return fmt.Sprintf("Agent %q deleted.", name), nil
			},
		},
	}
}

func parseTasksArg(args map[string]any) ([]schedule.Task, error) {
	raw, ok := args["tasks"]
	if !ok || raw == nil {
		return nil, nil
	}

	taskList, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("tasks must be an array")
	}

	tasks := make([]schedule.Task, 0, len(taskList))
	for i, t := range taskList {
		m, ok := t.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("tasks[%d] must be an object", i)
		}

		prompt, _ := m["prompt"].(string)
		sched, _ := m["schedule"].(string)
		script, _ := m["script"].(string)

		task, err := schedule.NewTask(prompt, sched)
		if err != nil {
			return nil, fmt.Errorf("tasks[%d]: %w", i, err)
		}

		task.Script = strings.TrimSpace(script)

		tasks = append(tasks, task)
	}

	return tasks, nil
}
