package util

import (
	wingman "github.com/adrianliechti/wingman/pkg/client"
)

func TruncateMessages(messages []wingman.Message) []wingman.Message {
	return messages
}

func messageTokens(message wingman.Message) int {
	var result = 0

	for _, c := range message.Content {
		result += len(c.Text)
		result += len(c.Refusal)

		if c.File != nil {
			result += len(c.File.Content)
		}

		if c.ToolCall != nil {
			result += len(c.ToolCall.Name)
			result += len(c.ToolCall.Arguments)
		}

		if c.ToolResult != nil {
			result += len(c.ToolResult.Data)
		}
	}

	return result / 4
}
