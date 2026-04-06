package agent

import (
	"errors"

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

// removeOldestToolMessages drops the oldest ~50% of tool call/output pairs
// to reduce context size, after first removing orphans.
func (a *Agent) removeOldestToolMessages() {
	a.removeOrphanedToolMessages()
	a.messages = dropOldestToolPairs(a.messages)
}

// dropOldestToolPairs removes the oldest ~50% of tool call + output pairs
// while preserving user messages, assistant text, reasoning, and compaction items.
func dropOldestToolPairs(messages []responses.ResponseInputItemUnionParam) []responses.ResponseInputItemUnionParam {
	type toolPair struct {
		callIdx   int
		outputIdx int
	}

	callIndices := make(map[string]int)
	var pairs []toolPair

	for i, item := range messages {
		if fc := item.OfFunctionCall; fc != nil {
			callIndices[fc.CallID] = i
		}
		if fco := item.OfFunctionCallOutput; fco != nil {
			if ci, ok := callIndices[fco.CallID]; ok {
				pairs = append(pairs, toolPair{callIdx: ci, outputIdx: i})
			}
		}
	}

	if len(pairs) <= 2 {
		return messages
	}

	dropCount := len(pairs) / 2
	dropSet := make(map[int]bool)

	for _, p := range pairs[:dropCount] {
		dropSet[p.callIdx] = true
		dropSet[p.outputIdx] = true

		if p.callIdx > 0 && messages[p.callIdx-1].OfReasoning != nil {
			dropSet[p.callIdx-1] = true
		}
	}

	var result []responses.ResponseInputItemUnionParam
	for i, item := range messages {
		if !dropSet[i] {
			result = append(result, item)
		}
	}

	return result
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
