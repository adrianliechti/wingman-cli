package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"golang.org/x/term"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/skill"

	"github.com/adrianliechti/wingman-agent/pkg/ui/clipboard"
	"github.com/adrianliechti/wingman-agent/pkg/ui/theme"
)

const maxToolOutputLen = 500
const compactWidthThreshold = 100
const compactHeightThreshold = 22

// isCompactMode returns true if padding should be removed (small screen or vscode caller)
func (a *App) isCompactMode() bool {
	if os.Getenv("WINGMAN_CALLER") == "vscode" {
		return true
	}

	// Try to get terminal size directly
	if width, height, err := term.GetSize(int(os.Stdout.Fd())); err == nil && width > 0 {
		if width < compactWidthThreshold || height < compactHeightThreshold {
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

	// Ctrl+E: toggle tool output expansion
	if event.Key() == tcell.KeyCtrlE && !a.hasActiveModal() {
		a.toolOutputExpanded = !a.toolOutputExpanded

		if !a.showWelcome && !a.isStreaming() && len(a.agent.Messages()) > 0 {
			a.renderChat(a.agent.Messages(), "", "", "")
		}

		a.updateInputHint()

		return nil
	}

	// Let modal handle its own events
	if a.hasActiveModal() {
		return event
	}

	// Handle @ to trigger file picker (don't insert @ into input)
	if event.Rune() == '@' && !a.isStreaming() {
		go func() {
			a.showFilePicker("", func(paths []string) {
				for _, p := range paths {
					a.addFileToContext(p)
				}
			})
		}()

		return nil // consume the event - don't type @
	}

	// Handle ask mode (free-text input, submit with Enter)
	if a.askActive {
		if event.Key() == tcell.KeyEnter {
			text := strings.TrimSpace(a.input.GetText())

			if text == "" {
				return nil // don't submit empty answers
			}

			a.input.SetText("", true)
			fmt.Fprint(a.chatView, a.formatUserMessage(text))
			a.setPhase(PhaseThinking)
			a.askResponse <- text

			return nil
		}

		return event
	}

	// Handle prompt mode
	if a.promptActive {
		switch event.Rune() {
		case 'y', 'Y':
			fmt.Fprint(a.chatView, a.formatUserMessage("Yes"))
			a.setPhase(PhaseThinking)
			a.promptResponse <- true

			return nil

		case 'n', 'N':
			fmt.Fprint(a.chatView, a.formatUserMessage("No"))
			a.setPhase(PhaseThinking)
			a.promptResponse <- false

			return nil
		}

		return nil // consume all input when prompt is active
	}

	// Ctrl+Y: copy last assistant message to clipboard
	if event.Key() == tcell.KeyCtrlY && !a.hasActiveModal() {
		a.copyLastResponse()
		return nil
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

func (a *App) resetPlaceholder() {
	a.app.QueueUpdateDraw(func() {
		a.input.SetPlaceholder("Ask anything...")
	})
}

func (a *App) promptUser(message string) (bool, error) {
	// Serialize prompts — tool calls may run concurrently
	a.promptMu.Lock()
	defer a.promptMu.Unlock()

	a.promptResponse = make(chan bool, 1)
	a.promptActive = true
	defer func() {
		a.promptActive = false
		a.resetPlaceholder()
	}()

	t := theme.Default
	hint := fmt.Sprintf("[%s]Press [-][%s::b]y[-::-][%s] to approve, [-][%s::b]n[-::-][%s] to deny[-]", t.BrBlack, t.Green, t.BrBlack, t.Red, t.BrBlack)

	a.app.QueueUpdateDraw(func() {
		fmt.Fprint(a.chatView, a.formatPrompt("Confirm Command", message, hint))
		a.input.SetPlaceholder("y/n")
		a.app.SetFocus(a.input)
	})

	select {
	case result := <-a.promptResponse:
		return result, nil
	case <-a.ctx.Done():
		return false, a.ctx.Err()
	}
}

func (a *App) askUser(question string) (string, error) {
	a.askResponse = make(chan string, 1)
	a.askActive = true
	defer func() {
		a.askActive = false
		a.resetPlaceholder()
	}()

	a.app.QueueUpdateDraw(func() {
		fmt.Fprint(a.chatView, a.formatPrompt("Question", question, ""))
		a.input.SetPlaceholder("Type your answer and press Enter...")
		a.app.SetFocus(a.input)
	})

	select {
	case result := <-a.askResponse:
		return result, nil
	case <-a.ctx.Done():
		return "", a.ctx.Err()
	}
}

func (a *App) copyLastResponse() {
	messages := a.agent.Messages()

	// Find the last assistant message (walking backwards)
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == agent.RoleAssistant {
			for _, c := range messages[i].Content {
				if c.Text != "" {
					clipboard.WriteText(c.Text)
					fmt.Fprint(a.chatView, a.formatNotice("Copied to clipboard", theme.Default.BrBlack))

					return
				}
			}
		}
	}
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
			// Check if the clipboard text contains file paths
			paths := detectFilePaths(c.Text, a.agent.Environment.WorkingDir())
			if len(paths) > 0 {
				for _, p := range paths {
					a.addFileToContext(normalizeFilePath(p, a.agent.Environment.WorkingDir()))
				}

				continue
			}

			// Get selection range (start, end are byte positions)
			_, start, end := a.input.GetSelection()
			a.input.Replace(start, end, c.Text)
		}
	}

	a.updateInputHint()
}

func (a *App) cancelStream() {
	// Cancel the stream context first
	a.streamMu.Lock()
	if a.streamCancel != nil {
		a.streamCancel()
	}
	a.streamMu.Unlock()

	// Unblock any pending ask/prompt so the stream goroutine can exit
	if a.askActive {
		a.input.SetText("", true)

		select {
		case a.askResponse <- "":
		default:
		}
	}

	if a.promptActive {
		select {
		case a.promptResponse <- false:
		default:
		}
	}
}

func (a *App) clearPendingContent() {
	a.pendingContent = nil
	a.pendingFiles = nil
	a.updateInputHint()
}

func (a *App) clearChat() {
	a.chatView.Clear()
	a.agent.Clear()
	a.inputTokens = 0
	a.outputTokens = 0
	a.updateStatusBar()
}

func (a *App) showError(title string, err error) {
	a.switchToChat()
	fmt.Fprint(a.chatView, a.formatError(title, err.Error()))
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
		if a.rewind != nil {
			fmt.Fprintf(a.chatView, "  [%s]/diff[-]   - Show changes from baseline\n", t.BrCyan)
			fmt.Fprintf(a.chatView, "  [%s]/review[-]  - Review code changes with AI\n", t.BrCyan)
			fmt.Fprintf(a.chatView, "  [%s]/rewind[-] - Restore to previous checkpoint\n", t.BrCyan)
		}
		fmt.Fprintf(a.chatView, "  [%s]/clear[-]  - Clear chat history\n", t.BrCyan)
		fmt.Fprintf(a.chatView, "  [%s]/quit[-]   - Exit application\n", t.BrCyan)

		if len(a.agent.Skills) > 0 {
			fmt.Fprintf(a.chatView, "\n[%s::b]Skills[-::-]\n", t.Cyan)
			for _, s := range a.agent.Skills {
				fmt.Fprintf(a.chatView, "  [%s]/%s[-] - %s\n", t.BrCyan, s.Name, s.Description)
			}
		}

		fmt.Fprint(a.chatView, "\n")
		a.chatView.ScrollToEnd()

		return

	case "/file":
		a.input.SetText("", true)
		go a.showFilePicker("", func(paths []string) {
			for _, p := range paths {
				a.addFileToContext(p)
			}
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
		a.switchToChat()
		a.showRewindPicker()

		return

	case "/diff":
		a.input.SetText("", true)
		a.switchToChat()
		a.showDiffView()

		return

	}

	// Check for skill slash commands: /skill-name [args]
	if strings.HasPrefix(query, "/") {
		parts := strings.SplitN(query[1:], " ", 2)
		skillName := parts[0]
		skillArgs := ""
		if len(parts) > 1 {
			skillArgs = parts[1]
		}

		if s := skill.FindSkill(skillName, a.agent.Skills); s != nil {
			a.input.SetText("", true)
			a.invokeSkill(s, skillArgs)
			return
		}
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
		displayText = fmt.Sprintf("%s\n[%s]%s[-]", query, theme.Default.BrBlack, strings.Join(attachments, ", "))
	}
	fmt.Fprint(a.chatView, a.formatUserMessage(displayText))

	// Build input for agent - display text plus hidden file list for context
	input := []agent.Content{{Text: displayText}}
	input = append(input, a.pendingContent...)

	if len(a.pendingFiles) > 0 {
		var sb strings.Builder
		fmt.Fprint(&sb, "\n[Attached files - use the read tool to access their content]\n")
		for _, f := range a.pendingFiles {
			fmt.Fprintf(&sb, "- %s\n", f)
		}
		input = append(input, agent.Content{Text: sb.String()})
	}

	a.clearPendingContent()

	go func() {
		instructions := a.agent.AgentInstructions

		if a.currentMode == ModePlan {
			instructions = a.agent.PlanningInstructions
		}

		// Append bridge instructions if connected
		if bridgeInstructions := a.bridge.GetInstructions(); bridgeInstructions != "" {
			instructions += "\n\n" + bridgeInstructions
		}

		// Inject bridge context (selection or active file) if connected
		if bridgeContext := a.bridge.GetContext(); bridgeContext != "" {
			input = append(input, agent.Content{Text: bridgeContext})
		}

		a.streamResponse(input, instructions, a.allTools())
	}()
}

func (a *App) invokeSkill(s *skill.Skill, args string) {
	content, err := s.GetContent(a.agent.Environment.WorkingDir())
	if err != nil {
		a.switchToChat()
		fmt.Fprint(a.chatView, a.formatNotice(fmt.Sprintf("Failed to load skill %q: %v", s.Name, err), theme.Default.Red))
		return
	}

	// Apply argument substitution
	content = s.ApplyArguments(content, args)

	// Display as a user message
	a.switchToChat()
	a.app.ForceDraw()

	displayText := fmt.Sprintf("/%s", s.Name)
	if args != "" {
		displayText += " " + args
	}
	fmt.Fprint(a.chatView, a.formatUserMessage(displayText))

	// Send skill content as the prompt
	input := []agent.Content{{Text: content}}
	input = append(input, a.pendingContent...)
	a.clearPendingContent()

	go func() {
		a.streamResponse(input, a.agent.AgentInstructions, a.allTools())
	}()
}

func (a *App) switchToChat() {
	if !a.showWelcome {
		return
	}
	a.showWelcome = false
	a.rebuildContentPages()
}

// rebuildContentPages rebuilds the content area.
// Welcome mode: logo centered at top, chatView pinned at bottom (above input).
// Chat mode: chatView fills the entire area.
func (a *App) rebuildContentPages() {
	a.contentPages.Clear()

	if a.showWelcome && !a.isCompactMode() {
		// Logo centered in upper area, notices pinned above input
		a.contentPages.AddItem(nil, 0, 2, false)
		a.contentPages.AddItem(a.welcomeView, 12, 0, false)
		a.contentPages.AddItem(nil, 0, 3, false)
		a.contentPages.AddItem(a.chatView, 3, 0, false)
	} else {
		a.contentPages.AddItem(a.chatView, 0, 1, false)
	}
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

	a.contentPages = tview.NewFlex().SetDirection(tview.FlexRow)
	a.contentPages.SetBackgroundColor(tcell.ColorDefault)
	a.rebuildContentPages()
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
	bottomBar.AddItem(a.statusBar, 40, 0, false)

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
	a.chatContainer.AddItem(a.contentPages, 0, 1, false)
	a.chatContainer.AddItem(nil, rightMargin, 0, false)

	a.lastCompact = a.isCompactMode()

	a.chatContainer.SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		newWidth := width - totalMargin

		if newWidth != a.chatWidth {
			a.chatWidth = newWidth

			// Re-render chat on resize to re-wrap content to new width
			if !a.showWelcome && len(a.agent.Messages()) > 0 {
				a.renderChat(a.agent.Messages(), "", a.currentToolName, a.currentToolHint)
			}
		}

		// Toggle logo visibility on resize while in welcome mode
		if a.showWelcome {
			compact := a.isCompactMode()
			if compact != a.lastCompact {
				a.lastCompact = compact
				a.rebuildContentPages()
			}
		}

		return x, y, width, height
	})

	a.mainLayout = tview.NewFlex().SetDirection(tview.FlexRow)
	a.mainLayout.
		AddItem(a.chatContainer, 0, 1, false).
		AddItem(a.inputSection, 6, 0, true)

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

	if a.inputTokens > 0 || a.outputTokens > 0 {
		parts = append(parts, fmt.Sprintf("[%s]↑%s ↓%s[-]", t.BrBlack, formatTokens(a.inputTokens), formatTokens(a.outputTokens)))
	}

	parts = append(parts, fmt.Sprintf("[%s]%s[-]", t.Cyan, a.agent.Model))
	parts = append(parts, fmt.Sprintf("[%s]%s[-]", t.Yellow, modeLabel))

	a.statusBar.SetText(strings.Join(parts, " • "))
}

func (a *App) formatShortcut(key, label string) string {
	t := theme.Default
	return fmt.Sprintf("[%s]%s[-] [%s]%s[-]", t.BrBlack, key, t.Foreground, label)
}

func (a *App) updateInputHint() {
	// Don't overwrite inputHint while the spinner owns it
	if a.isStreaming() {
		return
	}

	t := theme.Default

	var parts []string

	imageCount := a.countPendingImages()
	if imageCount == 1 {
		parts = append(parts, fmt.Sprintf("[%s]📎 1 image[-]", t.Cyan))
	} else if imageCount > 1 {
		parts = append(parts, fmt.Sprintf("[%s]📎 %d images[-]", t.Cyan, imageCount))
	}

	if len(a.pendingFiles) == 1 {
		parts = append(parts, fmt.Sprintf("[%s]📄 %s[-]", t.Cyan, filepath.Base(a.pendingFiles[0])))
	} else if len(a.pendingFiles) > 1 {
		parts = append(parts, fmt.Sprintf("[%s]📄 %d files[-]", t.Cyan, len(a.pendingFiles)))
	}

	expandLabel := "expand"
	if a.toolOutputExpanded {
		expandLabel = "collapse"
	}

	if a.bridge.IsConnected() {
		parts = append(parts, fmt.Sprintf("[%s]⬢[-]", t.Green))
	}

	parts = append(parts,
		a.formatShortcut("tab", "mode"),
		a.formatShortcut("shift+tab", "model"),
		a.formatShortcut("@", "file"),
		a.formatShortcut("ctrl+y", "copy"),
		a.formatShortcut("ctrl+e", expandLabel),
		a.formatShortcut("esc", "clear"),
	)

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

	prevWasTool := false

	for _, msg := range messages {
		isTool := isToolMessage(msg)

		// Add separator between tool results and non-tool messages
		if prevWasTool && !isTool {
			fmt.Fprint(a.chatView, "\n")
		}

		a.renderMessage(msg)
		prevWasTool = isTool
	}

	if streamingContent != "" {
		if prevWasTool {
			fmt.Fprint(a.chatView, "\n")
		}
		fmt.Fprint(a.chatView, a.formatAssistantMessage(streamingContent))
	}

	if toolName != "" && !a.isToolHidden(toolName) {
		fmt.Fprint(a.chatView, a.formatToolProgress(toolName, toolHint))
	}

	a.chatView.ScrollToEnd()
}

func isToolMessage(msg agent.Message) bool {
	for _, c := range msg.Content {
		if c.ToolResult != nil {
			return true
		}
	}
	return false
}

func (a *App) renderMessage(msg agent.Message) {
	for _, c := range msg.Content {
		// Tool results
		if c.ToolResult != nil {
			if a.isToolHidden(c.ToolResult.Name) {
				continue
			}

			hint := extractToolHint(c.ToolResult.Args)

			if a.toolOutputExpanded {
				output := c.ToolResult.Content
				if len(output) > maxToolOutputLen {
					output = output[:maxToolOutputLen] + "..."
				}
				fmt.Fprint(a.chatView, a.formatToolCall(c.ToolResult.Name, hint, output))
			} else {
				fmt.Fprint(a.chatView, a.formatToolCallCollapsed(c.ToolResult.Name, hint))
			}

			continue
		}

		// Tool calls have no displayable content
		if c.ToolCall != nil {
			continue
		}

		// Text content
		if c.Text != "" {
			switch msg.Role {
			case agent.RoleUser:
				fmt.Fprint(a.chatView, a.formatUserMessage(c.Text))
			case agent.RoleAssistant:
				fmt.Fprint(a.chatView, a.formatAssistantMessage(c.Text))
			}
		}
	}
}
