package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/code"
)

func (s *backendRuntime) buildInput(msg Command) []agent.Content {
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

func (s *backendRuntime) handleTurnEvent(ev code.TurnEvent) {
	if ev.StreamEvent != 0 {
		switch ev.StreamEvent {
		case agent.StreamEventReset:
			s.sendSession(ev.SessionID, Frame{Type: EvtStreamReset})
		case agent.StreamEventCommit:
			s.session(ev.SessionID).replaceHistory()
		}
		return
	}

	if ev.Message != nil {
		if ev.Message.Hidden || ev.Message.Role == agent.RoleUser {
			return
		}
		for _, c := range ev.Message.Content {
			if c.Hidden {
				continue
			}
			switch {
			case c.ToolCall != nil:
				display := displayTool(
					c.ToolCall.Name, c.ToolCall.Kind, c.ToolCall.Args, c.ToolCall.Locations,
					c.ToolCall.Presentation,
				)
				s.sendSession(ev.SessionID, Frame{
					Type:      EvtToolCall,
					ID:        c.ToolCall.ID,
					Name:      display.name,
					Kind:      display.kind,
					Args:      display.args,
					Locations: display.locations,
					Hint:      display.hint,
					Partial:   c.ToolCall.Partial,
				})
				if c.ToolCall.Partial {
					s.setSessionPhase(ev.SessionID, "thinking")
				} else {
					s.setSessionPhase(ev.SessionID, "tool_running")
				}

			case c.ToolResult != nil:
				display := displayTool(
					c.ToolResult.Name, c.ToolResult.Kind, c.ToolResult.Args, c.ToolResult.Locations,
					c.ToolResult.Presentation,
				)
				s.sendSession(ev.SessionID, Frame{
					Type:      EvtToolResult,
					ID:        c.ToolResult.ID,
					Name:      display.name,
					Kind:      display.kind,
					Args:      display.args,
					Locations: display.locations,
					Hint:      display.hint,
					Content:   c.ToolResult.Content,
				})

				if s.files != nil {
					s.files.Notify()
				}
				s.sendSession(ev.SessionID, Frame{Type: EvtTasksChanged})

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
				s.sendSession(ev.SessionID, Frame{Type: EvtTextDelta, ID: c.TextID, Text: c.Text})
			}
		}
		s.sendUsageIfChanged(ev.SessionID)
		return
	}

	if ev.State == "" {
		return
	}
	entry := turnQueueEntry(ev.Input, ev.State, ev.Position)
	s.sendSession(ev.SessionID, Frame{Type: EvtTurnInput, Input: &entry})

	switch ev.State {
	case code.TurnInputActive:
		s.setSessionPhase(ev.SessionID, "thinking")
		s.broadcast(Frame{Type: EvtSessionsChanged})
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
	s.sendTurnSnapshot(ev.SessionID)
}

func (s *backendRuntime) finalizeTurn(sid string) {
	a := s.agent
	if a == nil {
		return
	}
	s.session(sid).replaceHistory()
	s.sendUsageIfChanged(sid)

	ws := s.workspace
	s.flushFiles()

	if ws != nil && ws.HasLSP() {
		s.broadcast(Frame{Type: EvtDiagnosticsChanged})
	}

	// Persistence belongs to the backend's journal, including partial and
	// interrupted turns. Web finalization only refreshes resource projections.
	if len(a.Messages(sid)) > 0 {
		s.broadcast(Frame{Type: EvtSessionsChanged})
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

func (s *backendRuntime) sendTurnSnapshot(sessionID string) {
	if s.turns == nil {
		return
	}
	c := s.session(sessionID)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.Status == "deleted" {
		return
	}
	snapshot := s.turns.Snapshot(sessionID)
	queue := make([]TurnQueueEntry, 0, len(snapshot.Inputs))
	for _, input := range snapshot.Inputs {
		// Accepted active/steered inputs already belong to the transcript. Only
		// waiting user inputs are editable in the queue panel.
		if input.State != code.TurnInputQueued || input.Input.Origin == "task" {
			continue
		}
		queue = append(queue, turnQueueEntry(input.Input, input.State, input.Position))
	}
	c.state.PendingInputs = queue
	c.state.QueuePaused = snapshot.Paused
	c.state.CanSteer = snapshot.Features.Steer
	if snapshot.Error != nil {
		message := snapshot.Error.Error()
		c.state.Error = &message
	}
	if !snapshotHasActive(snapshot) {
		c.state.Phase = "idle"
	} else if c.state.Phase == "idle" {
		c.state.Phase = "thinking"
	}
	c.publishStateLocked()
}

func turnQueueEntry(input code.TurnInput, state code.TurnInputState, position int) TurnQueueEntry {
	display := visibleInput(code.CloneTurnInput(input))
	return TurnQueueEntry{ID: input.ID, State: string(state), Intent: string(input.Intent), Position: position, Text: display.Text, Files: display.Files, Images: display.Images, Origin: input.Origin}
}

func (s *backendRuntime) sendUsageIfChanged(sessionID string) {
	s.session(sessionID).refreshSettings()
}
