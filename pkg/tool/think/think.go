package think

import (
	"context"

	"github.com/adrianliechti/wingman-cli/pkg/tool"
)

func Tools(ctx context.Context) ([]tool.Tool, error) {
	return []tool.Tool{
		{
			Name:        "think",
			Description: "Use this tool to think deeply about the user's request and organize your thoughts. This tool helps improve response quality by allowing the model to consider the request carefully, brainstorm solutions, and plan complex tasks. It's particularly useful for:\n\n1. Exploring repository issues and brainstorming bug fixes\n2. Analyzing test results and planning fixes\n3. Planning complex refactoring approaches\n4. Designing new features and architecture\n5. Organizing debugging hypotheses\n\nThe tool logs your thought process for transparency but doesn't execute any code or make changes.",

			Schema: &tool.Schema{
				Type: "object",

				Properties: map[string]*tool.Schema{
					"thoughts": {
						Type:        "string",
						Description: "Your thoughts about the current task or problem. This should be a clear, structured explanation of your reasoning, analysis, or planning process.",
					},
				},

				Required: []string{"thoughts"},
			},

			ToolHandler: func(ctx context.Context, params map[string]any) (any, error) {
				thoughts := params["thoughts"].(string)
				return thoughts, nil
			},
		},
	}, nil
}
