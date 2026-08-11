package todo

import (
	"context"
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

var validStatuses = map[string]bool{
	"pending":     true,
	"in_progress": true,
	"completed":   true,
}

func Tools() []tool.Tool {
	description := strings.Join([]string{
		"Update your task list for the current session. Each call replaces the whole list.",
		"- Use for multi-step work (3+ distinct steps) or when the user gives several tasks. Skip it for single straightforward tasks — writing the list would cost more than it guides.",
		"- Keep exactly one item in_progress at a time. Mark items completed immediately when they are done; don't batch completions.",
		"- Only mark an item completed when it truly succeeded — tests pass, errors resolved. If blocked or partially done, keep it in_progress and add a new item for what's blocking.",
		"- Add newly discovered steps as they come up, and remove items that became irrelevant instead of leaving them pending.",
	}, "\n")

	return []tool.Tool{{
		Name:        "todo",
		Description: description,
		Effect:      tool.StaticEffect(tool.EffectReadOnly),

		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"items": map[string]any{
					"type":        "array",
					"description": "The full task list in execution order; replaces the previous list.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"content": map[string]any{"type": "string", "description": "Short imperative description of the step."},
							"status": map[string]any{
								"type": "string",
								"enum": []string{"pending", "in_progress", "completed"},
							},
						},
						"required":             []string{"content", "status"},
						"additionalProperties": false,
					},
				},
			},
			"required":             []string{"items"},
			"additionalProperties": false,
		},

		Execute: func(ctx context.Context, args map[string]any) (tool.Result, error) {
			raw, ok := args["items"].([]any)
			if !ok {
				return tool.Result{}, fmt.Errorf("items must be an array")
			}

			if len(raw) == 0 {
				return tool.Text("Todo list cleared."), nil
			}

			completed := 0

			for i, item := range raw {
				entry, ok := item.(map[string]any)
				if !ok {
					return tool.Result{}, fmt.Errorf("items[%d] must be an object with content and status", i)
				}

				content, _ := entry["content"].(string)
				if strings.TrimSpace(content) == "" {
					return tool.Result{}, fmt.Errorf("items[%d].content is required", i)
				}

				status, _ := entry["status"].(string)
				if !validStatuses[status] {
					return tool.Result{}, fmt.Errorf("items[%d].status must be pending, in_progress, or completed", i)
				}
				if status == "completed" {
					completed++
				}
			}

			// The model just wrote the list (it is in the call args); echoing
			// it back would only bloat the transcript.
			return tool.Text(fmt.Sprintf("Todo list updated (%d/%d completed).", completed, len(raw))), nil
		},
	}}
}
