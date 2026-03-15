package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/adrianliechti/wingman-cli/pkg/agent"
	"github.com/adrianliechti/wingman-cli/pkg/rewind"
	"github.com/adrianliechti/wingman-cli/pkg/tool"
	"github.com/adrianliechti/wingman-cli/pkg/tool/mcp"
)

type App struct {
	// Core dependencies
	ctx context.Context
	app *tview.Application

	agent *agent.Agent
	//config *agent.Config

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

	// MCP state
	mcpManager *mcp.Manager
	mcpTools   []tool.Tool
	mcpMu      sync.Mutex
	mcpError   error

	// Confirm dialog state
	confirmResponse chan bool

	// Rewind state
	rewind      *rewind.Manager
	rewindReady chan struct{}
}

func New(ctx context.Context, agent *agent.Agent) *App {
	app := tview.NewApplication()

	a := &App{
		ctx: ctx,
		app: app,

		agent: agent,

		isWelcomeMode: true,
		phase:         PhasePreparing,
		rewindReady:   make(chan struct{}),
	}

	agent.Environment.PromptUser = a.promptUser

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

	// Explicitly disable mouse tracking modes that tview may have enabled.
	// tview's screen.Fini() should handle this, but a race between terminal
	// restore and pending mouse events can leak escape sequences to the shell.
	fmt.Fprint(os.Stdout, "\033[?1000l\033[?1002l\033[?1003l\033[?1006l")
}

func (a *App) Run() error {
	a.setupUI()

	// Auto-select model if not configured
	a.autoSelectModel()

	if a.agent.MCP != nil {
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

	// Initialize rewind asynchronously
	go func() {
		defer close(a.rewindReady)

		workDir := a.agent.Environment.WorkingDir()
		gitDir := filepath.Join(workDir, ".git")

		if _, err := os.Stat(gitDir); os.IsNotExist(err) {
			if !a.showConfirm("Current directory is not a git repository. Continue?") {
				a.app.QueueUpdate(func() {
					a.stop()
				})
				return
			}
		}

		if rm, err := rewind.New(workDir); err == nil {
			a.rewind = rm
		}
	}()

	go func() {
		<-a.rewindReady
		a.app.QueueUpdateDraw(func() {
			a.spinner.Stop()
			a.phase = PhaseIdle
			a.updateInputHint()
		})
	}()

	root := &pasteInterceptRoot{
		Primitive: a.pages,

		intercept: func(text string) bool {
			paths := detectFilePaths(text, a.agent.Environment.WorkingDir())

			if len(paths) == 0 {
				return false
			}

			for _, p := range paths {
				a.addFileToContext(normalizeFilePath(p, a.agent.Environment.WorkingDir()))
			}

			a.app.QueueUpdateDraw(func() {
				a.updateInputHint()
			})

			return true
		},
	}

	return a.app.SetRoot(root, true).EnableMouse(true).EnablePaste(true).Run()
}

func (a *App) initMCP() error {
	if a.agent.MCP == nil {
		return nil
	}

	a.mcpManager = a.agent.MCP

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
	case ModalConfirm:
		a.closeConfirm(false)
	}
}

func (a *App) showConfirm(message string) bool {
	a.confirmResponse = make(chan bool, 1)

	a.app.QueueUpdateDraw(func() {
		modal := tview.NewModal().
			SetText(message).
			AddButtons([]string{"Yes", "No"}).
			SetDoneFunc(func(buttonIndex int, buttonLabel string) {
				a.closeConfirm(buttonLabel == "Yes")
			})

		a.activeModal = ModalConfirm
		a.pages.AddPage("confirm", modal, true, true)
	})

	return <-a.confirmResponse
}

func (a *App) closeConfirm(result bool) {
	a.activeModal = ModalNone

	if a.pages != nil {
		a.pages.RemovePage("confirm")
		a.app.SetFocus(a.input)
	}

	if a.confirmResponse != nil {
		select {
		case a.confirmResponse <- result:
		default:
		}
	}
}

func (a *App) allTools() []tool.Tool {
	a.mcpMu.Lock()
	defer a.mcpMu.Unlock()

	return append(a.agent.Tools, a.mcpTools...)
}

func (a *App) isToolHidden(name string) bool {
	for _, t := range a.allTools() {
		if t.Name == name {
			return t.Hidden
		}
	}

	return false
}
