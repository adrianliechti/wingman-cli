package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
)

// isRecoverableError returns true if the error might be resolved by
// cleaning up the message history and retrying. Auth and permission
// errors are never recoverable. Everything else gets a chance.
func isRecoverableError(err error) bool {
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) {
		return true // local error (JSON parse, etc.) — retry anyway
	}

	switch apiErr.StatusCode {
	case 401, 403:
		return false // auth/permission — cleanup won't help
	default:
		return true
	}
}

// removeOrphanedToolMessages removes tool calls without matching outputs
// and tool outputs without matching calls.
func (a *Agent) removeOrphanedToolMessages() {
	callIDs := make(map[string]bool)
	outputIDs := make(map[string]bool)

	for _, item := range a.messages {
		if fc := item.OfFunctionCall; fc != nil {
			callIDs[fc.CallID] = true
		}
		if fco := item.OfFunctionCallOutput; fco != nil {
			outputIDs[fco.CallID] = true
		}
	}

	// Fast path: no orphans
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

	var cleaned []responses.ResponseInputItemUnionParam

	for _, item := range a.messages {
		if fc := item.OfFunctionCall; fc != nil {
			if !outputIDs[fc.CallID] {
				continue
			}
		}

		if fco := item.OfFunctionCallOutput; fco != nil {
			if !callIDs[fco.CallID] {
				continue
			}
		}

		cleaned = append(cleaned, item)
	}

	a.messages = cleaned
}

// compactMessages summarizes the entire conversation into a single user
// message using an LLM call, preserving context while drastically reducing
// token count. Falls back to removeAllToolMessages if the summary fails.
func (a *Agent) compactMessages(ctx context.Context) {
	summary, err := a.summarizeMessages(ctx)
	if err != nil || summary == "" {
		a.removeAllToolMessages()
		return
	}

	a.messages = []responses.ResponseInputItemUnionParam{{
		OfMessage: &responses.EasyInputMessageParam{
			Role: responses.EasyInputMessageRoleUser,
			Content: responses.EasyInputMessageContentUnionParam{
				OfString: openai.String(summary),
			},
		},
	}}
}

const maxSummarizeBytes = 100 * 1024 // 100KB cap for the summarization input

func (a *Agent) summarizeMessages(ctx context.Context) (string, error) {
	var sb strings.Builder

	for _, item := range a.messages {
		if sb.Len() > maxSummarizeBytes {
			break
		}

		if msg := item.OfMessage; msg != nil {
			role := string(msg.Role)
			if text := msg.Content.OfString.Value; text != "" {
				fmt.Fprintf(&sb, "[%s]: %s\n\n", role, truncate(text, 2000))
			}
			for _, part := range msg.Content.OfInputItemContentList {
				if part.OfInputText != nil {
					fmt.Fprintf(&sb, "[%s]: %s\n\n", role, truncate(part.OfInputText.Text, 2000))
				}
			}
		}

		if outMsg := item.OfOutputMessage; outMsg != nil {
			for _, part := range outMsg.Content {
				if text := part.OfOutputText; text != nil {
					fmt.Fprintf(&sb, "[assistant]: %s\n\n", truncate(text.Text, 2000))
				}
			}
		}

		if fc := item.OfFunctionCall; fc != nil {
			fmt.Fprintf(&sb, "[tool call]: %s(%s)\n\n", fc.Name, truncate(fc.Arguments, 200))
		}

		if fco := item.OfFunctionCallOutput; fco != nil {
			fmt.Fprintf(&sb, "[tool result]: %s\n\n", truncate(fco.Output.OfString.Value, 500))
		}
	}

	if sb.Len() == 0 {
		return "", nil
	}

	resp, err := a.Client.Responses.New(ctx, responses.ResponseNewParams{
		Model: a.Model,
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

// removeAllToolMessages removes ALL tool calls and tool results from the
// message history, keeping only user messages, assistant text, reasoning,
// and compaction items. This is the most aggressive cleanup.
func (a *Agent) removeAllToolMessages() {
	var cleaned []responses.ResponseInputItemUnionParam

	for _, item := range a.messages {
		if item.OfFunctionCall != nil || item.OfFunctionCallOutput != nil {
			continue
		}
		cleaned = append(cleaned, item)
	}

	a.messages = cleaned
}
