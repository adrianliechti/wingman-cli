package app

import (
	"context"
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
	pages       *tview.Pages
	chatView    *tview.TextView
	welcomeView *tview.TextView
	input       *tview.TextArea
	statusBar   *tview.TextView
	inputHint   *tview.TextView

	// Layout containers
	mainContent   *tview.Flex
	chatContainer *tview.Flex
	inputSection  *tview.Flex
	inputFrame    *tview.Frame
	mainLayout    *tview.Flex

	// Components
	spinner *Spinner

	// State
	phase          AppPhase
	currentMode    Mode
	isWelcomeMode  bool
	activeModal    Modal
	promptActive   bool
	promptResponse chan bool
	totalTokens    int64
	chatWidth      int
	pendingContent []agent.Content
	pendingFiles   []string

	// Stream cancellation
	streamCancel context.CancelFunc
	streamMu     sync.Mutex

	// Plan state
	plan *plan.Plan

	// MCP state
	mcpManager *mcp.Manager
	mcpTools   []tool.Tool
	mcpMu      sync.Mutex
	mcpError   error

	// Rewind state
	rewind      *rewind.Manager
	rewindReady chan struct{}
}

func New(ctx context.Context, cfg *config.Config, ag *agent.Agent) *App {
	tvApp := tview.NewApplication()

	a := &App{
		app:           tvApp,
		agent:         ag,
		config:        cfg,
		ctx:           ctx,
		isWelcomeMode: true,
		phase:         PhasePreparing,
		plan:          &plan.Plan{},
		rewindReady:   make(chan struct{}),
	}

	cfg.Environment.PromptUser = a.promptUser
	cfg.Environment.Plan = a.plan

	// Initialize rewind asynchronously to avoid blocking startup
	go func() {
		defer close(a.rewindReady)
		if rm, err := rewind.New(cfg.Environment.WorkingDir()); err == nil {
			a.rewind = rm
		}
	}()

	return a
}

func (a *App) stop() {
	// Wait briefly for rewind to be ready, then cleanup
	select {
	case <-a.rewindReady:
		if a.rewind != nil {
			a.rewind.Cleanup()
		}
	default:
		// Rewind not ready yet, nothing to cleanup
	}

	a.app.EnableMouse(false)
	a.app.Stop()
}

func (a *App) Run() error {
	a.setupUI()

	// Auto-select model if not configured
	a.autoSelectModel()

	if a.config.MCP != nil {
		go func() {
			err := a.initMCP()
			if err != nil || a.mcpError != nil {
				if err == nil {
					err = a.mcpError
				}
				a.app.QueueUpdateDraw(func() {
					a.showError("MCP initialization failed", err)
				})
			}
		}()
	}

	mainLayout := a.buildLayout()
	a.spinner = NewSpinner(a.app, a.inputHint)
	a.pages = tview.NewPages()
	a.pages.SetBackgroundColor(tcell.ColorDefault)
	a.pages.AddPage("main", mainLayout, true, true)

	// Show "Preparing..." while rewind initializes, then transition to idle
	a.spinner.Start(PhasePreparing, "")
	go func() {
		<-a.rewindReady
		a.app.QueueUpdateDraw(func() {
			a.spinner.Stop()
			a.phase = PhaseIdle
			a.updateInputHint()
		})
	}()

	return a.app.SetRoot(a.pages, true).EnableMouse(true).EnablePaste(true).Run()
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

// hasActiveModal returns true if any modal is currently open
func (a *App) hasActiveModal() bool {
	return a.activeModal != ModalNone
}

// closeActiveModal closes the currently active modal
func (a *App) closeActiveModal() {
	switch a.activeModal {
	case ModalPicker:
		a.closePicker()
	case ModalFilePicker:
		a.closeFilePicker()
	case ModalDiff:
		a.closeDiffView()
	case ModalPlan:
		a.closePlanView()
	}
}

func (a *App) allTools() []tool.Tool {
	a.mcpMu.Lock()
	defer a.mcpMu.Unlock()

	return append(a.config.Tools, a.mcpTools...)
}

func (a *App) isToolHidden(name string) bool {
	for _, t := range a.allTools() {
		if t.Name == name {
			return t.Hidden
		}
	}

	return false
}
