package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman-cli/pkg/agent"
	"github.com/adrianliechti/wingman-cli/pkg/markdown"
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

// streamResponse processes user input and streams the response
func (a *App) streamResponse(input []agent.Content, instructions string, tools []tool.Tool) {
	t := theme.Default

	var content strings.Builder
	var streamErr error
	var lastCompaction *agent.CompactionInfo

	// Start with thinking phase and immediately show it
	a.phase = PhaseThinking
	if a.spinner != nil {
		a.spinner.Start(PhaseThinking, "")
	}
	// Force immediate UI update to show thinking state
	a.app.QueueUpdateDraw(func() {})

	for msg, err := range a.agent.Send(a.ctx, instructions, input, tools) {
		if err != nil {
			streamErr = err
			break
		}

		if msg.Usage != nil {
			a.totalTokens = msg.Usage.InputTokens + msg.Usage.OutputTokens

			a.app.QueueUpdateDraw(func() {
				a.updateStatusBar()
			})

			continue
		}

		if msg.Compaction != nil {
			if msg.Compaction.InProgress {
				a.phase = PhaseCompacting
				if a.spinner != nil {
					a.spinner.SetPhase(PhaseCompacting, "")
				}

				a.app.QueueUpdateDraw(func() {
					messages := a.agent.Messages()
					a.renderChat(messages, "", "", "")
				})
			} else {
				lastCompaction = msg.Compaction
				a.totalTokens = msg.Compaction.ToTokens

				a.app.QueueUpdateDraw(func() {
					a.updateStatusBar()
				})
			}

			continue
		}

		if msg.ToolCall != nil {
			a.phase = PhaseToolRunning
			if a.spinner != nil {
				a.spinner.SetPhase(PhaseToolRunning, msg.ToolCall.Name)
			}

			toolName := msg.ToolCall.Name
			toolHint := extractToolHint(msg.ToolCall.Args)
			currentContent := content.String()

			a.app.QueueUpdateDraw(func() {
				messages := a.agent.Messages()
				a.renderChat(messages, currentContent, toolName, toolHint)
			})

			continue
		}

		if msg.ToolResult != nil {
			// Tool complete, back to thinking for next iteration
			a.phase = PhaseThinking
			if a.spinner != nil {
				a.spinner.SetPhase(PhaseThinking, "")
			}
			content.Reset()

			a.app.QueueUpdateDraw(func() {
				messages := a.agent.Messages()
				a.renderChat(messages, "", "", "")
				a.updateStatusBar()
			})

			continue
		}

		// Streaming content - keep spinner running to show activity
		if a.phase != PhaseStreaming {
			a.phase = PhaseStreaming
			if a.spinner != nil {
				a.spinner.SetPhase(PhaseStreaming, "")
			}
		}

		for _, c := range msg.Content {
			content.WriteString(c.Text)
		}

		currentContent := content.String()
		a.app.QueueUpdateDraw(func() {
			messages := a.agent.Messages()
			a.renderChat(messages, currentContent, "", "")
		})
	}

	// Finalize
	if a.spinner != nil {
		a.spinner.Stop()
	}

	a.app.QueueUpdateDraw(func() {
		if streamErr != nil {
			fmt.Fprintf(a.chatView, "\n[%s]Error: %v[-]\n\n", t.Red, streamErr)
		} else {
			messages := a.agent.Messages()
			a.renderChat(messages, "", "", "")

			if lastCompaction != nil {
				fmt.Fprint(a.chatView, markdown.FormatCompaction(lastCompaction.FromTokens, lastCompaction.ToTokens, a.chatWidth))
			}
		}

		a.phase = PhaseIdle
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
