package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/adrianliechti/wingman-agent/pkg/session"

	"github.com/coder/websocket"
)

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // Allow connections from any origin
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()

	// Set as active connection
	s.wsMu.Lock()
	s.wsConn = conn
	s.wsMu.Unlock()

	defer func() {
		s.wsMu.Lock()
		s.wsConn = nil
		s.wsMu.Unlock()
	}()

	// Send current session id so the client can match it against sidebar
	// entries (e.g. to detect when the user deletes the active session).
	s.sendMessage(SessionEvent{ID: s.sessionID})

	// Send current messages on connect
	messages := convertMessages(s.agent.Messages)
	if len(messages) > 0 {
		s.sendMessage(MessagesEvent{Messages: messages})
	}

	// Send current usage
	usage := s.agent.Usage
	if usage.InputTokens > 0 || usage.OutputTokens > 0 {
		s.sendMessage(UsageEvent{
			InputTokens:  usage.InputTokens,
			OutputTokens: usage.OutputTokens,
		})
	}

	// Send model info
	s.sendMessage(PhaseEvent{Phase: "idle"})

	ctx := r.Context()

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}

		var msg ClientMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case MsgSend:
			go s.handleSend(ctx, msg)

		case MsgCancel:
			s.wsMu.Lock()
			if s.streamCancel != nil {
				s.streamCancel()
			}
			s.wsMu.Unlock()

		case MsgPromptResponse:
			select {
			case s.promptCh <- msg.Approved:
			default:
			}

		case MsgAskResponse:
			select {
			case s.askCh <- msg.Answer:
			default:
			}
		}
	}
}

func (s *Server) handleSend(ctx context.Context, msg ClientMessage) {
	var input []agent.Content

	if msg.Text != "" {
		// If the message starts with a slash command that matches one of the
		// agent's skills, replace the user text with the rendered skill content
		// (mirrors the CLI's invokeSkill flow in app/app_ui.go:652).
		text := s.resolveSkill(msg.Text)
		input = append(input, agent.Content{Text: text})
	}

	// Add file references as context
	for _, f := range msg.Files {
		input = append(input, agent.Content{Text: fmt.Sprintf("[File: %s]", f)})
	}

	streamCtx, cancel := context.WithCancel(ctx)

	s.wsMu.Lock()
	s.streamCancel = cancel
	s.wsMu.Unlock()

	defer func() {
		s.wsMu.Lock()
		s.streamCancel = nil
		s.wsMu.Unlock()
	}()

	// Track the last sent phase so we only emit transitions, not one frame
	// per content chunk. The client just calls setPhase on each event, so
	// repeats are pure noise.
	currentPhase := ""
	setPhase := func(p string) {
		if p == currentPhase {
			return
		}
		currentPhase = p
		s.sendMessage(PhaseEvent{Phase: p})
	}

	setPhase("thinking")

	for msg, err := range s.agent.Send(streamCtx, input) {
		if err != nil {
			if errors.Is(err, context.Canceled) {
				s.sendMessage(ErrorEvent{Message: "Cancelled"})
			} else {
				s.sendMessage(ErrorEvent{Message: err.Error()})
			}
			break
		}

		for _, c := range msg.Content {
			switch {
			case c.ToolCall != nil:
				s.sendMessage(ToolCallEvent{
					ID:   c.ToolCall.ID,
					Name: c.ToolCall.Name,
					Args: c.ToolCall.Args,
					Hint: extractToolHintFromArgs(c.ToolCall.Args),
				})
				setPhase("tool_running")

			case c.ToolResult != nil:
				s.sendMessage(ToolResultEvent{
					ID:      c.ToolResult.ID,
					Name:    c.ToolResult.Name,
					Content: c.ToolResult.Content,
				})

			case c.Reasoning != nil && c.Reasoning.Summary != "":
				setPhase("thinking")
				s.sendMessage(ReasoningDeltaEvent{
					ID:   c.Reasoning.ID,
					Text: c.Reasoning.Summary,
				})

			case c.Text != "":
				setPhase("streaming")
				s.sendMessage(TextDeltaEvent{Text: c.Text})
			}
		}

		// Send usage updates
		usage := s.agent.Usage
		s.sendMessage(UsageEvent{
			InputTokens:  usage.InputTokens,
			OutputTokens: usage.OutputTokens,
		})
	}

	// The agent likely touched files this turn — the FileTree refetches
	// even in scratch mode, where there's no watcher to fire this for us.
	s.sendMessage(FilesChangedEvent{})

	// Refresh diffs and diagnostics on supported workspaces. The checkpoint
	// commit walks the worktree and would block turn-end on a large repo,
	// so run it in the background and announce once it lands.
	if s.agent.Rewind != nil {
		commitMsg := msg.Text
		if commitMsg == "" {
			commitMsg = "<unknown>"
		}
		go func() {
			if err := s.agent.Rewind.Commit(commitMsg); err == nil {
				s.sendMessage(CheckpointsChangedEvent{})
			}
		}()
		s.sendMessage(DiffsChangedEvent{})
	}
	if s.agent.LSP != nil {
		s.sendMessage(DiagnosticsChangedEvent{})
	}

	// Save session and notify the sidebar so the new/updated entry shows up
	// without waiting for the periodic poll.
	state := agent.State{
		Messages: s.agent.Messages,
		Usage:    s.agent.Usage,
	}
	if err := session.Save(s.sessionsDir, s.sessionID, state); err == nil && len(state.Messages) > 0 {
		s.sendMessage(SessionsChangedEvent{})
	}

	s.sendMessage(DoneEvent{})
	setPhase("idle")
}

// extractToolHintFromArgs extracts a display hint from tool arguments JSON.
func extractToolHintFromArgs(argsJSON string) string {
	var args map[string]any

	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ""
	}

	if desc, ok := args["description"]; ok {
		if str, ok := desc.(string); ok && str != "" {
			return strings.Join(strings.Fields(str), " ")
		}
	}

	hintKeys := []string{"query", "pattern", "command", "prompt", "path", "file", "url", "name"}

	for _, key := range hintKeys {
		if val, ok := args[key]; ok {
			if str, ok := val.(string); ok && str != "" {
				return strings.Join(strings.Fields(str), " ")
			}
		}
	}

	return ""
}

func (s *Server) autoSelectModel(ctx context.Context) {
	if s.agent.Config.Model != nil && s.agent.Model() != "" {
		return
	}

	models, err := s.agent.Models(ctx)
	if err != nil {
		return
	}

	for _, allowed := range code.AvailableModels {
		for _, model := range models {
			if model.ID == allowed.ID {
				modelID := model.ID
				s.agent.Config.Model = func() string { return modelID }
				return
			}
		}
	}

	if len(models) > 0 {
		modelID := models[0].ID
		s.agent.Config.Model = func() string { return modelID }
	}
}
