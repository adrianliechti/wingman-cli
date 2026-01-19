package app

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/adrianliechti/wingman-cli/pkg/agent"
	"github.com/adrianliechti/wingman-cli/pkg/clipboard"
	"github.com/adrianliechti/wingman-cli/pkg/markdown"
	"github.com/adrianliechti/wingman-cli/pkg/theme"
)

const maxToolOutputLen = 500

// Input handling

func (a *App) handleInput(event *tcell.EventKey) *tcell.EventKey {
	// Handle Ctrl+C: close modals first, then stop app
	if event.Key() == tcell.KeyCtrlC {
		if a.filePickerActive {
			a.closeFilePicker()
			return nil
		}
		if a.pickerActive {
			a.closePicker()
			return nil
		}
		a.stop()
		return nil
	}

	// Let picker handle its own events
	if a.pickerActive || a.filePickerActive {
		return event
	}

	// Handle @ to trigger file picker (don't insert @ into input)
	if event.Rune() == '@' {
		go func() {
			a.showFilePicker("", func(path string) {
				a.addFileToContext(path)
			})
		}()
		return nil // consume the event - don't type @
	}

	// Handle prompt mode
	if a.promptActive {
		switch event.Rune() {
		case 'y', 'Y':
			a.promptResponse <- true
			return nil
		case 'n', 'N':
			a.promptResponse <- false
			return nil
		}
		return nil // consume all input when prompt is active
	}

	if event.Key() == tcell.KeyCtrlL {
		a.chatView.Clear()
		a.agent.Clear()
		a.totalTokens = 0
		a.updateStatusBar()
		return nil
	}

	// Ctrl+V, Cmd+V, or terminal paste event to paste images from clipboard
	if event.Key() == tcell.KeyCtrlV || event.Rune() == 0x16 ||
		(event.Modifiers()&tcell.ModCtrl != 0 && (event.Rune() == 'v' || event.Rune() == 'V')) ||
		(event.Modifiers()&tcell.ModMeta != 0 && (event.Rune() == 'v' || event.Rune() == 'V')) ||
		event.Name() == "Paste" {
		a.pasteFromClipboard()
		return nil
	}

	isStreaming := a.phase != PhaseIdle
	if event.Key() == tcell.KeyTab && !isStreaming {
		a.toggleMode()
		return nil
	}

	// Escape clears input and pending content
	if event.Key() == tcell.KeyEscape {
		a.input.SetText("", true)
		a.clearPendingContent()
		return nil
	}

	if event.Key() == tcell.KeyEnter && !isStreaming {
		a.submitInput()
		return nil
	}

	return event
}

func (a *App) promptUser(message string) (bool, error) {
	a.promptResponse = make(chan bool, 1)
	a.promptActive = true

	a.app.QueueUpdateDraw(func() {
		fmt.Fprint(a.chatView, markdown.FormatPrompt("Confirm Command", message, a.chatWidth))
		a.input.SetPlaceholder("y/n")
	})

	var result bool
	select {
	case result = <-a.promptResponse:
	case <-a.ctx.Done():
		a.promptActive = false
		a.app.QueueUpdateDraw(func() {
			a.input.SetPlaceholder("Ask anything...")
		})
		return false, a.ctx.Err()
	}

	a.promptActive = false
	a.app.QueueUpdateDraw(func() {
		a.input.SetPlaceholder("Ask anything...")
	})

	return result, nil
}

func (a *App) pasteFromClipboard() {
	contents, err := clipboard.Read()
	if err != nil || len(contents) == 0 {
		return
	}

	for _, c := range contents {
		if c.Image != nil {
			a.pendingContent = append(a.pendingContent, agent.Content{Image: c.Image})
		}
	}

	a.updateInputHint()
}

func (a *App) clearPendingContent() {
	a.pendingContent = nil
	a.pendingFiles = nil
	a.updateInputHint()
}

func (a *App) countPendingImages() int {
	count := 0

	for _, c := range a.pendingContent {
		if c.Image != nil {
			count++
		}
	}

	return count
}

func (a *App) submitInput() {
	if a.phase != PhaseIdle {
		return
	}

	query := strings.TrimSpace(a.input.GetText())

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
		fmt.Fprintf(a.chatView, "  [%s]/model[-]  - Select AI model\n", t.BrCyan)
		fmt.Fprintf(a.chatView, "  [%s]/file[-]   - Add file to context (or type @filename)\n", t.BrCyan)
		fmt.Fprintf(a.chatView, "  [%s]/paste[-]  - Paste from clipboard (Ctrl+V / Cmd+V / Paste)\n", t.BrCyan)
		fmt.Fprintf(a.chatView, "  [%s]/rewind[-] - Restore to previous checkpoint\n", t.BrCyan)
		fmt.Fprintf(a.chatView, "  [%s]/diff[-]   - Show changes from baseline\n", t.BrCyan)
		fmt.Fprintf(a.chatView, "  [%s]/clear[-]  - Clear chat history\n", t.BrCyan)
		fmt.Fprintf(a.chatView, "  [%s]/quit[-]   - Exit application\n\n", t.BrCyan)
		a.chatView.ScrollToEnd()
		return

	case "/file":
		a.input.SetText("", true)
		go a.showFilePicker("", func(path string) {
			a.addFileToContext(path)
		})
		return

	case "/paste":
		a.input.SetText("", true)
		a.pasteFromClipboard()
		return

	case "/models", "/model":
		a.input.SetText("", true)
		a.showModelPicker()
		return

	case "/rewind":
		a.input.SetText("", true)
		a.showRewindPicker()
		return

	case "/diff":
		a.input.SetText("", true)
		a.showDiffView()
		return
	}

	a.switchToChat()
	a.app.ForceDraw() // Ensure chatWidth is set before printing user message
	a.input.SetText("", true)

	imageCount := a.countPendingImages()
	fileCount := len(a.pendingFiles)

	if imageCount > 0 || fileCount > 0 {
		var attachments []string
		if imageCount > 0 {
			attachments = append(attachments, fmt.Sprintf("📷 %d image(s)", imageCount))
		}
		if fileCount > 0 {
			attachments = append(attachments, fmt.Sprintf("📄 %d file(s)", fileCount))
		}
		fmt.Fprint(a.chatView, markdown.FormatUserMessage(fmt.Sprintf("%s\n[%s]", query, strings.Join(attachments, ", ")), a.chatWidth))
	} else {
		fmt.Fprint(a.chatView, markdown.FormatUserMessage(query, a.chatWidth))
	}

	input := []agent.Content{{Text: query}}
	input = append(input, a.pendingContent...)
	a.clearPendingContent()

	go func() {
		instructions := a.config.AgentInstructions

		if a.currentMode == ModePlan {
			instructions = a.config.PlanningInstructions
		}

		a.streamResponse(input, instructions, a.allTools())
	}()
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

// UI setup and layout

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

		// Re-render chat on resize (only when idle and not in welcome mode)
		isStreaming := a.phase != PhaseIdle
		if newWidth != a.chatWidth && !a.isWelcomeMode && !isStreaming && len(a.agent.Messages()) > 0 {
			messages := a.agent.Messages()
			a.renderChat(messages, "", "", "")
		}

		a.chatWidth = newWidth
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

// View updates

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

	var parts []string

	imageCount := a.countPendingImages()
	if imageCount > 0 {
		parts = append(parts, fmt.Sprintf("[%s]📎 %d image(s)[-]", t.Cyan, imageCount))
	}

	fileCount := len(a.pendingFiles)
	if fileCount > 0 {
		parts = append(parts, fmt.Sprintf("[%s]📄 %d file(s)[-]", t.Cyan, fileCount))
	}

	isStreaming := a.phase != PhaseIdle
	if !isStreaming {
		parts = append(parts, fmt.Sprintf("[%s]enter[-] [%s]send[-]  [%s]tab[-] [%s]mode[-]  [%s]@[-] [%s]file[-]  [%s]esc[-] [%s]clear[-]", t.BrBlack, t.Foreground, t.BrBlack, t.Foreground, t.BrBlack, t.Foreground, t.BrBlack, t.Foreground))
	}

	a.inputHint.SetText(strings.Join(parts, "  "))
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

// Chat rendering (inlined from ChatRenderer)

func (a *App) renderChat(messages []agent.Message, streamingContent string, toolName string, toolHint string) {
	a.chatView.Clear()

	for _, msg := range messages {
		a.renderMessage(msg)
	}

	if streamingContent != "" {
		fmt.Fprint(a.chatView, markdown.FormatAssistantMessage(streamingContent, a.chatWidth))
	}

	if toolName != "" {
		fmt.Fprint(a.chatView, markdown.FormatToolProgress(toolName, toolHint, a.chatWidth))
	}
}

func (a *App) renderMessage(msg agent.Message) {
	if msg.ToolResult != nil {
		output := msg.ToolResult.Content[0].Text
		if len(output) > maxToolOutputLen {
			output = output[:maxToolOutputLen] + "..."
		}
		hint := extractToolHint(msg.ToolResult.Args)
		fmt.Fprint(a.chatView, markdown.FormatToolCall(msg.ToolResult.Name, hint, output, a.chatWidth))
		return
	}

	content := ""
	if len(msg.Content) > 0 {
		content = msg.Content[0].Text
	}

	switch msg.Role {
	case agent.RoleUser:
		fmt.Fprint(a.chatView, markdown.FormatUserMessage(content, a.chatWidth))
	case agent.RoleAssistant:
		fmt.Fprint(a.chatView, markdown.FormatAssistantMessage(content, a.chatWidth))
	}
}
