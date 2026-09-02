package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
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
					plan.noteUpdate(b.ID, b.Input)
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

	if b.ID == "" || tracker == nil || !shouldTrackToolCall(b.Name) {
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
		if applyTaskPlanResult(plan, name, b) {
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

func applyTaskPlanResult(plan *taskPlan, name string, result cliMsgBlock) bool {
	if plan == nil {
		return false
	}
	text := extractToolResultText(result.Content)
	switch name {
	case "TaskCreate":
		return plan.completeCreate(result.ToolUseID, text, result.IsError)
	case "TaskUpdate":
		return plan.completeUpdate(result.ToolUseID, text, result.IsError)
	case "TaskList":
		return plan.applyTaskList(text, result.IsError)
	default:
		return false
	}
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
	text, parts, isArray := decodeToolResult(b.Content)
	subagent := (name == "Agent" || name == "Task") && !b.IsError
	if isArray {
		if subagent || hasNonTextResultBlock(parts) {
			return resultBlocks(parts, name, b.IsError, subagent)
		}
		text = resultText(parts)
	} else if subagent {
		text = replacePartialOutputNote(text)
	}
	return textToolResultContent(name, text, b.IsError)
}

func textToolResultContent(name, text string, isError bool) []acp.ToolCallContent {
	if isError {
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

func resultBlocks(parts []cliMsgBlock, name string, isError, subagent bool) []acp.ToolCallContent {
	blocks := make([]acp.ToolCallContent, 0, len(parts))
	firstText := true
	for _, part := range parts {
		switch part.Type {
		case "text":
			if part.Text == "" {
				continue
			}
			text := part.Text
			if subagent && firstText {
				text = replacePartialOutputNote(text)
			}
			firstText = false
			blocks = append(blocks, acp.ToolContent(acp.TextBlock(formatResultText(name, text, isError))))
		case "image":
			if part.Source == nil {
				blocks = append(blocks, acp.ToolContent(acp.TextBlock(formatRichResultText("[image: file reference]", isError))))
				continue
			}
			switch {
			case part.Source.Type == "base64" && part.Source.Data != "":
				blocks = append(blocks, acp.ToolContent(acp.ImageBlock(part.Source.Data, part.Source.MediaType)))
			case part.Source.Type == "base64":
				continue
			case part.Source.Type == "url" && strings.TrimSpace(part.Source.URL) != "":
				blocks = append(blocks, acp.ToolContent(acp.TextBlock(formatRichResultText("[image: "+part.Source.URL+"]", isError))))
			default:
				blocks = append(blocks, acp.ToolContent(acp.TextBlock(formatRichResultText("[image: file reference]", isError))))
			}
		case "document":
			blocks = append(blocks, acp.ToolContent(acp.TextBlock(formatRichResultText(documentPlaceholder(part), isError))))
		}
	}
	return blocks
}

func formatResultText(name, text string, isError bool) string {
	if isError {
		return codeFence(text)
	}
	if name == "Read" {
		return markdownEscape(text)
	}
	return text
}

func formatRichResultText(text string, isError bool) string {
	if isError {
		return codeFence(text)
	}
	return text
}

var partialOutputNotePattern = regexp.MustCompile(`^NOTE: this agent stopped at its [0-9]+-turn limit before finishing\.`)

const partialOutputLabel = "[Agent stopped at its turn limit — the output below is partial]"

func replacePartialOutputNote(text string) string {
	if !partialOutputNotePattern.MatchString(text) {
		return text
	}
	_, report, found := strings.Cut(text, "\n\n")
	if !found {
		return partialOutputLabel
	}
	report = strings.TrimLeft(report, " \t\r\n")
	if report == "" {
		return partialOutputLabel
	}
	return partialOutputLabel + "\n\n" + report
}

func documentPlaceholder(block cliMsgBlock) string {
	title := sanitizeDocumentLabel(block.Title, 120)
	if title != "" {
		title = ` "` + strings.ReplaceAll(title, `"`, `'`) + `"`
	}
	if block.Source == nil {
		return "[document" + title + "]"
	}

	source := block.Source
	switch source.Type {
	case "url":
		url := sanitizeDocumentLabel(source.URL, 300)
		if url == "" || strings.HasPrefix(strings.ToLower(strings.TrimSpace(url)), "data:") {
			return "[document" + title + "]"
		}
		return "[document" + title + ": " + url + "]"
	case "base64", "text":
		mediaType := sanitizeDocumentLabel(source.MediaType, 80)
		if mediaType == "" {
			mediaType = "document"
		}
		size := len(source.Data)
		if source.Type == "base64" {
			size = decodedBase64Size(source.Data)
		}
		return fmt.Sprintf("[document%s: %s, %s]", title, mediaType, formatByteSize(size))
	default:
		return "[document" + title + "]"
	}
}

func sanitizeDocumentLabel(value string, maxRunes int) string {
	value = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, value))
	if len(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes-1]) + "…"
	}
	return value
}

func decodedBase64Size(data string) int {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" {
		return 0
	}
	size := len(trimmed) * 3 / 4
	if strings.HasSuffix(trimmed, "==") {
		size -= 2
	} else if strings.HasSuffix(trimmed, "=") {
		size--
	}
	if size < 0 {
		return 0
	}
	return size
}

func formatByteSize(size int) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	value := float64(size) / 1024
	unit := units[0]
	for _, next := range units[1:] {
		if value < 1024 {
			break
		}
		value /= 1024
		unit = next
	}
	if value >= 10 {
		return fmt.Sprintf("%.0f %s", value, unit)
	}
	return fmt.Sprintf("%.1f %s", value, unit)
}

func decodeToolResult(raw json.RawMessage) (string, []cliMsgBlock, bool) {
	if len(raw) == 0 {
		return "", nil, false
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, nil, false
	}
	var parts []cliMsgBlock
	if json.Unmarshal(raw, &parts) == nil {
		return "", parts, true
	}
	return string(raw), nil, false
}

func hasNonTextResultBlock(parts []cliMsgBlock) bool {
	for _, part := range parts {
		if part.Type != "text" {
			return true
		}
	}
	return false
}

func resultText(parts []cliMsgBlock) string {
	var out strings.Builder
	for _, part := range parts {
		if part.Type == "text" {
			out.WriteString(part.Text)
		}
	}
	return out.String()
}

func codeFence(text string) string {
	return "```\n" + text + "\n```"
}

func extractToolResultText(raw json.RawMessage) string {
	text, parts, isArray := decodeToolResult(raw)
	if isArray {
		return resultText(parts)
	}
	return text
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
