package app

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/openai/openai-go/v3/responses"

	"github.com/adrianliechti/wingman-cli/pkg/markdown"
	"github.com/adrianliechti/wingman-cli/pkg/theme"
)

func (a *App) setupUI() {
	t := theme.Default

	a.welcomeView = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter)
	a.welcomeView.SetText(Logo)
	a.welcomeView.SetBackgroundColor(tcell.ColorDefault)

	a.chatView = tview.NewTextView().
		SetDynamicColors(true).
		SetWordWrap(false).
		SetScrollable(true).
		SetChangedFunc(func() {
			a.app.Draw()
		})
	a.chatView.SetBorder(false)
	a.chatView.SetBackgroundColor(tcell.ColorDefault)

	a.errorView = tview.NewTextView().
		SetDynamicColors(true).
		SetWordWrap(true)
	a.errorView.SetBorder(false)
	a.errorView.SetBackgroundColor(tcell.ColorDefault)

	a.mcpStatusView = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)
	a.mcpStatusView.SetBorder(false)
	a.mcpStatusView.SetBackgroundColor(tcell.ColorDefault)

	inputBgColor := t.Selection
	a.input = tview.NewTextArea().
		SetPlaceholder("Ask anything...")
	a.input.SetBackgroundColor(inputBgColor)
	a.input.SetBorder(false)
	a.input.SetTextStyle(tcell.StyleDefault.Foreground(t.Foreground).Background(inputBgColor))
	a.input.SetPlaceholderStyle(tcell.StyleDefault.Foreground(t.BrBlack).Background(inputBgColor))

	a.mainContent = tview.NewFlex().SetDirection(tview.FlexRow)
	a.mainContent.AddItem(a.welcomeView, 0, 1, false)

	a.updateWelcomeView()
}

func (a *App) buildLayout() *tview.Flex {
	t := theme.Default
	inputBgColor := t.Selection

	a.inputFrame = tview.NewFrame(a.input).
		SetBorders(1, 1, 0, 0, 1, 1)
	a.inputFrame.SetBackgroundColor(inputBgColor)
	a.inputFrame.SetBorder(false)

	a.input.SetChangedFunc(func() {
		a.updateInputHeight()
		a.updateInputHint()
	})

	bottomBar := tview.NewFlex().SetDirection(tview.FlexColumn)

	a.inputHint = tview.NewTextView().
		SetDynamicColors(true)
	a.inputHint.SetBackgroundColor(tcell.ColorDefault)
	a.updateInputHint()

	a.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignRight)
	a.updateStatusBar()
	a.statusBar.SetBackgroundColor(tcell.ColorDefault)

	bottomBar.AddItem(a.inputHint, 0, 1, false)
	bottomBar.AddItem(a.statusBar, 0, 1, false)

	bottomBarContainer := tview.NewFlex().SetDirection(tview.FlexColumn)
	bottomBarContainer.AddItem(nil, 4, 0, false)
	bottomBarContainer.AddItem(bottomBar, 0, 1, false)
	bottomBarContainer.AddItem(nil, 4, 0, false)

	inputContainer := tview.NewFlex().SetDirection(tview.FlexColumn)
	inputContainer.AddItem(nil, 4, 0, false)
	inputContainer.AddItem(a.inputFrame, 0, 1, true)
	inputContainer.AddItem(nil, 4, 0, false)

	statusContainer := tview.NewFlex().SetDirection(tview.FlexColumn)
	statusContainer.AddItem(nil, 4, 0, false)
	statusContainer.AddItem(a.mcpStatusView, 0, 1, false)
	statusContainer.AddItem(nil, 4, 0, false)

	a.inputSection = tview.NewFlex().SetDirection(tview.FlexRow)
	a.inputSection.AddItem(inputContainer, 0, 1, true)
	a.inputSection.AddItem(bottomBarContainer, 1, 0, false)

	a.chatContainer = tview.NewFlex().SetDirection(tview.FlexColumn)
	a.chatContainer.AddItem(nil, 2, 0, false)
	a.chatContainer.AddItem(a.mainContent, 0, 1, false)
	a.chatContainer.AddItem(nil, 4, 0, false)

	a.chatContainer.SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		newWidth := width - 6

		if newWidth != a.chatWidth && !a.isWelcomeMode && !a.isStreaming {
			a.chatWidth = newWidth
			a.rerenderChat()
		} else if a.chatWidth == 0 {
			a.chatWidth = newWidth
		}

		return x, y, width, height
	})

	a.mainLayout = tview.NewFlex().SetDirection(tview.FlexRow)

	if a.isWelcomeMode {
		a.mainLayout.
			AddItem(nil, 2, 0, false).
			AddItem(a.welcomeView, 12, 0, false).
			AddItem(statusContainer, 4, 0, false).
			AddItem(nil, 0, 1, false).
			AddItem(a.inputSection, 6, 0, true).
			AddItem(nil, 0, 2, false)
	} else {
		a.mainLayout.
			AddItem(a.chatContainer, 0, 1, false).
			AddItem(a.inputSection, 6, 0, true)
	}

	a.app.SetInputCapture(a.handleInput)

	return a.mainLayout
}

func (a *App) updateInputHeight() {
	text := a.input.GetText()
	lines := strings.Count(text, "\n") + 1

	minHeight := 6
	maxHeight := 13
	height := min(max(lines+5, minHeight), maxHeight)

	a.mainLayout.ResizeItem(a.inputSection, height, 0)
}

func (a *App) updateStatusBar() {
	t := theme.Default

	modeLabel := "Agent"
	if a.currentMode == ModePlan {
		modeLabel = "Plan"
	}

	if a.totalTokens > 0 {
		a.statusBar.SetText(fmt.Sprintf("[%s]%s[-] • [%s]%s[-] • [%s]%s[-]", t.BrBlack, formatTokens(a.totalTokens), t.Cyan, a.config.Model, t.Yellow, modeLabel))
	} else {
		a.statusBar.SetText(fmt.Sprintf("[%s]%s[-] • [%s]%s[-]", t.Cyan, a.config.Model, t.Yellow, modeLabel))
	}
}

func (a *App) updateInputHint() {
	t := theme.Default

	if a.input.GetText() == "" && !a.isStreaming {
		a.inputHint.SetText(fmt.Sprintf("[%s]enter[-] [%s]send[-]  [%s]tab[-] [%s]mode[-]", t.BrBlack, t.Foreground, t.BrBlack, t.Foreground))
	} else {
		a.inputHint.SetText(fmt.Sprintf("[%s]enter[-] [%s]send[-]", t.BrBlack, t.Foreground))
	}
}

func formatTokens(tokens int64) string {
	if tokens >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(tokens)/1000000)
	}

	if tokens >= 1000 {
		return fmt.Sprintf("%.1fK", float64(tokens)/1000)
	}

	return fmt.Sprintf("%d", tokens)
}

func (a *App) updateWelcomeView() {
	var sb strings.Builder

	sb.WriteString(Logo)

	a.welcomeView.SetText(sb.String())

	a.mcpStatusView.SetText("")
}

func (a *App) rerenderChatWithStreaming(streamingContent string) {
	a.chatView.Clear()

	for _, item := range a.agent.Messages() {
		if msg := item.OfMessage; msg != nil {
			content := msg.Content.OfString.Value

			if strings.HasPrefix(content, "<conversation_summary>") {
				continue
			}

			switch msg.Role {
			case responses.EasyInputMessageRoleUser:
				fmt.Fprint(a.chatView, markdown.FormatUserMessage(content, a.chatWidth))
			case responses.EasyInputMessageRoleAssistant:
				fmt.Fprint(a.chatView, markdown.FormatAssistantMessage(content, a.chatWidth))
			}
		}

		if fc := item.OfFunctionCall; fc != nil {
			a.lastToolName = fc.Name
		}

		if fco := item.OfFunctionCallOutput; fco != nil {
			output := fco.Output.OfString.Value

			if len(output) > 500 {
				output = output[:500] + "..."
			}

			fmt.Fprint(a.chatView, markdown.FormatToolCall(a.lastToolName, output, a.chatWidth))
		}
	}

	fmt.Fprint(a.chatView, markdown.FormatAssistantMessage(streamingContent, a.chatWidth))
}

func (a *App) rerenderChatWithToolProgress(streamingContent string, toolName string) {
	a.chatView.Clear()

	for _, item := range a.agent.Messages() {
		if msg := item.OfMessage; msg != nil {
			content := msg.Content.OfString.Value

			if strings.HasPrefix(content, "<conversation_summary>") {
				continue
			}

			switch msg.Role {
			case responses.EasyInputMessageRoleUser:
				fmt.Fprint(a.chatView, markdown.FormatUserMessage(content, a.chatWidth))
			case responses.EasyInputMessageRoleAssistant:
				fmt.Fprint(a.chatView, markdown.FormatAssistantMessage(content, a.chatWidth))
			}
		}

		if fc := item.OfFunctionCall; fc != nil {
			a.lastToolName = fc.Name
		}

		if fco := item.OfFunctionCallOutput; fco != nil {
			output := fco.Output.OfString.Value

			if len(output) > 500 {
				output = output[:500] + "..."
			}

			fmt.Fprint(a.chatView, markdown.FormatToolCall(a.lastToolName, output, a.chatWidth))
		}
	}

	if streamingContent != "" {
		fmt.Fprint(a.chatView, markdown.FormatAssistantMessage(streamingContent, a.chatWidth))
	}

	fmt.Fprint(a.chatView, markdown.FormatToolProgress(toolName, a.chatWidth))
}

func (a *App) rerenderChat() {
	a.chatView.Clear()

	var lastToolName string

	for _, item := range a.agent.Messages() {
		if msg := item.OfMessage; msg != nil {
			content := msg.Content.OfString.Value

			if strings.HasPrefix(content, "<conversation_summary>") {
				continue
			}

			switch msg.Role {
			case responses.EasyInputMessageRoleUser:
				fmt.Fprint(a.chatView, markdown.FormatUserMessage(content, a.chatWidth))

			case responses.EasyInputMessageRoleAssistant:
				fmt.Fprint(a.chatView, markdown.FormatAssistantMessage(content, a.chatWidth))
			}
		}

		if fc := item.OfFunctionCall; fc != nil {
			lastToolName = fc.Name
		}

		if fco := item.OfFunctionCallOutput; fco != nil {
			output := fco.Output.OfString.Value

			if len(output) > 500 {
				output = output[:500] + "..."
			}

			fmt.Fprint(a.chatView, markdown.FormatToolCall(lastToolName, output, a.chatWidth))
		}
	}
}

func (a *App) renderMCPInitError(err error) {
	if err == nil {
		err = a.mcpError
	}

	if err == nil {
		return
	}

	a.switchToChat()
	a.startupError = err.Error()

	a.updateErrorView()
}

func (a *App) updateErrorView() {
	if a.startupError == "" {
		a.errorView.SetText("")
		a.mainContent.ResizeItem(a.errorView, 0, 0)

		return
	}

	width := a.chatWidth

	if width == 0 {
		width = 80
	}

	a.errorView.SetText(markdown.FormatError("MCP initialization failed", a.startupError, width))
	a.mainContent.ResizeItem(a.errorView, 4, 0)
}
