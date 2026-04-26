package manage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/claw/memory"
	"github.com/adrianliechti/wingman-agent/pkg/claw/tool/schedule"
)

// AgentManager provides the operations needed by the management tools.
type AgentManager interface {
	CreateAgent(name string) error
	DeleteAgent(name string) error
}

// Tools returns agent lifecycle management tools.
func Tools(mgr AgentManager, store *memory.Store) []tool.Tool {
	return []tool.Tool{
		{
			Name: "create_agent",
			Description: strings.Join([]string{
				"Create a new agent with its own isolated workspace and optional scheduled tasks.",
				"",
				"Parameters:",
				"- name: unique identifier (required)",
				"- instructions: written to the agent's AGENTS.md (its identity and behavior)",
				"- tasks: list of scheduled tasks",
				"",
				"Task format: {\"prompt\": \"...\", \"schedule\": \"every 15m\"} or {\"prompt\": \"...\", \"schedule\": \"0 9 * * 1\"}",
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
									"description": "Schedule: \"every 15m\", cron expression, or ISO 8601 timestamp.",
								},
							},
							"required": []string{"prompt", "schedule"},
						},
						"description": "Scheduled tasks for the agent.",
					},
				},
				"required": []string{"name"},
			},
			Execute: func(ctx context.Context, args map[string]any) (string, error) {
				name, _ := args["name"].(string)
				if name == "" {
					return "", fmt.Errorf("name is required")
				}

				if err := mgr.CreateAgent(name); err != nil {
					return "", err
				}

				// Write AGENTS.md
				if instructions, ok := args["instructions"].(string); ok && instructions != "" {
					if err := store.WriteAgent(name, instructions); err != nil {
						return "", fmt.Errorf("agent created but failed to write AGENTS.md: %w", err)
					}
				}

				// Write tasks.yaml
				if taskList, ok := args["tasks"].([]any); ok && len(taskList) > 0 {
					agentDir := store.AgentDir(name)
					var tasks []schedule.Task

					for _, t := range taskList {
						m, ok := t.(map[string]any)
						if !ok {
							continue
						}

						prompt := strVal(m, "prompt")
						sched := strVal(m, "schedule")

						if prompt == "" || sched == "" {
							continue
						}

						tasks = append(tasks, schedule.Task{
							ID:        uuid.NewString(),
							Prompt:    prompt,
							Schedule:  sched,
							Status:    "active",
							CreatedAt: time.Now().UTC(),
						})
					}

					if err := schedule.SaveTasks(agentDir, tasks); err != nil {
						return "", fmt.Errorf("agent created but failed to save tasks: %w", err)
					}
				}

				var result strings.Builder
				fmt.Fprintf(&result, "Agent %q created.\n", name)
				fmt.Fprintf(&result, "Workspace: %s\n", store.WorkspaceDir(name))

				if instructions, ok := args["instructions"].(string); ok && instructions != "" {
					fmt.Fprintf(&result, "AGENTS.md: written (%d bytes)\n", len(instructions))
				}

				if taskList, ok := args["tasks"].([]any); ok && len(taskList) > 0 {
					fmt.Fprintf(&result, "tasks.yaml: %d task(s) scheduled\n", len(taskList))
				}

				return result.String(), nil
			},
		},
		{
			Name:        "delete_agent",
			Description: "Unregister an agent, stop its scheduled task, and delete all its data. Cannot delete the main agent.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Name of the agent to delete.",
					},
				},
				"required": []string{"name"},
			},
			Execute: func(ctx context.Context, args map[string]any) (string, error) {
				name, _ := args["name"].(string)
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

func strVal(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}
