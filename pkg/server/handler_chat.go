package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman-agent/app/session"
	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/code"

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

	// Send current messages on connect
	messages := convertMessages(s.agent.Messages)
	if len(messages) > 0 {
		s.sendMessage(ServerMessage{Type: MsgMessages, Messages: messages})
	}

	// Send current usage
	usage := s.agent.Usage
	if usage.InputTokens > 0 || usage.OutputTokens > 0 {
		s.sendMessage(ServerMessage{
			Type:         MsgUsage,
			InputTokens:  usage.InputTokens,
			OutputTokens: usage.OutputTokens,
		})
	}

	// Send model info
	s.sendMessage(ServerMessage{Type: MsgPhase, Phase: "idle"})

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
		input = append(input, agent.Content{Text: msg.Text})
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

	s.sendMessage(ServerMessage{Type: MsgPhase, Phase: "thinking"})

	for msg, err := range s.agent.Send(streamCtx, input) {
		if err != nil {
			if errors.Is(err, context.Canceled) {
				s.sendMessage(ServerMessage{Type: MsgError, Message: "Cancelled"})
			} else {
				s.sendMessage(ServerMessage{Type: MsgError, Message: err.Error()})
			}
			break
		}

		for _, c := range msg.Content {
			switch {
			case c.ToolCall != nil:
				hint := extractToolHintFromArgs(c.ToolCall.Args)
				s.sendMessage(ServerMessage{
					Type: MsgToolCall,
					ID:   c.ToolCall.ID,
					Name: c.ToolCall.Name,
					Args: c.ToolCall.Args,
					Hint: hint,
				})
				s.sendMessage(ServerMessage{Type: MsgPhase, Phase: "tool_running"})

			case c.ToolResult != nil:
				s.sendMessage(ServerMessage{
					Type:    MsgToolResult,
					ID:      c.ToolResult.ID,
					Name:    c.ToolResult.Name,
					Content: c.ToolResult.Content,
				})

			case c.Text != "":
				s.sendMessage(ServerMessage{Type: MsgPhase, Phase: "streaming"})
				s.sendMessage(ServerMessage{Type: MsgTextDelta, Text: c.Text})
			}
		}

		// Send usage updates
		usage := s.agent.Usage
		s.sendMessage(ServerMessage{
			Type:         MsgUsage,
			InputTokens:  usage.InputTokens,
			OutputTokens: usage.OutputTokens,
		})
	}

	// Commit to rewind
	select {
	case <-s.rewindReady:
		if s.rewind != nil {
			commitMsg := msg.Text
			if commitMsg == "" {
				commitMsg = "<unknown>"
			}
			if err := s.rewind.Commit(commitMsg); err == nil {
				s.sendMessage(ServerMessage{Type: MsgCheckpointsChanged})
			}
			// fsnotify will likely fire too, but push explicitly to avoid
			// any race where the UI fetches before the watcher debounces.
			s.sendMessage(ServerMessage{Type: MsgDiffsChanged})
			s.sendMessage(ServerMessage{Type: MsgFilesChanged})
		}
	default:
	}

	// Diagnostics most often change as a consequence of agent edits — nudge
	// the panel so users see fresh problems without reopening the tab.
	s.sendMessage(ServerMessage{Type: MsgDiagnosticsChanged})

	// Save session and notify the sidebar so the new/updated entry shows up
	// without waiting for the periodic poll.
	state := agent.State{
		Messages: s.agent.Messages,
		Usage:    s.agent.Usage,
	}
	if err := session.Save(s.sessionsDir, s.sessionID, state); err == nil && len(state.Messages) > 0 {
		s.sendMessage(ServerMessage{Type: MsgSessionsChanged})
	}

	s.sendMessage(ServerMessage{Type: MsgDone})
	s.sendMessage(ServerMessage{Type: MsgPhase, Phase: "idle"})
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
