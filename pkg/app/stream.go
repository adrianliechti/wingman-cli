package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"

	"github.com/adrianliechti/wingman-agent/pkg/ui/theme"
)

// extractToolHint extracts a display hint from tool arguments JSON.
// Returns the full hint text — truncation is handled by the format functions based on available width.
func extractToolHint(argsJSON string) string {
	var args map[string]any

	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ""
	}

	// If a description is provided (e.g., shell tool), prefer it as the hint
	if desc, ok := args["description"]; ok {
		if str, ok := desc.(string); ok && str != "" {
			return strings.Join(strings.Fields(str), " ")
		}
	}

	// Priority order of parameters to use as hint
	hintKeys := []string{
		"query",
		"pattern",
		"command",
		"prompt",
		"path",
		"file",
		"url",
		"name",
	}

	for _, key := range hintKeys {
		if val, ok := args[key]; ok {
			if str, ok := val.(string); ok && str != "" {
				return strings.Join(strings.Fields(str), " ")
			}
		}
	}

	return ""
}

// setPhase updates the phase and spinner state.
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
func (a *App) render(streaming, toolName, toolHint string) {
	messages := a.agent.Messages() // Capture now, not in closure
	a.app.QueueUpdateDraw(func() {
		a.renderChat(messages, streaming, toolName, toolHint)
	})
}

// streamResponse processes user input and streams the response
func (a *App) streamResponse(input []agent.Content, instructions string, tools []tool.Tool) {
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

	var content strings.Builder
	var streamErr error

	a.setPhase(PhaseThinking)

	for msg, err := range a.agent.Send(streamCtx, instructions, input, tools) {
		if err != nil {
			streamErr = err
			break
		}

		for _, c := range msg.Content {
			switch {
			case c.ToolCall != nil:
				a.currentToolName = c.ToolCall.Name
				a.currentToolHint = extractToolHint(c.ToolCall.Args)
				a.setPhase(PhaseToolRunning)
				content.Reset()
				a.render("", c.ToolCall.Name, a.currentToolHint)

			case c.ToolResult != nil:
				a.currentToolName = ""
				a.currentToolHint = ""
				content.Reset()
				// Don't re-render here — let the next event (ToolCall or Text)
				// update the view. This avoids flashing empty state between
				// rapid tool call/result pairs.

			case c.Text != "":
				if a.phase != PhaseStreaming {
					a.setPhase(PhaseStreaming)
				}
				content.WriteString(c.Text)
				a.render(content.String(), "", "")
			}
		}
	}

	a.setPhase(PhaseIdle)

	usage := a.agent.Usage()
	a.totalTokens = usage.InputTokens + usage.OutputTokens

	a.app.QueueUpdateDraw(func() {
		if streamErr != nil {
			if errors.Is(streamErr, context.Canceled) {
				fmt.Fprint(a.chatView, a.formatNotice("Cancelled", t.Yellow))
			} else {
				fmt.Fprint(a.chatView, a.formatNotice(fmt.Sprintf("Error: %v", streamErr), t.Red))
			}
		} else {
			messages := a.agent.Messages()
			a.renderChat(messages, "", "", "")
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
	}
}
