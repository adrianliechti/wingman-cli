package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman-cli/pkg/agent"
	"github.com/adrianliechti/wingman-cli/pkg/theme"
	"github.com/adrianliechti/wingman-cli/pkg/tool"
)

// extractToolHint extracts a display hint from tool arguments JSON.
func extractToolHint(argsJSON string) string {
	var args map[string]any

	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ""
	}

	// Priority order of parameters to use as hint
	hintKeys := []string{
		"query",
		"pattern",
		"command",
		"path",
		"file",
		"url",
		"name",
	}

	for _, key := range hintKeys {
		if val, ok := args[key]; ok {
			if str, ok := val.(string); ok && str != "" {
				// Collapse newlines and multiple spaces to single space
				str = strings.Join(strings.Fields(str), " ")

				if len(str) > 50 {
					str = str[:47] + "..."
				}

				return str
			}
		}
	}

	return ""
}

// setPhase updates the phase and spinner state.
func (a *App) setPhase(phase AppPhase, hint string) {
	a.phase = phase

	if a.spinner != nil {
		if phase == PhaseIdle {
			a.spinner.Stop()
			a.updateInputHint()
		} else {
			a.spinner.SetPhase(phase, hint)
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

	// Start with thinking phase
	if a.spinner != nil {
		a.spinner.Start(PhaseThinking, "")
	}
	a.setPhase(PhaseThinking, "")
	a.app.QueueUpdateDraw(func() {})

	for msg, err := range a.agent.Send(streamCtx, instructions, input, tools) {
		if err != nil {
			streamErr = err
			break
		}

		for _, c := range msg.Content {
			switch {
			case c.ToolCall != nil:
				a.setPhase(PhaseToolRunning, c.ToolCall.Name)
				a.render(content.String(), c.ToolCall.Name, extractToolHint(c.ToolCall.Args))

			case c.ToolResult != nil:
				a.setPhase(PhaseThinking, "")
				content.Reset()
				a.render("", "", "")
				a.app.QueueUpdateDraw(func() { a.updateStatusBar() })

			case c.Text != "":
				if a.phase != PhaseStreaming {
					a.setPhase(PhaseStreaming, "")
				}
				content.WriteString(c.Text)
				a.render(content.String(), "", "")
			}
		}
	}

	a.setPhase(PhaseIdle, "")

	usage := a.agent.Usage()
	a.totalTokens = usage.InputTokens + usage.OutputTokens

	a.app.QueueUpdateDraw(func() {
		if streamErr != nil {
			if errors.Is(streamErr, context.Canceled) {
				// User cancelled - show brief notice instead of error
				fmt.Fprintf(a.chatView, "\n[%s]Cancelled[-]\n\n", t.Yellow)
			} else {
				fmt.Fprintf(a.chatView, "\n[%s]Error: %v[-]\n\n", t.Red, streamErr)
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
