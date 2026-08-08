package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/coder/acp-go-sdk"
)

type streamedBlock struct {
	index int
	kind  string
	text  string
}

// streamedBlockTracker records the text and thinking that reached the client
// through live deltas. Claude's consolidated assistant message repeats those
// blocks, but its message id is not stable across every gateway, so content is
// the reliable correlation key.
type streamedBlockTracker struct {
	blocks []streamedBlock
}

func (t *streamedBlockTracker) reset() {
	if t != nil {
		t.blocks = t.blocks[:0]
	}
}

func (t *streamedBlockTracker) append(index int, kind, text string) {
	if t == nil || text == "" {
		return
	}
	if n := len(t.blocks); n > 0 && t.blocks[n-1].index == index && t.blocks[n-1].kind == kind {
		t.blocks[n-1].text += text
		return
	}
	t.blocks = append(t.blocks, streamedBlock{index: index, kind: kind, text: text})
}

// consume removes the prefix of each assembled block that was already sent as
// deltas. A partial stream therefore emits only its missing tail, while a block
// that never streamed is preserved in full.
func (t *streamedBlockTracker) consume(content []cliMsgBlock) []cliMsgBlock {
	if t == nil {
		return content
	}
	defer t.reset()

	kept := make([]cliMsgBlock, 0, len(content))
	streamPos := 0
	for _, block := range content {
		kind, full := "", ""
		switch block.Type {
		case "text":
			kind, full = "text", block.Text
		case "thinking":
			kind, full = "thinking", block.Thinking
		default:
			kept = append(kept, block)
			continue
		}
		if full == "" {
			continue
		}
		if streamPos < len(t.blocks) {
			streamed := t.blocks[streamPos]
			if streamed.kind == kind && streamed.text != "" && strings.HasPrefix(full, streamed.text) {
				streamPos++
				remainder := strings.TrimPrefix(full, streamed.text)
				if remainder == "" {
					continue
				}
				if kind == "text" {
					block.Text = remainder
				} else {
					block.Thinking = remainder
				}
			}
		}
		kept = append(kept, block)
	}
	return kept
}

func emitStreamEvent(ctx context.Context, conn *acp.AgentSideConnection, sid acp.SessionId, raw json.RawMessage, streamed *streamedBlockTracker) error {
	if len(raw) == 0 {
		return nil
	}
	var e streamEvent
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil
	}
	if e.Type == "message_start" {
		streamed.reset()
		return nil
	}
	if e.Type != "content_block_delta" {
		return nil
	}
	var update acp.SessionUpdate
	switch e.Delta.Type {
	case "text_delta":
		if e.Delta.Text == "" {
			return nil
		}
		streamed.append(e.Index, "text", e.Delta.Text)
		update = acp.UpdateAgentMessageText(e.Delta.Text)
	case "thinking_delta":
		if e.Delta.Thinking == "" {
			return nil
		}
		streamed.append(e.Index, "thinking", e.Delta.Thinking)
		update = acp.UpdateAgentThoughtText(e.Delta.Thinking)
	default:
		return nil
	}
	return conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: sid, Update: update})
}

// emitAssistant renders a consolidated assistant message. Blocks already sent
// as live deltas are removed by content, while unstreamed blocks and partial
// tails are still emitted. streamed is nil on history replay.
func emitAssistant(ctx context.Context, conn *acp.AgentSideConnection, sid acp.SessionId, raw json.RawMessage, cwd string, cache toolUseCache, tracker *toolCallTracker, streamed *streamedBlockTracker, plan *taskPlan, parentToolUseID string) error {
	if len(raw) == 0 {
		return nil
	}
	var m cliMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("parse assistant message: %w", err)
	}
	content := m.Content
	if parentToolUseID == "" {
		content = streamed.consume(content)
	}
	for _, b := range content {
		var update acp.SessionUpdate
		switch b.Type {
		case "text":
			if b.Text == "" || parentToolUseID != "" {
				continue
			}
			text := b.Text
			if strings.Contains(text, "<local-command-") || strings.Contains(text, "<command-") {
				stripped, ok := stripMarkerTags(text)
				if !ok {
					continue
				}
				text = stripped
			}
			update = acp.UpdateAgentMessageText(text)
		case "thinking":
			if b.Thinking == "" || parentToolUseID != "" {
				continue
			}
			update = acp.UpdateAgentThoughtText(b.Thinking)
		case "tool_use":
			if cache != nil && b.ID != "" {
				cache[b.ID] = b.Name
			}
			if plan != nil {
				switch b.Name {
				case "TaskCreate":
					plan.noteCreate(b.ID, b.Input)
				case "TaskUpdate":
					if plan.noteUpdate(b.Input) {
						if err := conn.SessionUpdate(ctx, acp.SessionNotification{
							SessionId: sid,
							Update:    acp.UpdatePlan(plan.entries()...),
						}); err != nil {
							return err
						}
					}
				}
			}
			if plan != nil && isPlanTool(b.Name) {
				entries, ok := planEntriesFromTodoWrite(b.Input)
				if !ok {
					continue
				}
				update = acp.UpdatePlan(entries...)
				break
			}
			if err := emitToolUseCall(ctx, conn, sid, b, cwd, tracker, parentToolUseID); err != nil {
				return err
			}
			continue
		default:
			continue
		}
		if err := conn.SessionUpdate(ctx, acp.SessionNotification{
			SessionId: sid,
			Update:    update,
		}); err != nil {
			return err
		}
	}
	return nil
}

// emitToolUseCall surfaces a streamed tool_use block as a tool_call, unless a
// concurrent permission request for the same id already claimed it (see
// toolCallTracker), in which case it sends a tool_call_update that refines
// the eagerly-emitted call with the now-complete info instead of duplicating
// it.
func emitToolUseCall(ctx context.Context, conn *acp.AgentSideConnection, sid acp.SessionId, b cliMsgBlock, cwd string, tracker *toolCallTracker, parentToolUseID string) error {
	send := func(u acp.SessionUpdate) error {
		return conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: sid, Update: u})
	}
	start := func() error {
		u := toolCallStartUpdate(b.ID, b.Name, b.Input, cwd, acp.ToolCallStatusInProgress)
		withClaudeToolMeta(&u, b.Name, parentToolUseID)
		return send(u)
	}

	if b.ID == "" || tracker == nil || !shouldEmitToolCall(b.Name) {
		return start()
	}

	refine := func() error {
		u := toolCallRefineUpdate(b.ID, b.Name, b.Input, cwd, acp.ToolCallStatusInProgress)
		withClaudeToolMeta(&u, b.Name, parentToolUseID)
		return send(u)
	}
	return tracker.emit(b.ID, start, refine)
}

func emitToolResults(ctx context.Context, conn *acp.AgentSideConnection, sid acp.SessionId, raw json.RawMessage, cache toolUseCache, tracker *toolCallTracker, plan *taskPlan, parentToolUseID string) error {
	if len(raw) == 0 {
		return nil
	}
	var m cliMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	for _, b := range m.Content {
		if b.Type == "text" && parentToolUseID == "" && strings.Contains(b.Text, "<local-command-stdout>") {
			if text, ok := stripMarkerTags(b.Text); ok {
				if err := conn.SessionUpdate(ctx, acp.SessionNotification{
					SessionId: sid,
					Update:    acp.UpdateAgentMessageText(text),
				}); err != nil {
					return err
				}
			}
			continue
		}
		if b.Type != "tool_result" || b.ToolUseID == "" {
			continue
		}
		name := cache[b.ToolUseID]
		if plan != nil && name == "TaskCreate" && plan.completeCreate(b.ToolUseID, extractToolResultText(b.Content)) {
			if err := conn.SessionUpdate(ctx, acp.SessionNotification{
				SessionId: sid,
				Update:    acp.UpdatePlan(plan.entries()...),
			}); err != nil {
				return err
			}
		}
		if plan != nil && isPlanTool(name) {
			continue
		}
		// A cancelled turn can drop the assistant message that announced this
		// tool call while a late result still arrives. Never reference an id the
		// ACP client has not seen, and remove completed ids so late progress
		// heartbeats cannot reopen them.
		if tracker != nil && !tracker.complete(b.ToolUseID) {
			continue
		}
		status := acp.ToolCallStatusCompleted
		if b.IsError {
			status = acp.ToolCallStatusFailed
		}
		opts := []acp.ToolCallUpdateOpt{acp.WithUpdateStatus(status)}
		if content := toolResultContent(name, b); len(content) > 0 {
			opts = append(opts, acp.WithUpdateContent(content))
		}
		if len(b.Content) > 0 {
			var rawOutput any
			if json.Unmarshal(b.Content, &rawOutput) == nil {
				opts = append(opts, acp.WithUpdateRawOutput(rawOutput))
			}
		}
		u := acp.UpdateToolCall(acp.ToolCallId(b.ToolUseID), opts...)
		withClaudeToolMeta(&u, name, parentToolUseID)
		if err := conn.SessionUpdate(ctx, acp.SessionNotification{
			SessionId: sid,
			Update:    u,
		}); err != nil {
			return err
		}
	}
	return nil
}

func withClaudeToolMeta(update *acp.SessionUpdate, toolName, parentToolUseID string) {
	meta := map[string]any{"toolName": toolName}
	if parentToolUseID != "" {
		meta["parentToolUseId"] = parentToolUseID
	}
	root := map[string]any{"claudeCode": meta}
	switch {
	case update.ToolCall != nil:
		update.ToolCall.Meta = root
	case update.ToolCallUpdate != nil:
		update.ToolCallUpdate.Meta = root
	}
}

func toolResultContent(name string, b cliMsgBlock) []acp.ToolCallContent {
	if name == "Bash" && !b.IsError {
		if blocks, ok := bashImageResultBlocks(b.Content); ok {
			return blocks
		}
	}
	text := extractToolResultText(b.Content)
	if b.IsError {
		if text == "" {
			return nil
		}
		return []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(codeFence(text)))}
	}
	switch name {
	case "Read":
		if text == "" {
			return nil
		}
		return []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(markdownEscape(text)))}
	case "Bash":
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []acp.ToolCallContent{acp.ToolContent(acp.TextBlock("```console\n" + strings.TrimRight(text, "\n") + "\n```"))}
	case "Edit", "Write", "MultiEdit":
		return nil
	default:
		if text == "" {
			return nil
		}
		return []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(text))}
	}
}

// bashImageResultBlocks handles a Bash tool_result whose content array
// contains a non-text block (e.g. an image from a command piping a base64
// data URI). extractToolResultText only collects "text" blocks, so without
// this, image output is silently dropped. ok is false for text-only or
// non-array content, telling the caller to fall back to the normal
// text-extraction path (which keeps the existing console code-fence
// formatting for plain Bash output).
func bashImageResultBlocks(raw json.RawMessage) ([]acp.ToolCallContent, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var parts []cliMsgBlock
	if err := json.Unmarshal(raw, &parts); err != nil || len(parts) == 0 {
		return nil, false
	}

	textOnly := true
	for _, p := range parts {
		if p.Type != "text" {
			textOnly = false
			break
		}
	}
	if textOnly {
		return nil, false
	}

	var blocks []acp.ToolCallContent
	for _, p := range parts {
		switch p.Type {
		case "text":
			if p.Text != "" {
				blocks = append(blocks, acp.ToolContent(acp.TextBlock(p.Text)))
			}
		case "image":
			if p.Source != nil && p.Source.Data != "" {
				blocks = append(blocks, acp.ToolContent(acp.ImageBlock(p.Source.Data, p.Source.MediaType)))
			}
		}
	}
	if len(blocks) == 0 {
		return nil, false
	}
	return blocks, true
}

func codeFence(text string) string {
	return "```\n" + text + "\n```"
}

func extractToolResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []cliMsgBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var out strings.Builder
		for _, blk := range blocks {
			if blk.Type == "text" {
				out.WriteString(blk.Text)
			}
		}
		return out.String()
	}
	return string(raw)
}

func toolKindFor(name string) acp.ToolKind {
	switch name {
	case "Read":
		return acp.ToolKindRead
	case "Glob", "Grep":
		return acp.ToolKindSearch
	case "WebFetch", "WebSearch":
		return acp.ToolKindFetch
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		return acp.ToolKindEdit
	case "Bash", "BashOutput", "KillShell":
		return acp.ToolKindExecute
	case "Agent", "Task":
		return acp.ToolKindThink
	case "ExitPlanMode":
		return acp.ToolKindSwitchMode
	}
	return acp.ToolKindOther
}
