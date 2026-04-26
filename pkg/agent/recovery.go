package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
)

func isRecoverableError(err error) bool {
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) {
		return true
	}

	switch apiErr.StatusCode {
	case 401, 403:
		return false
	default:
		return true
	}
}

func (a *Agent) removeOrphanedToolMessages() {

	callIDs := make(map[string]bool)
	outputIDs := make(map[string]bool)

	for _, m := range a.Messages {
		for _, c := range m.Content {
			if c.ToolCall != nil && c.ToolCall.ID != "" {
				callIDs[c.ToolCall.ID] = true
			}

			if c.ToolResult != nil && c.ToolResult.ID != "" {
				outputIDs[c.ToolResult.ID] = true
			}
		}
	}

	hasOrphans := false
	for id := range callIDs {
		if !outputIDs[id] {
			hasOrphans = true
			break
		}
	}
	if !hasOrphans {
		for id := range outputIDs {
			if !callIDs[id] {
				hasOrphans = true
				break
			}
		}
	}
	if !hasOrphans {
		return
	}

	var cleaned []Message

	for _, m := range a.Messages {
		drop := false

		for _, c := range m.Content {
			if c.ToolCall != nil && !outputIDs[c.ToolCall.ID] {
				drop = true
				break
			}

			if c.ToolResult != nil && !callIDs[c.ToolResult.ID] {
				drop = true
				break
			}
		}

		if !drop {
			cleaned = append(cleaned, m)
		}
	}

	a.Messages = cleaned
}

func (a *Agent) compactMessages(ctx context.Context) {
	summary, err := a.summarizeMessages(ctx)
	if err != nil || summary == "" {
		a.removeAllToolMessages()
		return
	}

	a.Messages = []Message{{
		Role:    RoleUser,
		Content: []Content{{Text: summary}},
	}}
}

const maxSummarizeBytes = 100 * 1024

func (a *Agent) summarizeMessages(ctx context.Context) (string, error) {
	var sb strings.Builder
	messages := a.Messages

	for _, m := range messages {
		if sb.Len() > maxSummarizeBytes {
			break
		}

		for _, c := range m.Content {
			if c.Text != "" {
				fmt.Fprintf(&sb, "[%s]: %s\n\n", m.Role, truncate(c.Text, 2000))
			}

			if c.Refusal != "" {
				fmt.Fprintf(&sb, "[%s]: %s\n\n", m.Role, truncate(c.Refusal, 2000))
			}

			if c.ToolCall != nil {
				fmt.Fprintf(&sb, "[tool call]: %s(%s)\n\n", c.ToolCall.Name, truncate(c.ToolCall.Args, 200))
			}

			if c.ToolResult != nil {
				fmt.Fprintf(&sb, "[tool result]: %s\n\n", truncate(c.ToolResult.Content, 500))
			}
		}
	}

	if sb.Len() == 0 {
		return "", nil
	}

	model := ""
	if a.Config.Model != nil {
		model = a.Model()
	}

	resp, err := a.client.Responses.New(ctx, responses.ResponseNewParams{
		Model: model,
		Instructions: openai.String(
			"Summarize the following conversation between a user and an AI assistant. " +
				"Preserve all important context: what the user asked, what was done, what files were modified, " +
				"key decisions made, and the current state of the task. " +
				"Be concise but complete. Format as a briefing the assistant can use to continue the conversation.",
		),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(sb.String()),
		},
		Store: openai.Bool(false),
	})

	if err != nil {
		return "", err
	}

	var result strings.Builder
	result.WriteString("[Previous conversation summary]\n\n")

	for _, item := range resp.Output {
		msg := item.AsMessage()
		for _, part := range msg.Content {
			text := part.AsOutputText()
			if text.Text != "" {
				result.WriteString(text.Text)
			}
		}
	}

	return result.String(), nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + " [truncated]"
}

func (a *Agent) removeAllToolMessages() {

	var cleaned []Message

	for _, m := range a.Messages {
		drop := false

		for _, c := range m.Content {
			if c.ToolCall != nil || c.ToolResult != nil {
				drop = true
				break
			}
		}

		if !drop {
			cleaned = append(cleaned, m)
		}
	}

	a.Messages = cleaned
}
