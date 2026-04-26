package code

import (
	"context"
	"errors"
	"fmt"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/tui"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

// setPhase updates the phase and spinner state.
// Must be called from the UI goroutine (e.g. inside QueueUpdateDraw or an input handler).
func (a *App) setPhase(phase AppPhase) {
	a.phase = phase

	if a.spinner != nil {
		if phase == PhaseIdle {
			a.spinner.Stop()
			a.updateInputHint()
		} else {
			a.spinner.Start(phase)
		}
	}
}

// render queues a UI update with the current state.
// Skipped while a confirmation prompt is active to avoid wiping it.
//
// renderChat reads the transient streaming/tool fields from a directly, so
// callers don't pass them in. Messages is still captured before the closure
// to avoid a race with a.agent.Messages being mutated mid-flight.
func (a *App) render() {
	if a.promptActive || a.askActive {
		return
	}

	messages := a.agent.Messages
	a.app.QueueUpdateDraw(func() {
		a.renderChat(messages)
	})
}

// clearStreamingState resets the transient overlay so the next renderChat
// shows only committed messages.
func (a *App) clearStreamingState() {
	a.streamingText = ""
	a.streamingReasoning = ""
	a.currentToolName = ""
	a.currentToolHint = ""
}

// streamResponse processes user input and streams the response
func (a *App) streamResponse(input []agent.Content) {
	t := theme.Default

	// Create cancellable context for this stream
	streamCtx, cancel := context.WithCancel(a.ctx)

	a.streamMu.Lock()
	a.streamCancel = cancel
	a.streamMu.Unlock()

	defer func() {
		a.streamMu.Lock()
		a.streamCancel = nil
		a.streamMu.Unlock()
	}()

	// Recover from panics so the UI never locks up
	defer func() {
		if r := recover(); r != nil {
			a.clearStreamingState()
			a.setPhase(PhaseIdle)
			a.app.QueueUpdateDraw(func() {
				fmt.Fprint(a.chatView, a.formatNotice(fmt.Sprintf("Internal error: %v", r), t.Red))
				a.updateStatusBar()
			})
		}
	}()

	var reasoningID string
	var streamErr error

	a.setPhase(PhaseThinking)

	for msg, err := range a.agent.Send(streamCtx, input) {
		if err != nil {
			streamErr = err
			break
		}

		for _, c := range msg.Content {
			switch {
			case c.ToolCall != nil:
				a.currentToolName = c.ToolCall.Name
				a.currentToolHint = tui.ExtractToolHint(c.ToolCall.Args)
				a.setPhase(PhaseToolRunning)
				a.streamingText = ""
				a.streamingReasoning = ""
				reasoningID = ""
				a.render()

			case c.ToolResult != nil:
				a.currentToolName = ""
				a.currentToolHint = ""
				a.streamingText = ""
				// Don't re-render here — let the next event (ToolCall or Text)
				// update the view. This avoids flashing empty state between
				// rapid tool call/result pairs.

			case c.Reasoning != nil && c.Reasoning.Summary != "":
				if a.phase != PhaseThinking {
					a.setPhase(PhaseThinking)
				}
				// New reasoning item id: drop the prior in-progress block — it'll
				// reappear from agent.Messages once the request completes.
				if reasoningID != "" && c.Reasoning.ID != reasoningID {
					a.streamingReasoning = ""
				}
				reasoningID = c.Reasoning.ID
				a.streamingReasoning += c.Reasoning.Summary
				a.render()

			case c.Text != "":
				if a.phase != PhaseStreaming {
					a.setPhase(PhaseStreaming)
				}
				a.streamingReasoning = ""
				reasoningID = ""
				a.streamingText += c.Text
				a.render()
			}
		}

		// Update token count during the loop so the statusbar stays current
		usage := a.agent.Usage
		a.inputTokens = usage.InputTokens
		a.outputTokens = usage.OutputTokens
		a.app.QueueUpdateDraw(func() {
			a.updateStatusBar()
		})
	}

	a.setPhase(PhaseIdle)

	a.app.QueueUpdateDraw(func() {
		if streamErr != nil {
			a.clearStreamingState()
			if errors.Is(streamErr, context.Canceled) {
				fmt.Fprint(a.chatView, a.formatNotice("Cancelled", t.Yellow))
			} else {
				fmt.Fprint(a.chatView, a.formatNotice(fmt.Sprintf("Error: %v", streamErr), t.Red))
			}
		} else {
			a.clearStreamingState()
			a.renderChat(a.agent.Messages)
		}

		a.updateStatusBar()
	})

	if streamErr == nil {
		var commit string

		for _, c := range input {
			if c.Text != "" {
				commit = c.Text
				break
			}
		}

		if commit == "" {
			commit = "<unknown>"
		}

		a.commitRewind(commit)
		a.saveSession()
	}
}
