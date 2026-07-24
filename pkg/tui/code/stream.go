package code

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

func (a *App) getPhase() AppPhase {
	return AppPhase(a.phase.Load())
}

func (a *App) setPhase(phase AppPhase) {
	prev := AppPhase(a.phase.Swap(int32(phase)))

	if phase == PhaseIdle {
		// A quit confirmation armed while cancelling a turn must not carry
		// over once that turn has died.
		a.disarmQuitGate()
	}
	if phase != PhaseIdle && (prev == PhaseIdle || a.phaseStart.IsZero()) {
		a.phaseStart = time.Now()
	}
	if phase != PhaseIdle && a.turnStart.IsZero() {
		a.turnStart = time.Now()
	}
}

// queuePhase updates the phase from agent goroutines and schedules a repaint.
func (a *App) queuePhase(phase AppPhase) {
	a.post(func() {
		a.setPhase(phase)
		a.invalidate()
	})
}

// syncMessages flushes newly committed messages to scrollback.
func (a *App) syncMessages() {
	messages := a.agent.Messages(a.sessionID)

	if a.printed > len(messages) {
		a.printed = 0
	}
	if a.printed == len(messages) {
		return
	}

	width := a.width()
	var lines []string

	for i := a.printed; i < len(messages); i++ {
		lines = append(lines, a.formatMessageCells(messages[i], width)...)
	}
	a.printed = len(messages)

	if len(lines) > 0 {
		a.appendChat(lines)
	}

	usage := a.agent.Usage(a.sessionID)
	a.inputTokens = usage.InputTokens
	a.outputTokens = usage.OutputTokens
	a.lastInputTokens = usage.LastInputTokens
}

func (a *App) formatMessageCells(msg agent.Message, width int) []string {
	if msg.Hidden || msg.Role == agent.RoleSystem {
		return nil
	}

	var lines []string

	for _, c := range msg.Content {
		if c.Hidden {
			continue
		}
		switch {
		case c.ToolResult != nil:
			a.releaseToolCell(c.ToolResult)
			if a.isToolHidden(c.ToolResult.Name) {
				continue
			}
			cell := cellTool(c.ToolResult, width, false)
			if a.flow.beforeTool(len(cell) > 1) {
				lines = append(lines, "")
			}
			lines = append(lines, cell...)

		case c.ToolCall != nil:
			continue

		case c.Reasoning != nil && c.Reasoning.Summary != "":
			cell := cellReasoning(c.Reasoning.Summary, width, true)
			if a.flow.beforeThought(len(cell) > 1) {
				lines = append(lines, "")
			}
			lines = append(lines, cell...)

		case strings.TrimSpace(c.Text) != "":
			if a.flow.gap() {
				lines = append(lines, "")
			}
			switch msg.Role {
			case agent.RoleUser:
				a.removePendingEchoText(c.Text)
				if isCommandEcho(c.Text) {
					lines = append(lines, cellCommand(c.Text, width)...)
				} else {
					lines = append(lines, cellUser(c.Text, width)...)
				}
			case agent.RoleAssistant:
				lines = append(lines, cellAssistant(c.Text, width, theme.Default.Green)...)
			}
		}
	}

	return lines
}

func (a *App) removePendingEchoText(text string) {
	for i, item := range a.pendingEcho {
		if item.Text == text {
			a.pendingEcho = append(a.pendingEcho[:i], a.pendingEcho[i+1:]...)
			return
		}
	}
}

// releaseToolCell drops the live tool cell once its committed result reaches
// the chat.
func (a *App) releaseToolCell(result *agent.ToolResult) {
	a.streamStateMu.Lock()
	match := a.currentToolName != "" &&
		((result.ID != "" && result.ID == a.currentToolID) ||
			(a.currentToolID == "" && result.Name == a.currentToolName))
	if match {
		a.currentToolID = ""
		a.currentToolName = ""
		a.currentToolHint = ""
		a.currentToolProgress = ""
	}
	a.streamStateMu.Unlock()
}

func (a *App) clearStreamingState() {
	a.streamStateMu.Lock()
	a.streamingText = ""
	a.streamingReasoning = ""
	a.currentToolID = ""
	a.currentToolName = ""
	a.currentToolHint = ""
	a.currentToolProgress = ""
	a.reasoningID = ""
	a.reasoningPart = 0
	a.streamStateMu.Unlock()
}

func (a *App) snapshotStreamState() (toolName, toolHint, toolProgress, text, reasoning string) {
	a.streamStateMu.Lock()
	defer a.streamStateMu.Unlock()
	return a.currentToolName, a.currentToolHint, a.currentToolProgress, a.streamingText, a.streamingReasoning
}

const renderInterval = 40 * time.Millisecond

// requestRender coalesces repaints from streaming goroutines.
func (a *App) requestRender() {
	if !a.renderPending.CompareAndSwap(false, true) {
		return
	}

	delay := renderInterval - time.Duration(time.Now().UnixNano()-a.renderLast.Load())
	if delay < 0 {
		delay = 0
	}

	time.AfterFunc(delay, func() {
		a.post(func() {
			a.renderPending.Store(false)
			a.renderLast.Store(time.Now().UnixNano())
			a.invalidate()
		})
	})
}

func (a *App) handleTurnEvent(ev code.TurnEvent) {
	defer func() {
		if recovered := recover(); recovered != nil {
			a.sessionMu.Lock()
			visible := a.sessionID == ev.SessionID
			if visible {
				a.clearStreamingState()
			}
			a.sessionMu.Unlock()
			if visible {
				a.queuePhase(PhaseIdle)
				a.post(func() {
					a.appendChat(cellNotice(fmt.Sprintf("Internal error: %v", recovered), theme.Default.Red, a.width()))
				})
			}
		}
	}()

	if ev.Message != nil {
		a.withCurrentSession(ev.SessionID, func() {
			a.handleStreamMessage(*ev.Message)
		})
		return
	}

	switch ev.State {
	case code.TurnInputActive:
		a.withCurrentSession(ev.SessionID, func() {
			a.queuePhase(PhaseThinking)
		})
	case code.TurnInputCompleted, code.TurnInputCancelled, code.TurnInputFailed:
		commit := a.takeTurnCommit(ev.InputID)
		a.post(func() {
			a.removePendingEcho(ev.InputID)
		})
		if ev.Executed {
			a.finishTurn(ev.SessionID, commit, ev.State, ev.Err)
		}
	}
}

func (a *App) handleStreamMessage(msg agent.Message) {
	for _, c := range msg.Content {
		switch {
		case c.ToolCall != nil:
			hint := tool.ExtractHint(c.ToolCall.Args, c.ToolCall.Name)
			a.streamStateMu.Lock()
			a.currentToolID = c.ToolCall.ID
			a.currentToolName = c.ToolCall.Name
			a.currentToolHint = hint
			a.currentToolProgress = ""
			a.streamingText = ""
			a.streamingReasoning = ""
			a.reasoningID = ""
			a.reasoningPart = 0
			a.streamStateMu.Unlock()
			a.queuePhase(PhaseToolRunning)
			a.requestRender()

		case c.ToolResult != nil:
			// Keep the live tool cell visible; it is released when the
			// committed result flushes into the chat, so it never blinks out
			// between the stream event and the commit.
			a.streamStateMu.Lock()
			a.streamingText = ""
			a.streamStateMu.Unlock()
			a.requestRender()

		case c.Reasoning != nil && c.Reasoning.Summary != "":
			if a.getPhase() != PhaseThinking {
				a.queuePhase(PhaseThinking)
			}
			a.streamStateMu.Lock()
			if a.reasoningID != "" && c.Reasoning.ID != a.reasoningID {
				a.streamingReasoning = ""
			}
			if a.streamingReasoning != "" && c.Reasoning.Part != a.reasoningPart {
				a.streamingReasoning += "\n\n"
			}
			a.streamingReasoning += c.Reasoning.Summary
			a.reasoningID = c.Reasoning.ID
			a.reasoningPart = c.Reasoning.Part
			a.streamStateMu.Unlock()
			a.requestRender()

		case c.Text != "":
			if a.getPhase() != PhaseStreaming {
				a.queuePhase(PhaseStreaming)
			}
			a.streamStateMu.Lock()
			a.streamingReasoning = ""
			a.streamingText += c.Text
			a.reasoningID = ""
			a.reasoningPart = 0
			a.streamStateMu.Unlock()
			a.requestRender()
		}
	}
}

func (a *App) finishTurn(sessionID, commit string, state code.TurnInputState, turnErr error) {
	t := theme.Default

	a.sessionMu.Lock()
	visible := a.sessionID == sessionID
	var nextPhase AppPhase
	if visible {
		nextPhase = PhaseIdle
		for _, input := range a.turns.Snapshot(sessionID).Inputs {
			if input.State == code.TurnInputActive {
				nextPhase = PhaseThinking
				break
			}
		}
	}
	a.sessionMu.Unlock()

	if visible {
		epoch := a.currentEpoch()
		a.post(func() {
			if a.sessionID != sessionID || a.sessionEpoch != epoch {
				return
			}

			a.clearStreamingState()
			a.setPhase(nextPhase)
			a.syncMessages()

			switch {
			case state == code.TurnInputCompleted:
				if nextPhase == PhaseIdle {
					a.flushTurnSeparator()
					a.bellIfUnfocused()
				}
			case state == code.TurnInputCancelled || errors.Is(turnErr, context.Canceled):
				a.flushToolGap()
				a.appendChat(cellNotice("Cancelled", t.Yellow, a.width()))
				a.resetTurnStats()
			default:
				a.flushToolGap()
				a.appendChat(cellNotice(fmt.Sprintf("Error: %v", turnErr), t.Red, a.width()))
				a.resetTurnStats()
			}

			a.invalidate()
		})
	}

	if state == code.TurnInputCompleted {
		a.commitRewind(commit)
		_ = a.agent.Save(sessionID)
	}
}

func (a *App) currentEpoch() uint64 {
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	return a.sessionEpoch
}

// flushToolGap commits the blank line a trailing tool cell is still owed, so
// separators and notices never sit tight against tool output.
func (a *App) flushToolGap() {
	if a.flow.gap() {
		a.appendChat([]string{""})
	}
}

// turnWork counts the visible tool and thought cells the current turn has
// committed since the last separator.
func (a *App) turnWork() (tools, thoughts int) {
	messages := a.agent.Messages(a.sessionID)
	if a.turnBase > len(messages) {
		return 0, 0
	}

	for _, m := range messages[a.turnBase:] {
		if m.Hidden || m.Role == agent.RoleSystem {
			continue
		}
		for _, c := range m.Content {
			switch {
			case c.ToolResult != nil:
				if !a.isToolHidden(c.ToolResult.Name) {
					tools++
				}
			case c.Reasoning != nil && c.Reasoning.Summary != "":
				thoughts++
			}
		}
	}

	return tools, thoughts
}

func (a *App) flushTurnSeparator() {
	tools, thoughts := a.turnWork()
	if tools == 0 && thoughts == 0 {
		a.resetTurnStats()
		return
	}
	a.flushToolGap()

	elapsed := ""
	if !a.turnStart.IsZero() {
		elapsed = formatElapsed(time.Since(a.turnStart))
	}

	a.appendChat(cellTurnSeparator(elapsed, tools, thoughts, a.width()))
	a.resetTurnStats()
}

func (a *App) resetTurnStats() {
	a.turnBase = len(a.agent.Messages(a.sessionID))
	a.turnStart = time.Time{}
	a.phaseStart = time.Time{}
}

func (a *App) rememberTurn(id string, input []agent.Content) {
	commit := "<unknown>"
	for _, c := range input {
		if c.Text != "" {
			commit = c.Text
			break
		}
	}
	a.turnMu.Lock()
	a.turnCommits[id] = commit
	a.turnMu.Unlock()
}

func (a *App) takeTurnCommit(id string) string {
	a.turnMu.Lock()
	commit := a.turnCommits[id]
	delete(a.turnCommits, id)
	a.turnMu.Unlock()
	return commit
}

func (a *App) commitRewind(message string) {
	if runes := []rune(message); len(runes) > 50 {
		message = string(runes[:50])
	}

	go func() {
		_ = a.agent.Workspace().Commit(message)
	}()
}
