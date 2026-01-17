package app

import (
	"context"
	"sync"

	"github.com/rivo/tview"

	"github.com/adrianliechti/wingman-cli/pkg/agent"
	"github.com/adrianliechti/wingman-cli/pkg/config"
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
