package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"

	"github.com/adrianliechti/wingman-agent/pkg/text"
)

func isRecoverableError(err error) bool {
	if isContextOverflowError(err) {
		return true
	}

	var streamErr *streamFailure
	errors.As(err, &streamErr)

	var responseErr *responseFailure
	if errors.As(err, &responseErr) {
		switch responseErr.code {
		case string(responses.ResponseErrorCodeServerError),
			string(responses.ResponseErrorCodeRateLimitExceeded),
			string(responses.ResponseErrorCodeVectorStoreTimeout):
			return true
		default:
			return false
		}
	}

	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 408, 409, 429:
			return true
		default:
			return apiErr.StatusCode >= 500
		}
	}

	// Other non-API failures are retryable only when complete marked them as a
	// stream transport failure. Partial responses are not committed and their
	// tool calls are not executed, so a replay cannot duplicate side effects.
	return streamErr != nil
}

var contextOverflowMarkers = []string{
	"context length",
	"context_length",
	"context window",
	"maximum context",
	"too many tokens",
	"token limit",
	"prompt is too long",
	"input is too long",
	"exceeds the maximum",
}

func isContextOverflowError(err error) bool {
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) {
		return false
	}

	switch apiErr.StatusCode {
	case 400, 413:
	default:
		return false
	}

	msg := strings.ToLower(apiErr.Code + " " + apiErr.Message)

	for _, marker := range contextOverflowMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}

	return false
}

func (a *Agent) removeOrphanedToolMessages() {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	a.removeOrphanedToolMessagesLocked()
}

func (a *Agent) removeOrphanedToolMessagesLocked() {

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

	dropped := make(map[int]bool)

	for i, m := range a.Messages {
		for _, c := range m.Content {
			if c.ToolCall != nil && !outputIDs[c.ToolCall.ID] {
				dropped[i] = true
				break
			}

			if c.ToolResult != nil && !callIDs[c.ToolResult.ID] {
				dropped[i] = true
				break
			}
		}
	}

	cleaned := a.Messages
	changed := false

	if len(dropped) > 0 {
		cleaned = nil
		for i, m := range a.Messages {
			if !dropped[i] {
				cleaned = append(cleaned, m)
			}
		}
		changed = true
	}

	if pruned, ok := dropDanglingReasoning(cleaned); ok {
		cleaned = pruned
		changed = true
	}

	if !changed {
		return
	}

	a.Messages = cleaned
	a.Revision++
}

// dropForeignReasoning strips encrypted reasoning payloads that the current
// model cannot decrypt (produced by a different model, e.g. after switching
// from GPT to Claude mid-session or reloading a session under a new model).
// Summaries stay for display; only the opaque payload and its tag are removed.
func (a *Agent) dropForeignReasoning(model string) {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()

	changed := false

	for _, m := range a.Messages {
		for _, c := range m.Content {
			r := c.Reasoning
			if r == nil || r.Content == "" || r.Model == model {
				continue
			}

			r.Content = ""
			r.Model = ""
			changed = true
		}
	}

	if changed {
		a.Revision++
	}
}

// dropDanglingReasoning removes reasoning-only messages that are not followed
// by assistant output (text or a tool call) from the same turn. Providers
// reject replayed reasoning items whose required following item is missing —
// which happens when a broken stream or orphaned-tool-call cleanup strands one.
func dropDanglingReasoning(messages []Message) ([]Message, bool) {
	isReasoningOnly := func(m Message) bool {
		if m.Role != RoleAssistant || len(m.Content) == 0 {
			return false
		}
		for _, c := range m.Content {
			if c.Reasoning == nil {
				return false
			}
		}
		return true
	}

	followedByOutput := func(i int) bool {
		for j := i + 1; j < len(messages); j++ {
			if isReasoningOnly(messages[j]) {
				continue
			}
			if messages[j].Role != RoleAssistant {
				return false
			}
			for _, c := range messages[j].Content {
				if c.ToolCall != nil || c.Text != "" || c.Refusal != "" {
					return true
				}
			}
			return false
		}
		return false
	}

	var drop map[int]bool
	for i, m := range messages {
		if isReasoningOnly(m) && !followedByOutput(i) {
			if drop == nil {
				drop = make(map[int]bool)
			}
			drop[i] = true
		}
	}

	if len(drop) == 0 {
		return messages, false
	}

	var out []Message
	for i, m := range messages {
		if !drop[i] {
			out = append(out, m)
		}
	}
	return out, true
}

const (
	trimProtectBytes    = 96 * 1024
	trimProtectMessages = 12
	trimResultThreshold = 1024
	trimResultKeepBytes = 256
)

const trimMarker = "\n[earlier tool output trimmed to reclaim context — rerun the tool if it is needed again]"
const trimImageMarker = "[image result trimmed to reclaim context — rerun the tool if it is needed again]"

// trimStaleToolResults rewrites old tool-result payloads down to a short stub
// and drops old image results, reclaiming context without an LLM summarization
// pass and without disturbing the conversation spine. The newest messages stay
// untouched so the working set survives. Returns the number of bytes freed.
func (a *Agent) trimStaleToolResults() int {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()

	cut := 0
	total := 0
	for i := len(a.Messages) - 1; i >= 0; i-- {
		if total > trimProtectBytes && len(a.Messages)-i > trimProtectMessages {
			cut = i + 1
			break
		}
		total += messageBytes(a.Messages[i])
	}

	freed := 0

	for i := range a.Messages[:cut] {
		m := a.Messages[i]
		if m.Role != RoleAssistant {
			continue
		}

		var content []Content
		changed := false
		imageDropped := false

		for _, c := range m.Content {
			if c.File != nil {
				freed += len(c.File.Data)
				changed = true
				imageDropped = true
				continue
			}
			if c.ToolResult != nil && len(c.ToolResult.Content) > trimResultThreshold {
				result := *c.ToolResult
				result.Content = text.HeadBytes(result.Content, trimResultKeepBytes) + trimMarker
				freed += len(c.ToolResult.Content) - len(result.Content)
				c.ToolResult = &result
				changed = true
			}
			content = append(content, c)
		}

		if !changed {
			continue
		}

		if imageDropped {
			for j := range content {
				if content[j].ToolResult != nil {
					result := *content[j].ToolResult
					result.Content = trimImageMarker
					content[j].ToolResult = &result
				}
			}
		}

		a.Messages[i].Content = content
	}

	if freed > 0 {
		a.Revision++
	}
	return freed
}

func (a *Agent) compactMessages(ctx context.Context, truncateOnFailure bool) {
	messages := a.requestMessages()
	summaryMessages, recentMessages := splitMessagesForRecoverySummary(messages)

	if len(summaryMessages) == 0 && truncateOnFailure {
		summaryMessages, recentMessages = messages, nil
		if idx := lastVisibleUserIndex(messages); idx >= 0 {
			recentMessages = []Message{messages[idx]}
		}
	}

	summary, err := a.summarizeMessages(ctx, summaryMessages)
	if err != nil || summary == "" {
		if truncateOnFailure {
			a.truncateMessagesForRecovery()
		}
		return
	}

	compacted := append([]Message{{
		Role:    RoleUser,
		Hidden:  true,
		Content: []Content{{Text: summary}},
	}}, recentMessages...)
	a.stateMu.Lock()
	a.Messages = compacted
	a.Revision++
	a.stateMu.Unlock()
	a.removeOrphanedToolMessages()
}

const maxSummarizeBytes = 100 * 1024
const maxRecentBytes = 64 * 1024
const minRecoveryMessagesToPreserve = 12

func splitMessagesForRecoverySummary(messages []Message) ([]Message, []Message) {
	if len(messages) == 0 {
		return nil, nil
	}

	userIdx := lastVisibleUserIndex(messages)

	split := max(userIdx, 0)
	total := 0
	for i := len(messages) - 1; i > userIdx; i-- {
		total += messageBytes(messages[i])
		if total > maxRecentBytes && len(messages)-i > minRecoveryMessagesToPreserve {
			split = i + 1
			break
		}
	}

	recent := messages[split:]
	if userIdx >= 0 && split > userIdx {
		recent = append([]Message{messages[userIdx]}, recent...)
	}

	return messages[:split], recent
}

func lastVisibleUserIndex(messages []Message) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == RoleUser && !messages[i].Hidden {
			return i
		}
	}
	return -1
}

func messageBytes(m Message) int {
	total := 0
	for _, c := range m.Content {
		total += len(c.Text) + len(c.Refusal)
		if c.File != nil {
			total += len(c.File.Data)
		}
		if c.ToolCall != nil {
			total += len(c.ToolCall.Args)
		}
		if c.ToolResult != nil {
			total += len(c.ToolResult.Content)
		}
		if c.Reasoning != nil {
			total += len(c.Reasoning.Summary) + len(c.Reasoning.Content)
		}
	}
	return total
}

func (a *Agent) summarizeMessages(ctx context.Context, messages []Message) (string, error) {
	transcript := recoverySummaryTranscript(messages)

	if transcript == "" {
		return "", nil
	}

	resp, err := a.client.Responses.New(ctx, responses.ResponseNewParams{
		Model: a.utilityModelName(),
		Instructions: openai.String(
			"An LLM context limit was reached during an active working session between a user and you (the assistant). " +
				"Produce a continuation briefing for yourself so the session can resume seamlessly. " +
				"Frame and tone for an agent reader (you), not a human — completeness matters more than brevity. " +
				"Do not answer the user's latest request; only summarize the prior context. " +
				"Do not introduce new ideas unless the user already confirmed them.\n\n" +
				"Include these sections:\n" +
				"1. User Intent — all goals and requests\n" +
				"2. Technical Concepts — tools, methods, libraries discussed\n" +
				"3. Files + Code — viewed/edited files with key code and why changes were made\n" +
				"4. Errors + Fixes — bugs encountered, resolutions, user corrections\n" +
				"5. Problem Solving — issues solved or still in progress\n" +
				"6. Pending Tasks — unresolved user requests\n" +
				"7. Current Work — what was active when the limit hit: file names, code, alignment to the latest instruction\n" +
				"8. Next Step — only if it directly continues an explicit user instruction",
		),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(transcript),
		},
		Store: openai.Bool(false),
	})

	if err != nil {
		return "", err
	}

	summary := recoverySummaryOutput(resp)
	if summary == "" {
		return "", nil
	}
	return "[Previous conversation summary]\n\n" + summary, nil
}

// Recap produces a short user-facing briefing of the conversation so far,
// for returning to a resumed session.
func (a *Agent) Recap(ctx context.Context) (string, error) {
	transcript := recoverySummaryTranscript(a.requestMessages())
	if transcript == "" {
		return "", nil
	}

	resp, err := a.client.Responses.New(ctx, responses.ResponseNewParams{
		Model: a.utilityModelName(),
		Instructions: openai.String(
			"The user is returning to a coding session after time away. " +
				"From the transcript, write a brief recap in Markdown: 2-5 bullets covering what was being worked on, " +
				"what was accomplished or changed, and any open items or agreed next step. " +
				"Address the user directly. No preamble, no heading, no questions.",
		),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(transcript),
		},
		Store: openai.Bool(false),
	})

	if err != nil {
		return "", err
	}

	return strings.TrimSpace(recoverySummaryOutput(resp)), nil
}

func recoverySummaryOutput(resp *responses.Response) string {
	var result strings.Builder
	for _, item := range resp.Output {
		msg := item.AsMessage()
		for _, part := range msg.Content {
			text := part.AsOutputText()
			if text.Text != "" {
				result.WriteString(text.Text)
			}
		}
	}

	return result.String()
}

func recoverySummaryTranscript(messages []Message) string {
	var chunks []string
	total := 0

	for i := len(messages) - 1; i >= 0; i-- {
		var mb strings.Builder
		m := messages[i]
		for _, c := range m.Content {
			if c.Text != "" {
				fmt.Fprintf(&mb, "[%s]: %s\n\n", m.Role, text.TruncateHead(c.Text, 2000))
			}

			if c.Refusal != "" {
				fmt.Fprintf(&mb, "[%s]: %s\n\n", m.Role, text.TruncateHead(c.Refusal, 2000))
			}

			if c.File != nil {
				fmt.Fprintf(&mb, "[%s]: [file attachment, %d bytes]\n\n", m.Role, len(c.File.Data))
			}

			if c.ToolCall != nil {
				fmt.Fprintf(&mb, "[tool call]: %s(%s)\n\n", c.ToolCall.Name, text.TruncateHead(c.ToolCall.Args, 200))
			}

			if c.ToolResult != nil {
				fmt.Fprintf(&mb, "[tool result]: %s\n\n", text.TruncateHead(c.ToolResult.Content, 500))
			}
		}

		if mb.Len() == 0 {
			continue
		}

		if len(chunks) > 0 && total+mb.Len() > maxSummarizeBytes {
			break
		}

		chunks = append(chunks, mb.String())
		total += mb.Len()
	}

	var sb strings.Builder
	for i := len(chunks) - 1; i >= 0; i-- {
		sb.WriteString(chunks[i])
	}

	return sb.String()
}

func (a *Agent) truncateMessagesForRecovery() {
	a.stateMu.Lock()
	if len(a.Messages) > minRecoveryMessagesToPreserve {
		start := len(a.Messages) - minRecoveryMessagesToPreserve
		trimmed := CloneMessages(a.Messages[start:])
		if userIdx := lastVisibleUserIndex(a.Messages); userIdx >= 0 && userIdx < start {
			trimmed = append(CloneMessages(a.Messages[userIdx:userIdx+1]), trimmed...)
		}
		a.Messages = trimmed
		a.Revision++
	}
	a.stateMu.Unlock()
	a.removeOrphanedToolMessages()
}
