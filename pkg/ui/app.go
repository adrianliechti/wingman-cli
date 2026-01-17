package ui

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/openai/openai-go/v3/responses"

	"github.com/adrianliechti/wingman-cli/pkg/agent"
	"github.com/adrianliechti/wingman-cli/pkg/config"
	"github.com/adrianliechti/wingman-cli/pkg/markdown"
	"github.com/adrianliechti/wingman-cli/pkg/theme"
	"github.com/adrianliechti/wingman-cli/pkg/tool"
	"github.com/adrianliechti/wingman-cli/pkg/tool/mcp"
)

type promptRequest struct {
	message  string
	response chan bool
}

type App struct {
	app           *tview.Application
	agent         *agent.Agent
	config        *config.Config
	chatView      *tview.TextView
	errorView     *tview.TextView
	mcpStatusView *tview.TextView
	input         *tview.TextArea
	mainContent   *tview.Flex
	welcomeView   *tview.TextView
	chatContainer *tview.Flex
	inputSection  *tview.Flex
	inputFrame    *tview.Frame
	mainLayout    *tview.Flex
	isWelcomeMode bool
	isStreaming   bool
	ctx           context.Context
	chatWidth     int
	lastToolName  string

	promptChan   chan promptRequest
	promptMu     sync.Mutex
	promptActive bool

	mcpManager    *mcp.Manager
	mcpTools      []tool.Tool
	mcpMu         sync.Mutex
	mcpError      error
	mcpConnecting bool
	startupError  string

	statusBar   *tview.TextView
	totalTokens int64
}

func New(ctx context.Context, cfg *config.Config, ag *agent.Agent) *App {
	a := &App{
		app:           tview.NewApplication(),
		agent:         ag,
		config:        cfg,
		ctx:           ctx,
		isWelcomeMode: true,
		isStreaming:   false,
		promptChan:    make(chan promptRequest),
	}

	cfg.Environment.PromptUser = a.promptUser

	return a
}

func (a *App) stop() {
	a.app.EnableMouse(false)
	a.app.Stop()
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

func (a *App) Run() error {
	a.setupUI()

	go a.handlePromptRequests()

	if a.config.MCP != nil {
		a.mcpConnecting = true
		a.updateWelcomeView()

		go func() {
			err := a.initMCP()

			a.mcpConnecting = false
			a.app.QueueUpdateDraw(func() {
				a.updateWelcomeView()

				if err != nil || a.mcpError != nil {
					a.renderMCPInitError(err)
				}
			})
		}()
	}

	return a.app.SetRoot(a.buildLayout(), true).EnableMouse(true).Run()
}

func (a *App) initMCP() error {
	if a.config.MCP == nil {
		return nil
	}

	a.mcpManager = a.config.MCP

	if err := a.mcpManager.Connect(a.ctx); err != nil {
		a.mcpError = err
	}

	mcpTools, err := a.mcpManager.Tools(a.ctx)

	if err != nil {
		return err
	}

	a.mcpMu.Lock()
	a.mcpTools = mcpTools
	a.mcpMu.Unlock()

	return nil
}

func (a *App) allTools() []tool.Tool {
	a.mcpMu.Lock()
	defer a.mcpMu.Unlock()

	return append(a.config.Tools, a.mcpTools...)
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

func (a *App) updateStatusBar() {
	t := theme.Default

	if a.totalTokens > 0 {
		a.statusBar.SetText(fmt.Sprintf("[%s]%s[-] • [%s]%s[-]", t.BrBlack, formatTokens(a.totalTokens), t.Cyan, a.config.Model))
	} else {
		a.statusBar.SetText(fmt.Sprintf("[%s]%s[-]", t.Cyan, a.config.Model))
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

func (a *App) buildLayout() *tview.Flex {
	t := theme.Default
	inputBgColor := t.Selection

	a.inputFrame = tview.NewFrame(a.input).
		SetBorders(1, 1, 0, 0, 1, 1)
	a.inputFrame.SetBackgroundColor(inputBgColor)
	a.inputFrame.SetBorder(false)

	a.input.SetChangedFunc(func() {
		a.updateInputHeight()
	})

	bottomBar := tview.NewFlex().SetDirection(tview.FlexColumn)

	inputHint := tview.NewTextView().
		SetDynamicColors(true).
		SetText(fmt.Sprintf("[%s]enter[-] [%s]send[-]", t.BrBlack, t.Foreground))
	inputHint.SetBackgroundColor(tcell.ColorDefault)

	a.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignRight)
	a.updateStatusBar()
	a.statusBar.SetBackgroundColor(tcell.ColorDefault)

	bottomBar.AddItem(inputHint, 0, 1, false)
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

func (a *App) updateInputHeight() {
	text := a.input.GetText()
	lines := strings.Count(text, "\n") + 1

	minHeight := 6
	maxHeight := 13
	height := min(max(lines+5, minHeight), maxHeight)

	a.mainLayout.ResizeItem(a.inputSection, height, 0)
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
