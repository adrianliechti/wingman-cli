package app

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/adrianliechti/wingman-cli/pkg/agent"
	"github.com/adrianliechti/wingman-cli/pkg/markdown"
	"github.com/adrianliechti/wingman-cli/pkg/theme"
)

func (a *App) handleInput(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() == tcell.KeyCtrlC {
		a.stop()
		return nil
	}

	a.promptMu.Lock()
	isPrompt := a.promptActive
	a.promptMu.Unlock()

	if isPrompt {
		return a.handlePromptInput(event)
	}

	if event.Key() == tcell.KeyCtrlL {
		a.chatView.Clear()
		a.agent.Clear()
		a.totalTokens = 0
		a.updateStatusBar()
		return nil
	}

	if event.Key() == tcell.KeyEnter && !a.isStreaming {
		a.submitInput()
		return nil
	}

	return event
}

func (a *App) handlePromptInput(event *tcell.EventKey) *tcell.EventKey {
	switch event.Rune() {
	case 'y', 'Y':
		a.respondToPrompt(true)
		return nil
	case 'n', 'N':
		a.respondToPrompt(false)
		return nil
	}

	return nil
}

func (a *App) respondToPrompt(approved bool) {
	a.promptMu.Lock()
	defer a.promptMu.Unlock()

	if !a.promptActive {
		return
	}

	select {
	case req := <-a.promptChan:
		req.response <- approved
		a.promptActive = false
		a.input.SetPlaceholder("Ask anything...")

	default:
	}
}

func (a *App) promptUser(message string) (bool, error) {
	responseChan := make(chan bool, 1)

	a.promptChan <- promptRequest{
		message:  message,
		response: responseChan,
	}

	result := <-responseChan

	return result, nil
}

func (a *App) handlePromptRequests() {
	for req := range a.promptChan {
		a.promptMu.Lock()
		a.promptActive = true
		a.promptMu.Unlock()

		a.app.QueueUpdateDraw(func() {
			fmt.Fprint(a.chatView, markdown.FormatPrompt("Confirm Command", req.message, a.chatWidth))
			a.input.SetPlaceholder("y/n")
		})

		a.promptChan <- req
	}
}

func (a *App) switchToChat() {
	if !a.isWelcomeMode {
		return
	}

	a.isWelcomeMode = false
	a.mainContent.Clear()
	a.mainContent.SetDirection(tview.FlexRow)
	a.mainContent.AddItem(a.errorView, 0, 0, false)
	a.mainContent.AddItem(a.chatView, 0, 1, false)

	a.updateErrorView()

	a.mainLayout.Clear()
	a.mainLayout.
		AddItem(a.chatContainer, 0, 1, false).
		AddItem(a.inputSection, 6, 0, true)
}

func (a *App) submitInput() {
	if a.isStreaming {
		return
	}

	query := a.input.GetText()

	if query == "" {
		return
	}

	switch query {
	case "/quit":
		a.stop()
		return

	case "/clear":
		a.chatView.Clear()
		a.agent.Clear()
		a.totalTokens = 0
		a.updateStatusBar()
		a.input.SetText("", true)
		return

	case "/help":
		a.switchToChat()
		a.input.SetText("", true)
		t := theme.Default
		fmt.Fprintf(a.chatView, "[%s::b]Commands[-::-]\n", t.Cyan)
		fmt.Fprintf(a.chatView, "  [%s]/help[-]   - Show this help\n", t.BrCyan)
		fmt.Fprintf(a.chatView, "  [%s]/clear[-]  - Clear chat history\n", t.BrCyan)
		fmt.Fprintf(a.chatView, "  [%s]/quit[-]   - Exit application\n\n", t.BrCyan)
		return
	}

	a.switchToChat()
	a.isStreaming = true
	a.input.SetText("", true)
	fmt.Fprint(a.chatView, markdown.FormatUserMessage(query, a.chatWidth))

	go a.streamResponse(query)
}

func (a *App) streamResponse(query string) {
	t := theme.Default

	var content strings.Builder
	var streamErr error
	var currentTool string
	var lastCompaction *agent.CompactionInfo

	for msg, err := range a.agent.Send(a.ctx, query, a.allTools()) {
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
				a.app.QueueUpdateDraw(func() {
					a.rerenderChat()
					fmt.Fprint(a.chatView, markdown.FormatCompactionProgress(msg.Compaction.FromTokens, a.chatWidth))
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
			currentTool = msg.ToolCall.Name

			a.app.QueueUpdateDraw(func() {
				a.rerenderChatWithToolProgress(content.String(), currentTool)
			})

			continue
		}

		if msg.ToolResult != nil {
			currentTool = ""
			content.Reset()

			a.app.QueueUpdateDraw(func() {
				a.rerenderChat()
			})

			continue
		}

		for _, c := range msg.Content {
			content.WriteString(c.Text)
		}

		a.app.QueueUpdateDraw(func() {
			a.rerenderChatWithStreaming(content.String())
		})
	}

	a.app.QueueUpdateDraw(func() {
		if streamErr != nil {
			fmt.Fprintf(a.chatView, "\n[%s]Error: %v[-]\n\n", t.Red, streamErr)
		} else {
			a.rerenderChat()

			if lastCompaction != nil {
				fmt.Fprint(a.chatView, markdown.FormatCompaction(lastCompaction.FromTokens, lastCompaction.ToTokens, a.chatWidth))
			}
		}

		a.isStreaming = false
	})
}
