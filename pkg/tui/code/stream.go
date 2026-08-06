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

	a.refreshUsage()
}

func (a *App) refreshUsage() {
	usage := a.agent.Usage(a.sessionID)
	a.inputTokens = usage.InputTokens
	a.outputTokens = usage.OutputTokens
	a.lastInputTokens = usage.LastInputTokens
	a.contextWindow = usage.ContextWindow
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
	a.pendingEchoMu.Lock()
	defer a.pendingEchoMu.Unlock()
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
	if a.streamCurrent.matchesTool(result) {
		a.streamCurrent.clearTool()
	} else {
		for i := len(a.streamHistory) - 1; i >= 0; i-- {
			if !a.streamHistory[i].matchesTool(result) {
				continue
			}
			a.streamHistory[i].clearTool()
			if a.streamHistory[i].empty() {
				a.streamHistory = append(a.streamHistory[:i], a.streamHistory[i+1:]...)
			}
			break
		}
	}
	a.streamStateMu.Unlock()
}

func (snapshot streamSnapshot) matchesTool(result *agent.ToolResult) bool {
	return snapshot.toolName != "" &&
		((result.ID != "" && result.ID == snapshot.toolID) ||
			(snapshot.toolID == "" && result.Name == snapshot.toolName))
}

func (snapshot *streamSnapshot) clearTool() {
	snapshot.toolID = ""
	snapshot.toolName = ""
	snapshot.toolArgs = ""
	snapshot.toolHint = ""
	snapshot.toolProgress = ""
	snapshot.toolResult = nil
}

func (snapshot streamSnapshot) empty() bool {
	return snapshot.userText == "" && snapshot.toolName == "" && strings.TrimSpace(snapshot.text) == "" && snapshot.reasoning == ""
}

func (snapshot streamSnapshot) toolLines(width int, expanded bool) []string {
	if snapshot.toolResult != nil {
		return cellTool(snapshot.toolResult, width, expanded)
	}
	return cellToolProgress(snapshot.toolName, snapshot.toolHint, snapshot.toolProgress, width)
}

func (snapshot streamSnapshot) toolText() string {
	if snapshot.toolResult != nil {
		result := snapshot.toolResult
		return result.Name + " " + tool.ExtractHint(result.Args, result.Name) + "\n" + result.Content
	}
	return snapshot.toolName + " " + snapshot.toolHint + "\n" + snapshot.toolProgress
}

func (a *App) clearStreamingState() {
	a.streamStateMu.Lock()
	a.streamCurrent = streamSnapshot{}
	a.streamHistory = nil
	a.streamStateMu.Unlock()
}

func (a *App) archiveStreamStateLocked() {
	if !a.streamCurrent.empty() {
		a.streamHistory = append(a.streamHistory, a.streamCurrent)
	}
	a.streamCurrent = streamSnapshot{}
}

// appendLiveUserEcho inserts an input being processed into the ordered live
// tail. ACP commits the full turn only when it completes, so this snapshot is
// replaced by the persisted user message during finishTurn.
func (a *App) appendLiveUserEcho(text string) {
	a.streamStateMu.Lock()
	a.archiveStreamStateLocked()
	a.streamHistory = append(a.streamHistory, streamSnapshot{userText: text})
	a.streamStateMu.Unlock()
}

func (a *App) snapshotStreamState() []streamSnapshot {
	a.streamStateMu.Lock()
	defer a.streamStateMu.Unlock()
	snapshots := append([]streamSnapshot(nil), a.streamHistory...)
	if !a.streamCurrent.empty() {
		snapshots = append(snapshots, a.streamCurrent)
	}
	return snapshots
}

func (a *App) completeToolStateLocked(result *agent.ToolResult) {
	if a.streamCurrent.matchesTool(result) {
		a.streamCurrent.completeTool(result)
		a.archiveStreamStateLocked()
		return
	}
	for i := len(a.streamHistory) - 1; i >= 0; i-- {
		if a.streamHistory[i].matchesTool(result) {
			a.streamHistory[i].completeTool(result)
			return
		}
	}
}

func (snapshot *streamSnapshot) completeTool(result *agent.ToolResult) {
	completed := *result
	if completed.ID == "" {
		completed.ID = snapshot.toolID
	}
	if completed.Name == "" {
		completed.Name = snapshot.toolName
	}
	if completed.Args == "" {
		completed.Args = snapshot.toolArgs
	}
	snapshot.toolResult = &completed
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
			// TurnManager emits Active synchronously before Agent.Send starts,
			// preserving the user cell ahead of any output from this turn.
			a.promotePendingEcho(ev.InputID)
			a.queuePhase(PhaseThinking)
		})
	case code.TurnInputCompleted, code.TurnInputCancelled, code.TurnInputFailed:
		a.post(func() {
			a.removePendingEcho(ev.InputID)
		})
		if ev.Executed {
			a.finishTurn(ev.SessionID, ev.State, ev.Err)
		}
	}
}

func (a *App) handleStreamMessage(msg agent.Message) {
	for _, c := range msg.Content {
		switch {
		case c.ToolCall != nil:
			hint := tool.ExtractHint(c.ToolCall.Args, c.ToolCall.Name)
			a.streamStateMu.Lock()
			a.archiveStreamStateLocked()
			a.streamCurrent.toolID = c.ToolCall.ID
			a.streamCurrent.toolName = c.ToolCall.Name
			a.streamCurrent.toolArgs = c.ToolCall.Args
			a.streamCurrent.toolHint = hint
			a.streamStateMu.Unlock()
			a.queuePhase(PhaseToolRunning)
			a.requestRender()

		case c.ToolResult != nil:
			a.streamStateMu.Lock()
			a.completeToolStateLocked(c.ToolResult)
			a.streamStateMu.Unlock()
			a.requestRender()

		case c.Reasoning != nil && c.Reasoning.Summary != "":
			if a.getPhase() != PhaseThinking {
				a.queuePhase(PhaseThinking)
			}
			a.streamStateMu.Lock()
			if a.streamCurrent.reasoning != "" && c.Reasoning.ID != a.streamCurrent.reasoningID {
				a.archiveStreamStateLocked()
			}
			if a.streamCurrent.reasoning != "" && c.Reasoning.Part != a.streamCurrent.reasoningPart {
				a.streamCurrent.reasoning += "\n\n"
			}
			a.streamCurrent.reasoning += c.Reasoning.Summary
			a.streamCurrent.reasoningID = c.Reasoning.ID
			a.streamCurrent.reasoningPart = c.Reasoning.Part
			a.streamStateMu.Unlock()
			a.requestRender()

		case c.Text != "":
			if a.getPhase() != PhaseStreaming {
				a.queuePhase(PhaseStreaming)
			}
			a.streamStateMu.Lock()
			if a.streamCurrent.reasoning != "" {
				a.archiveStreamStateLocked()
			}
			a.streamCurrent.text += c.Text
			a.streamStateMu.Unlock()
			a.requestRender()
		}
	}
}

func (a *App) finishTurn(sessionID string, state code.TurnInputState, turnErr error) {
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
			a.refreshUsage()

			switch {
			case state == code.TurnInputCompleted:
				if nextPhase == PhaseIdle {
					a.flushTurnSeparator()
					a.revealUsage(time.Now())
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
		a.saveSessionID(sessionID)
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
