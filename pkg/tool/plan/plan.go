package plan

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/adrianliechti/wingman-cli/pkg/plan"
	"github.com/adrianliechti/wingman-cli/pkg/tool"
)

func Tools() []tool.Tool {
	return []tool.Tool{
		UpdatePlan(),
	}
}

func UpdatePlan() tool.Tool {
	return tool.Tool{
		Name:        "update_plan",
		Description: `Manage task plan. Actions: "set" (create plan with steps), "update" (change step status by index), "get" (show current plan).`,

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
				"steps": map[string]any{
					"type":        "array",
					"description": "List of step descriptions (for 'set' action)",
					"items": map[string]any{
						"type": "string",
					},
				},
				"index": map[string]any{
					"type":        "integer",
					"description": "Step index to update, 0-based (for 'update' action)",
				},
				"status": map[string]any{
					"type":        "string",
					"enum":        []string{"pending", "in_progress", "done", "skipped"},
					"description": "New status for the step (for 'update' action)",
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
		return actionSet(args)
	case "update":
		return actionUpdate(args)
	case "get":
		return actionGet()
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

func actionSet(args map[string]any) (string, error) {
	title, _ := args["title"].(string)

	stepsRaw, ok := args["steps"]
	if !ok {
		return "", fmt.Errorf("steps array is required for 'set' action")
	}

	// Handle JSON array conversion
	var stepDescriptions []string

	switch v := stepsRaw.(type) {
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				stepDescriptions = append(stepDescriptions, s)
			}
		}
	case []string:
		stepDescriptions = v
	default:
		// Try JSON unmarshal as fallback
		data, _ := json.Marshal(stepsRaw)
		json.Unmarshal(data, &stepDescriptions)
	}

	if len(stepDescriptions) == 0 {
		return "", fmt.Errorf("at least one step is required")
	}

	steps := make([]plan.Step, len(stepDescriptions))
	for i, desc := range stepDescriptions {
		steps[i] = plan.Step{
			Description: desc,
			Status:      plan.StatusPending,
		}
	}

	plan.Set(&plan.Plan{
		Title: title,
		Steps: steps,
	})

	return fmt.Sprintf("Plan set with %d steps.\n\n%s", len(steps), plan.Format()), nil
}

func actionUpdate(args map[string]any) (string, error) {
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

	statusStr, ok := args["status"].(string)
	if !ok {
		return "", fmt.Errorf("status is required for 'update' action")
	}

	status := plan.Status(statusStr)

	if err := plan.UpdateStep(index, status); err != nil {
		return "", err
	}

	return fmt.Sprintf("Step %d updated to %s.\n\n%s", index+1, status, plan.Format()), nil
}

func actionGet() (string, error) {
	if !plan.HasPlan() {
		return "No plan has been set yet.", nil
	}
	return plan.Format(), nil
}
