package plan

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman-cli/pkg/plan"
	"github.com/adrianliechti/wingman-cli/pkg/tool"
)

func formatPlan(p *plan.Plan) string {
	if p == nil || p.IsEmpty() {
		return "No plan set."
	}

	var sb strings.Builder

	if p.Title != "" {
		fmt.Fprintf(&sb, "## Plan: %s\n\n", p.Title)
	}

	for i, task := range p.Tasks {
		marker := " "

		switch task.Status {
		case plan.StatusDone:
			marker = "✓"

		case plan.StatusRunning:
			marker = "→"

		case plan.StatusSkipped:
			marker = "○"
		}

		fmt.Fprintf(&sb, "%d. [%s] %s\n", i+1, marker, task.Description)
	}

	return sb.String()
}

func Tools() []tool.Tool {
	return []tool.Tool{
		UpdatePlan(),
	}
}

func UpdatePlan() tool.Tool {
	return tool.Tool{
		Name:        "update_plan",
		Description: `Manage task plan. Actions: "set" (create plan with tasks), "update" (change task status by index), "get" (show current plan).`,
		Hidden:      true,

		Parameters: map[string]any{
			"type": "object",

			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"set", "update", "get"},
					"description": "The action to perform",
				},

				"title": map[string]any{
					"type":        "string",
					"description": "Plan title (for 'set' action)",
				},

				"tasks": map[string]any{
					"type":        "array",
					"description": "List of task descriptions (for 'set' action)",

					"items": map[string]any{
						"type": "string",
					},
				},

				"index": map[string]any{
					"type":        "integer",
					"description": "Task index to update, 0-based (for 'update' action)",
				},

				"status": map[string]any{
					"type":        "string",
					"enum":        []string{"pending", "running", "done", "skipped"},
					"description": "New status for the task (for 'update' action)",
				},
			},

			"required": []string{"action"},
		},

		Execute: executeUpdatePlan,
	}
}

func executeUpdatePlan(ctx context.Context, env *tool.Environment, args map[string]any) (string, error) {
	action, ok := args["action"].(string)

	if !ok {
		return "", fmt.Errorf("action is required")
	}

	switch action {
	case "set":

		return actionSet(env, args)

	case "update":

		return actionUpdate(env, args)

	case "get":

		return actionGet(env)

	default:

		return "", fmt.Errorf("unknown action: %s", action)
	}
}

func actionSet(env *tool.Environment, args map[string]any) (string, error) {
	title, _ := args["title"].(string)

	tasksRaw, ok := args["tasks"]

	if !ok {
		return "", fmt.Errorf("tasks array is required for 'set' action")
	}

	// Handle JSON array conversion
	var taskDescriptions []string

	switch v := tasksRaw.(type) {
	case []any:

		for _, item := range v {
			if s, ok := item.(string); ok {
				taskDescriptions = append(taskDescriptions, s)
			}
		}
	case []string:
		taskDescriptions = v

	default:
		// Try JSON unmarshal as fallback
		data, _ := json.Marshal(tasksRaw)
		json.Unmarshal(data, &taskDescriptions)
	}

	if len(taskDescriptions) == 0 {
		return "", fmt.Errorf("at least one task is required")
	}

	env.Plan.SetTasks(title, taskDescriptions)

	return fmt.Sprintf("Plan set with %d tasks.\n\n%s", len(taskDescriptions), formatPlan(env.Plan)), nil
}

func actionUpdate(env *tool.Environment, args map[string]any) (string, error) {
	indexRaw, ok := args["index"]

	if !ok {
		return "", fmt.Errorf("index is required for 'update' action")
	}

	// Handle JSON number conversion (comes as float64)
	var index int

	switch v := indexRaw.(type) {
	case float64:
		index = int(v)

	case int:
		index = v

	default:

		return "", fmt.Errorf("index must be a number")
	}

	status, ok := args["status"].(string)

	if !ok {
		return "", fmt.Errorf("status is required for 'update' action")
	}

	if err := env.Plan.UpdateTask(index, plan.Status(status)); err != nil {
		return "", err
	}

	return fmt.Sprintf("Task %d updated to %s.\n\n%s", index+1, status, formatPlan(env.Plan)), nil
}

func actionGet(env *tool.Environment) (string, error) {
	if env.Plan.IsEmpty() {
		return "No plan has been set yet.", nil
	}

	return formatPlan(env.Plan), nil
}