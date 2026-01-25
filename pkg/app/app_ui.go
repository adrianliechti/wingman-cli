package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"golang.org/x/term"

	"github.com/adrianliechti/wingman-cli/pkg/agent"
	"github.com/adrianliechti/wingman-cli/pkg/clipboard"
	"github.com/adrianliechti/wingman-cli/pkg/markdown"
	"github.com/adrianliechti/wingman-cli/pkg/theme"
)

const maxToolOutputLen = 500
const compactWidthThreshold = 100

// isCompactMode returns true if padding should be removed (small screen or vscode caller)
func (a *App) isCompactMode() bool {
	if os.Getenv("WINGMAN_CALLER") == "vscode" {
		return true
	}

	// Try to get terminal width directly
	if width, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && width > 0 {
		if width < compactWidthThreshold {
			return true
		}
	}

	// Fallback: chatWidth is set during draw
	if a.chatWidth > 0 && a.chatWidth+6 < compactWidthThreshold {
		return true
	}

	return false
}

// getMargins returns (left, right) margins based on compact mode
func (a *App) getMargins() (int, int) {
	if a.isCompactMode() {
		return 0, 0
	}
	return 2, 4
}

// getInputMargins returns (left, right) margins for input area based on compact mode
func (a *App) getInputMargins() (int, int) {
	if a.isCompactMode() {
		return 0, 0
	}
	return 4, 4
}

// isStreaming returns true if the app is currently processing a request
func (a *App) isStreaming() bool {
	return a.phase != PhaseIdle
}

// Input handling

func (a *App) handleInput(event *tcell.EventKey) *tcell.EventKey {
	// Handle Escape: close modals, cancel stream, or clear input
	if event.Key() == tcell.KeyEscape {
		if a.hasActiveModal() {
			a.closeActiveModal()
			return nil
		}
		if a.isStreaming() {
			a.cancelStream()
			return nil
		}
		// Clear input and pending content when idle
		a.input.SetText("", true)
		a.clearPendingContent()
		return nil
	}

	// Handle Ctrl+C: copy if text selected, else close modals, cancel stream, or stop app
	if event.Key() == tcell.KeyCtrlC {
		if !a.hasActiveModal() && a.input.HasSelection() {
			selectedText, _, _ := a.input.GetSelection()
			if selectedText != "" {
				clipboard.WriteText(selectedText)
				return nil
			}
		}
		if a.hasActiveModal() {
			a.closeActiveModal()
			return nil
		}
		if a.isStreaming() {
			a.cancelStream()
			return nil
		}
		a.stop()
		return nil
	}

	// Let modal handle its own events
	if a.hasActiveModal() {
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
		a.clearChat()
		return nil
	}

	if event.Key() == tcell.KeyTab && !a.isStreaming() {
		a.toggleMode()
		return nil
	}

	// Shift+Tab cycles through models
	if event.Key() == tcell.KeyBacktab && !a.isStreaming() {
		go a.cycleModel()
		return nil
	}

	if event.Key() == tcell.KeyEnter && !a.isStreaming() {
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
			a.pendingContent = append(a.pendingContent, agent.Content{File: &agent.File{Data: *c.Image}})
		}

		if c.Text != "" {
			// Get selection range (start, end are byte positions)
			_, start, end := a.input.GetSelection()
			a.input.Replace(start, end, c.Text)
		}
	}

	a.updateInputHint()
}

func (a *App) cancelStream() {
	a.streamMu.Lock()

	if a.streamCancel != nil {
		a.streamCancel()
	}
	a.streamMu.Unlock()
}

func (a *App) clearPendingContent() {
	a.pendingContent = nil
	a.pendingFiles = nil
	a.updateInputHint()
}

func (a *App) clearChat() {
	a.chatView.Clear()
	a.agent.Clear()
	a.totalTokens = 0
	a.plan.Clear()
	a.updateStatusBar()
}

func (a *App) showError(title string, err error) {
	a.switchToChat()
	width := a.chatWidth
	if width == 0 {
		width = 80
	}
	fmt.Fprint(a.chatView, markdown.FormatError(title, err.Error(), width))
}

func (a *App) countPendingImages() int {
	count := 0

	for _, c := range a.pendingContent {
		if c.File != nil {
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
		a.clearChat()
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
		fmt.Fprintf(a.chatView, "  [%s]/plan[-]   - Show current plan\n", t.BrCyan)
		fmt.Fprintf(a.chatView, "  [%s]/diff[-]   - Show changes from baseline\n", t.BrCyan)
		fmt.Fprintf(a.chatView, "  [%s]/review[-] - Review code changes with AI\n", t.BrCyan)
		fmt.Fprintf(a.chatView, "  [%s]/rewind[-] - Restore to previous checkpoint\n", t.BrCyan)
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

	case "/plan":
		a.input.SetText("", true)
		a.showPlanView()

		return

	case "/review":
		a.input.SetText("", true)
		a.startReview("")

		return
	}

	a.switchToChat()
	a.app.ForceDraw() // Ensure chatWidth is set before printing user message
	a.input.SetText("", true)

	imageCount := a.countPendingImages()

	// Build display text with attachments
	displayText := query
	if imageCount > 0 || len(a.pendingFiles) > 0 {
		var attachments []string
		if imageCount == 1 {
			attachments = append(attachments, "📷 1 image")
		} else if imageCount > 1 {
			attachments = append(attachments, fmt.Sprintf("📷 %d images", imageCount))
		}
		for _, f := range a.pendingFiles {
			attachments = append(attachments, fmt.Sprintf("📄 %s", filepath.Base(f)))
		}
		displayText = fmt.Sprintf("%s\n[%s]", query, strings.Join(attachments, ", "))
	}
	fmt.Fprint(a.chatView, markdown.FormatUserMessage(displayText, a.chatWidth))

	// Build input for agent - display text plus hidden file list for context
	input := []agent.Content{{Text: displayText}}
	input = append(input, a.pendingContent...)

	if len(a.pendingFiles) > 0 {
		var sb strings.Builder
		sb.WriteString("\n[Attached files - use the read tool to access their content]\n")
		for _, f := range a.pendingFiles {
			sb.WriteString(fmt.Sprintf("- %s\n", f))
		}
		input = append(input, agent.Content{Text: sb.String()})
	}

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
	a.mainContent.AddItem(a.chatView, 0, 1, false)

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

	inputBgColor := t.Selection
	a.input = tview.NewTextArea().
		SetPlaceholder("Ask anything...")
	a.input.SetBackgroundColor(inputBgColor)
	a.input.SetBorder(false)
	a.input.SetTextStyle(tcell.StyleDefault.Foreground(t.Foreground).Background(inputBgColor))
	a.input.SetPlaceholderStyle(tcell.StyleDefault.Foreground(t.BrBlack).Background(inputBgColor))

	a.mainContent = tview.NewFlex().SetDirection(tview.FlexRow)
	a.mainContent.AddItem(a.welcomeView, 0, 1, false)
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

	// Handle Ctrl+V/Cmd+V to paste from clipboard (images + text)
	a.input.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		// Check for paste: Ctrl+V or Cmd+V (macOS)
		isPaste := event.Key() == tcell.KeyCtrlV || (event.Modifiers()&tcell.ModMeta != 0 && (event.Rune() == 'v' || event.Rune() == 'V'))

		if isPaste {
			a.pasteFromClipboard()

			return nil // Consume event - we handled the paste
		}

		return event
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

	inputLeftMargin, inputRightMargin := a.getInputMargins()

	bottomBarContainer := tview.NewFlex().SetDirection(tview.FlexColumn)
	bottomBarContainer.AddItem(nil, inputLeftMargin, 0, false)
	bottomBarContainer.AddItem(bottomBar, 0, 1, false)
	bottomBarContainer.AddItem(nil, inputRightMargin, 0, false)

	inputContainer := tview.NewFlex().SetDirection(tview.FlexColumn)
	inputContainer.AddItem(nil, inputLeftMargin, 0, false)
	inputContainer.AddItem(a.inputFrame, 0, 1, true)
	inputContainer.AddItem(nil, inputRightMargin, 0, false)

	a.inputSection = tview.NewFlex().SetDirection(tview.FlexRow)
	a.inputSection.AddItem(inputContainer, 0, 1, true)
	a.inputSection.AddItem(bottomBarContainer, 1, 0, false)

	leftMargin, rightMargin := a.getMargins()
	totalMargin := leftMargin + rightMargin

	a.chatContainer = tview.NewFlex().SetDirection(tview.FlexColumn)
	a.chatContainer.AddItem(nil, leftMargin, 0, false)
	a.chatContainer.AddItem(a.mainContent, 0, 1, false)
	a.chatContainer.AddItem(nil, rightMargin, 0, false)

	a.chatContainer.SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		newWidth := width - totalMargin

		// Re-render chat on resize (only when idle and not in welcome mode)
		if newWidth != a.chatWidth && !a.isWelcomeMode && !a.isStreaming() && len(a.agent.Messages()) > 0 {
			a.renderChat(a.agent.Messages(), "", "", "")
		}
		a.chatWidth = newWidth

		return x, y, width, height
	})

	a.mainLayout = tview.NewFlex().SetDirection(tview.FlexRow)

	if a.isWelcomeMode {
		if a.isCompactMode() {
			// In compact mode, skip the logo and go straight to input
			a.mainLayout.
				AddItem(nil, 0, 1, false).
				AddItem(a.inputSection, 6, 0, true).
				AddItem(nil, 0, 1, false)
		} else {
			a.mainLayout.
				AddItem(nil, 2, 0, false).
				AddItem(a.welcomeView, 12, 0, false).
				AddItem(nil, 0, 1, false).
				AddItem(a.inputSection, 6, 0, true).
				AddItem(nil, 0, 2, false)
		}
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

	var parts []string

	if a.totalTokens > 0 {
		parts = append(parts, fmt.Sprintf("[%s]%s[-]", t.BrBlack, formatTokens(a.totalTokens)))
	}

	if ps := a.planStats(); ps != "" {
		parts = append(parts, ps)
	}

	parts = append(parts, fmt.Sprintf("[%s]%s[-]", t.Cyan, a.config.Model))
	parts = append(parts, fmt.Sprintf("[%s]%s[-]", t.Yellow, modeLabel))

	a.statusBar.SetText(strings.Join(parts, " • "))
}

func (a *App) updateInputHint() {
	t := theme.Default

	var parts []string

	imageCount := a.countPendingImages()
	if imageCount == 1 {
		parts = append(parts, fmt.Sprintf("[%s]📎 1 image[-]", t.Cyan))
	} else if imageCount > 1 {
		parts = append(parts, fmt.Sprintf("[%s]📎 %d images[-]", t.Cyan, imageCount))
	}

	if len(a.pendingFiles) == 1 {
		name := filepath.Base(a.pendingFiles[0])
		parts = append(parts, fmt.Sprintf("[%s]📄 %s[-]", t.Cyan, name))
	} else if len(a.pendingFiles) > 1 {
		parts = append(parts, fmt.Sprintf("[%s]📄 %d files[-]", t.Cyan, len(a.pendingFiles)))
	}

	if !a.isStreaming() {
		parts = append(parts, fmt.Sprintf("[%s]tab[-] [%s]mode[-]  [%s]shift+tab[-] [%s]model[-]  [%s]@[-] [%s]file[-]  [%s]esc[-] [%s]clear[-]", t.BrBlack, t.Foreground, t.BrBlack, t.Foreground, t.BrBlack, t.Foreground, t.BrBlack, t.Foreground))
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

// Chat rendering (inlined from ChatRenderer)

func (a *App) renderChat(messages []agent.Message, streamingContent string, toolName string, toolHint string) {
	a.chatView.Clear()

	for _, msg := range messages {
		a.renderMessage(msg)
	}

	if streamingContent != "" {
		fmt.Fprint(a.chatView, markdown.FormatAssistantMessage(streamingContent, a.chatWidth))
	}

	if toolName != "" && !a.isToolHidden(toolName) {
		fmt.Fprint(a.chatView, markdown.FormatToolProgress(toolName, toolHint, a.chatWidth))
	}
}

func (a *App) renderMessage(msg agent.Message) {
	// Check for tool result in content
	for _, c := range msg.Content {
		if c.ToolResult != nil {
			if a.isToolHidden(c.ToolResult.Name) {
				return
			}
			output := c.ToolResult.Content

			if len(output) > maxToolOutputLen {
				output = output[:maxToolOutputLen] + "..."
			}
			hint := extractToolHint(c.ToolResult.Args)
			fmt.Fprint(a.chatView, markdown.FormatToolCall(c.ToolResult.Name, hint, output, a.chatWidth))

			return
		}
	}

	// Get first text content for display
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
