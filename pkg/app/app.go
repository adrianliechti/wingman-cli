package app

import (
	"context"
	"fmt"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/adrianliechti/wingman-cli/pkg/agent"
	"github.com/adrianliechti/wingman-cli/pkg/config"
	"github.com/adrianliechti/wingman-cli/pkg/plan"
	"github.com/adrianliechti/wingman-cli/pkg/rewind"
	"github.com/adrianliechti/wingman-cli/pkg/tool"
	"github.com/adrianliechti/wingman-cli/pkg/tool/mcp"
)

type App struct {
	// Core dependencies
	app    *tview.Application
	agent  *agent.Agent
	config *config.Config
	ctx    context.Context

	// UI Components
	pages         *tview.Pages
	chatView      *tview.TextView
	errorView     *tview.TextView
	mcpStatusView *tview.TextView
	welcomeView   *tview.TextView
	input         *tview.TextArea
	statusBar     *tview.TextView
	inputHint     *tview.TextView

	// Layout containers
	mainContent   *tview.Flex
	chatContainer *tview.Flex
	inputSection  *tview.Flex
	inputFrame    *tview.Frame
	mainLayout    *tview.Flex

	// Components
	spinner *Spinner

	// State
	phase            AppPhase
	currentMode      Mode
	isWelcomeMode    bool
	pickerActive     bool
	filePickerActive bool
	promptActive     bool
	promptResponse   chan bool
	totalTokens      int64
	startupError     string
	chatWidth        int
	pendingContent   []agent.Content
	pendingFiles     []string

	// Stream cancellation
	streamCancel context.CancelFunc
	streamMu     sync.Mutex

	// Plan state
	plan *plan.Plan

	// MCP state
	mcpManager    *mcp.Manager
	mcpTools      []tool.Tool
	mcpMu         sync.Mutex
	mcpError      error
	mcpConnecting bool

	// Rewind state
	rewind *rewind.Manager
}

func New(ctx context.Context, cfg *config.Config, ag *agent.Agent) *App {
	tvApp := tview.NewApplication()

	a := &App{
		app:           tvApp,
		agent:         ag,
		config:        cfg,
		ctx:           ctx,
		isWelcomeMode: true,
		phase:         PhaseIdle,
		plan:          &plan.Plan{},
	}

	cfg.Environment.PromptUser = a.promptUser
	cfg.Environment.Plan = a.plan

	rm, err := rewind.New(cfg.Environment.WorkingDir())
	if err != nil {
		a.startupError = fmt.Sprintf("rewind init failed: %v", err)
	} else {
		a.rewind = rm
	}

	return a
}

func (a *App) stop() {
	if a.rewind != nil {
		a.rewind.Cleanup()
	}

	a.app.EnableMouse(false)
	a.app.Stop()
}

func (a *App) Run() error {
	a.setupUI()

	// Auto-select model if not configured
	a.autoSelectModel()

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

	mainLayout := a.buildLayout()
	a.spinner = NewSpinner(a.app, a.inputHint, a.updateInputHint)
	a.pages = tview.NewPages()
	a.pages.SetBackgroundColor(tcell.ColorDefault)
	a.pages.AddPage("main", mainLayout, true, true)

	return a.app.SetRoot(a.pages, true).EnableMouse(true).Run()
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

func (a *App) toggleMode() {
	if a.currentMode == ModeAgent {
		a.currentMode = ModePlan
	} else {
		a.currentMode = ModeAgent
	}
	a.updateStatusBar()
}

func (a *App) allTools() []tool.Tool {
	a.mcpMu.Lock()
	defer a.mcpMu.Unlock()

	return append(a.config.Tools, a.mcpTools...)
}
