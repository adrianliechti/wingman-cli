package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	codeagent "github.com/adrianliechti/wingman-agent/pkg/code/agent"
)

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()

	conn.SetReadLimit(32 << 20)

	client := newWSClient(conn)
	s.wsMu.Lock()
	s.wsConns[conn] = client
	s.wsMu.Unlock()

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		client.run()
	}()

	defer func() {
		s.wsMu.Lock()
		delete(s.wsConns, conn)
		s.wsMu.Unlock()
		client.close()
		<-writerDone
	}()

	for {
		_, data, err := conn.Read(r.Context())
		if err != nil {
			return
		}

		var msg ClientMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case MsgSend:
			if msg.SessionID == "" {
				continue
			}
			s.handleSend(r.Context(), msg)

		case MsgCancel:
			if msg.SessionID == "" {
				continue
			}
			if _, turns := s.activeRuntime(); turns != nil {
				if msg.ClearQueue {
					turns.CancelAll(msg.SessionID)
				} else {
					turns.CancelCurrent(msg.SessionID)
				}
				s.sendTurnSnapshot(msg.SessionID)
			}

		case MsgQueueRemove:
			if _, turns := s.activeRuntime(); turns != nil && msg.SessionID != "" && msg.ID != "" {
				if err := turns.RemoveQueued(msg.SessionID, msg.ID); err != nil {
					s.sendTurnInputError(msg.SessionID, msg.ID, err)
				}
			}

		case MsgQueueUpdate:
			s.handleQueueUpdate(msg)

		case MsgQueueResume:
			if _, turns := s.activeRuntime(); turns != nil && msg.SessionID != "" {
				turns.Resume(msg.SessionID)
				s.sendTurnSnapshot(msg.SessionID)
			}

		case MsgQueueClear:
			if _, turns := s.activeRuntime(); turns != nil && msg.SessionID != "" {
				turns.ClearQueue(msg.SessionID)
				s.sendTurnSnapshot(msg.SessionID)
			}

		case MsgSync:
			for _, sid := range msg.Sessions {
				if sid == "" {
					continue
				}
				s.sendSessionState(sid)
			}

		case MsgPromptResponse:
			if msg.PromptID == "" {
				continue
			}
			s.resolvePrompt(msg)

		case MsgFocus:

			s.files.Notify()
		}
	}
}

func (s *Server) buildInput(msg ClientMessage) []agent.Content {
	var input []agent.Content
	if msg.Text != "" {
		input = append(input, agent.Content{Text: msg.Text})
		for _, block := range s.skillBlocks(msg.Text) {
			input = append(input, agent.Content{Text: block, Hidden: true})
		}
	}
	for _, f := range msg.Files {
		input = append(input, agent.Content{Text: fmt.Sprintf("[File: %s]", f)})
	}
	for _, img := range msg.Images {
		if img == "" {
			continue
		}
		input = append(input, agent.Content{File: &agent.File{Data: img}})
	}
	return input
}

func (s *Server) handleSend(ctx context.Context, msg ClientMessage) {
	_, turns := s.activeRuntime()
	if turns == nil {
		return
	}
	if msg.ID == "" {
		msg.ID = uuid.NewString()
	}
	intent := code.TurnInputIntent(msg.Intent)
	if intent != code.TurnInputSteer {
		intent = code.TurnInputFollowUp
	}
	msg.Intent = string(intent)
	s.storeTurnMeta(msg)
	_, err := turns.Submit(ctx, msg.SessionID, code.TurnInput{
		ID: msg.ID, Content: s.buildInput(msg), Intent: intent,
	})
	if err != nil {
		s.sendTurnInputError(msg.SessionID, msg.ID, err)
		s.deleteTurnMeta(msg.SessionID, msg.ID)
		return
	}
	s.sendTurnSnapshot(msg.SessionID)
	s.ensureTaskPump(msg.SessionID)
}

func (s *Server) handleTurnEvent(ev code.TurnEvent) {
	if ev.Message != nil {
		for _, c := range ev.Message.Content {
			switch {
			case c.ToolCall != nil:
				s.sendSession(ev.SessionID, Frame{
					Type: EvtToolCall,
					ID:   c.ToolCall.ID,
					Name: c.ToolCall.Name,
					Args: c.ToolCall.Args,
					Hint: tool.ExtractHint(c.ToolCall.Args, c.ToolCall.Name),
				})
				s.setSessionPhase(ev.SessionID, "tool_running")

			case c.ToolResult != nil:
				s.sendSession(ev.SessionID, Frame{
					Type:    EvtToolResult,
					ID:      c.ToolResult.ID,
					Name:    c.ToolResult.Name,
					Content: c.ToolResult.Content,
				})

				s.files.Notify()

			case c.Reasoning != nil && c.Reasoning.Summary != "":
				s.setSessionPhase(ev.SessionID, "thinking")
				s.sendSession(ev.SessionID, Frame{
					Type: EvtReasoningDelta,
					ID:   c.Reasoning.ID,
					Part: c.Reasoning.Part,
					Text: c.Reasoning.Summary,
				})

			case c.Text != "":
				s.setSessionPhase(ev.SessionID, "streaming")
				s.sendSession(ev.SessionID, Frame{Type: EvtTextDelta, Text: c.Text})
			}
		}
		s.sendUsageIfChanged(ev.SessionID)
		return
	}

	if ev.State == "" {
		return
	}
	if ev.Intent != "" {
		s.updateTurnIntent(ev.SessionID, ev.InputID, ev.Intent)
	}
	s.sendTurnInputStatus(ev)

	switch ev.State {
	case code.TurnInputActive:
		s.setSessionPhase(ev.SessionID, "thinking")
	case code.TurnInputFailed:
		if ev.Err != nil && !errors.Is(ev.Err, context.Canceled) {
			s.sendSession(ev.SessionID, Frame{Type: EvtError, Message: ev.Err.Error()})
		}
	case code.TurnInputCancelled:
		if ev.Executed {
			s.sendSession(ev.SessionID, Frame{Type: EvtError, Message: "Cancelled"})
		}
	}

	if ev.Executed {
		s.finalizeTurn(ev.SessionID)
	}
	if ev.State == code.TurnInputCompleted || ev.State == code.TurnInputCancelled || ev.State == code.TurnInputFailed {
		s.deleteTurnMeta(ev.SessionID, ev.InputID)
	}
	s.sendTurnSnapshot(ev.SessionID)
}

func (s *Server) finalizeTurn(sid string) {
	a := s.activeAgent()
	if a == nil {
		return
	}
	s.sendUsageIfChanged(sid)

	ws := s.workspace
	s.flushFiles()

	if ws.HasLSP() {
		s.broadcast(Frame{Type: EvtDiagnosticsChanged})
	}

	saved := true
	if w, ok := a.(*codeagent.Agent); ok {
		saved = w.Save(sid) == nil
	}
	if saved && len(a.Messages(sid)) > 0 {
		s.broadcast(Frame{Type: EvtSessionsChanged})
	}

	_, turns := s.activeRuntime()
	if turns == nil || !snapshotHasActive(turns.Snapshot(sid)) {
		s.setSessionPhase(sid, "idle")
	}
}

func snapshotHasActive(snapshot code.TurnSnapshot) bool {
	for _, input := range snapshot.Inputs {
		if input.State == code.TurnInputActive || input.State == code.TurnInputSteered {
			return true
		}
	}
	return false
}

func (s *Server) handleQueueUpdate(msg ClientMessage) {
	if msg.SessionID == "" || msg.ID == "" {
		return
	}
	_, turns := s.activeRuntime()
	if turns == nil {
		return
	}
	previous, ok := s.getTurnMeta(msg.SessionID, msg.ID)
	if !ok {
		s.sendTurnInputError(msg.SessionID, msg.ID, code.ErrInputNotQueued)
		return
	}
	msg.Type = MsgSend
	msg.Intent = string(code.TurnInputFollowUp)
	s.storeTurnMeta(msg)
	err := turns.ReplaceQueued(msg.SessionID, msg.ID, code.TurnInput{
		ID: msg.ID, Content: s.buildInput(msg), Intent: code.TurnInputFollowUp,
	})
	if err != nil {
		s.storeTurnMeta(previous)
		s.sendTurnInputError(msg.SessionID, msg.ID, err)
		return
	}
	s.sendTurnSnapshot(msg.SessionID)
}

func (s *Server) sendTurnInputStatus(ev code.TurnEvent) {
	meta, _ := s.getTurnMeta(ev.SessionID, ev.InputID)
	s.sendSession(ev.SessionID, turnInputFrame(ev.InputID, meta, ev.State, ev.Position, ev.Err))
}

func (s *Server) sendTurnInputError(sessionID, inputID string, err error) {
	meta, _ := s.getTurnMeta(sessionID, inputID)
	s.sendSession(sessionID, turnInputFrame(inputID, meta, code.TurnInputFailed, 0, err))
}

func turnInputFrame(inputID string, meta ClientMessage, state code.TurnInputState, position int, err error) Frame {
	meta.ID = inputID
	entry := turnQueueEntry(meta, state, position)
	return Frame{
		Type: EvtTurnInput, ID: entry.ID, State: entry.State,
		Intent: entry.Intent, Position: entry.Position, Text: entry.Text,
		Message: errorText(err), Queue: []TurnQueueEntry{entry},
	}
}

func (s *Server) sendTurnSnapshot(sessionID string) {
	_, turns := s.activeRuntime()
	if turns == nil {
		return
	}
	snapshot := turns.Snapshot(sessionID)
	queue := make([]TurnQueueEntry, 0, len(snapshot.Inputs))
	for _, input := range snapshot.Inputs {
		meta, _ := s.getTurnMeta(sessionID, input.ID)
		queue = append(queue, turnQueueEntry(meta, input.State, input.Position))
	}
	s.sendSession(sessionID, Frame{
		Type: EvtTurnQueue, Queue: queue, Paused: snapshot.Paused,
		CanSteer: snapshot.Features.Steer,
	})
}

func turnQueueEntry(meta ClientMessage, state code.TurnInputState, position int) TurnQueueEntry {
	return TurnQueueEntry{
		ID: meta.ID, State: string(state), Intent: meta.Intent, Position: position,
		Text: meta.Text, Files: append([]string(nil), meta.Files...),
		Images: append([]string(nil), meta.Images...), ImageCount: len(meta.Images),
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *Server) storeTurnMeta(msg ClientMessage) {
	msg.Files = append([]string(nil), msg.Files...)
	msg.Images = append([]string(nil), msg.Images...)
	s.turnMetaMu.Lock()
	byID := s.turnMeta[msg.SessionID]
	if byID == nil {
		byID = map[string]ClientMessage{}
		s.turnMeta[msg.SessionID] = byID
	}
	byID[msg.ID] = msg
	s.turnMetaMu.Unlock()
}

func (s *Server) getTurnMeta(sessionID, inputID string) (ClientMessage, bool) {
	s.turnMetaMu.Lock()
	defer s.turnMetaMu.Unlock()
	msg, ok := s.turnMeta[sessionID][inputID]
	return msg, ok
}

func (s *Server) updateTurnIntent(sessionID, inputID string, intent code.TurnInputIntent) {
	s.turnMetaMu.Lock()
	if byID := s.turnMeta[sessionID]; byID != nil {
		msg := byID[inputID]
		msg.Intent = string(intent)
		byID[inputID] = msg
	}
	s.turnMetaMu.Unlock()
}

func (s *Server) deleteTurnMeta(sessionID, inputID string) {
	s.turnMetaMu.Lock()
	if byID := s.turnMeta[sessionID]; byID != nil {
		delete(byID, inputID)
		if len(byID) == 0 {
			delete(s.turnMeta, sessionID)
		}
	}
	s.turnMetaMu.Unlock()
}

func (s *Server) sendUsageIfChanged(sessionID string) {
	a := s.activeAgent()
	if a == nil {
		return
	}
	u := a.Usage(sessionID)
	s.turnMetaMu.Lock()
	previous := s.turnUsage[sessionID]
	if u == previous {
		s.turnMetaMu.Unlock()
		return
	}
	s.turnUsage[sessionID] = u
	s.turnMetaMu.Unlock()
	s.sendSession(sessionID, s.usageFrame(a, sessionID, u))
}

func (s *Server) usageFrame(a code.Agent, sid string, u agent.Usage) Frame {
	f := Frame{
		Type:         EvtUsage,
		InputTokens:  u.InputTokens,
		CachedTokens: u.CachedTokens,
		OutputTokens: u.OutputTokens,

		LastInputTokens: u.LastInputTokens,
		ContextWindow:   u.ContextWindow,
	}

	if f.ContextWindow <= 0 && u.LastInputTokens > 0 {
		_, model := a.Models(sid)
		f.ContextWindow = int64(agent.ContextWindowFor(model, false))
	}

	return f
}
