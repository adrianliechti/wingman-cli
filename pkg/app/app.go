package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/bridge"
	"github.com/adrianliechti/wingman-agent/pkg/agent/lsp"
	"github.com/adrianliechti/wingman-agent/pkg/agent/mcp"
	"github.com/adrianliechti/wingman-agent/pkg/agent/rewind"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"

	lsptool "github.com/adrianliechti/wingman-agent/pkg/agent/tool/lsp"
	mcptool "github.com/adrianliechti/wingman-agent/pkg/agent/tool/mcp"
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
	phase              AppPhase
	currentMode        Mode
	isWelcomeMode      bool
	activeModal        Modal
	promptActive       bool
	promptResponse     chan bool
	promptMu           sync.Mutex
	askActive          bool
	askResponse        chan string
	toolOutputExpanded bool
	totalTokens        int64
	chatWidth          int
	lastWelcomeCompact bool
	pendingContent     []agent.Content
	pendingFiles       []string

	// Stream cancellation
	streamCancel context.CancelFunc
	streamMu     sync.Mutex

	// Current tool progress (for sub-agent status updates)
	currentToolName string
	currentToolHint string


	// MCP state
	mcpManager *mcp.Manager
	mcpTools   []tool.Tool
	mcpMu      sync.Mutex
	mcpError   error

	// Bridge state (VS Code IDE integration)
	bridge *bridge.Bridge

	// LSP state
	lspManager  *lsp.Manager
	lspTracker  *lsp.DiagnosticTracker
	lspTool     tool.Tool

	// Confirm dialog state
	confirmResponse chan bool

	// Rewind state
	rewind      *rewind.Manager
	rewindReady chan struct{}
}

func New(ctx context.Context, agent *agent.Agent) *App {
	app := tview.NewApplication()

	lspManager := lsp.NewManager(agent.Environment.WorkingDir())

	a := &App{
		ctx: ctx,
		app: app,

		agent: agent,

		isWelcomeMode: true,
		phase:         PhasePreparing,

		lspManager: lspManager,
		lspTracker: lsp.NewDiagnosticTracker(),
		lspTool:    lsptool.NewTool(lspManager),

		rewindReady: make(chan struct{}),
	}

	agent.Environment.AskUser = a.askUser
	agent.Environment.PromptUser = a.promptUser
	agent.Environment.DiagnoseFile = a.lspDiagnostics
	agent.Environment.StatusUpdate = func(status string) {
		a.currentToolHint = status
		a.render("", a.currentToolName, status)
	}

	return a
}

func (a *App) stop() {
	// Shut down cached LSP servers
	if a.lspManager != nil {
		a.lspManager.Close()
	}

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

		a.app.QueueUpdateDraw(func() {
			a.updateStatusBar()
		})
	}()

	mainLayout := a.buildLayout()
	a.spinner = NewSpinner(a.app, a.inputHint)
	a.pages = tview.NewPages()
	a.pages.SetBackgroundColor(tcell.ColorDefault)
	a.pages.AddPage("main", mainLayout, true, true)

	// Show "Preparing..." while rewind initializes, then transition to idle
	a.spinner.Start(PhasePreparing)

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
	if a.agent.MCP != nil {
		a.mcpManager = a.agent.MCP

		if err := a.mcpManager.Connect(a.ctx); err != nil {
			a.mcpError = err
		}
	} else {
		a.mcpManager = mcp.NewManager(&mcp.Config{})
	}

	// Auto-discover VS Code bridge from lockfiles
	a.bridge = bridge.Setup(a.ctx, a.agent.Environment.WorkingDir(), a.mcpManager)

	mcpTools, err := mcptool.Tools(a.ctx, a.mcpManager)

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

	tools := append([]tool.Tool{}, a.agent.Tools...)

	tools = append(tools, a.mcpTools...)
	tools = append(tools, a.lspTool)

	return tools
}

func (a *App) lspDiagnostics(ctx context.Context, path string) string {
	absPath := path
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(a.lspManager.WorkingDir(), path)
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	uri := lsp.FileURI(absPath)

	session, err := a.lspManager.GetSession(ctx, absPath)
	if err != nil {
		return ""
	}

	// Capture baseline diagnostics before syncing the changed file content.
	// This lets us diff against what existed before the edit.
	baselineDiags := session.PushDiagnostics(uri)
	if len(baselineDiags) == 0 {
		baselineDiags = session.CollectDiagnostics(ctx, uri)
	}
	a.lspTracker.SetBaseline(uri, baselineDiags)

	// Clear push diagnostics so we get fresh ones after the change
	session.ClearPushDiagnostics(uri)

	// Now sync the updated file content to the LSP server (sends didChange + didSave)
	if _, err := session.OpenDocument(ctx, absPath); err != nil {
		return ""
	}

	// Wait for new diagnostics
	diags := session.WaitForDiagnostics(ctx, uri)
	if len(diags) == 0 {
		return ""
	}

	// Filter to only new diagnostics, sort by severity, cap volume
	newDiags := a.lspTracker.FilterNew(uri, diags)
	if len(newDiags) == 0 {
		return ""
	}

	// Mark as delivered for cross-turn deduplication
	a.lspTracker.MarkDelivered(uri, newDiags)

	return lsp.FormatNewDiagnostics(newDiags, path, a.lspManager.WorkingDir())
}

func (a *App) isToolHidden(name string) bool {
	for _, t := range a.allTools() {
		if t.Name == name {
			return t.Hidden
		}
	}

	return false
}
