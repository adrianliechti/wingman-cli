package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/bridge"
	"github.com/adrianliechti/wingman-agent/pkg/agent/lsp"
	"github.com/adrianliechti/wingman-agent/pkg/agent/mcp"
	"github.com/adrianliechti/wingman-agent/pkg/agent/rewind"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/ui/theme"

	lsptool "github.com/adrianliechti/wingman-agent/pkg/agent/tool/lsp"
	mcptool "github.com/adrianliechti/wingman-agent/pkg/agent/tool/mcp"
)

type App struct {
	// Core dependencies
	ctx context.Context
	app *tview.Application

	agent *agent.Agent

	// UI Components
	pages       *tview.Pages
	chatView    *tview.TextView
	welcomeView *tview.TextView
	input       *tview.TextArea
	statusBar   *tview.TextView
	inputHint   *tview.TextView

	// Layout containers
	contentPages  *tview.Flex
	chatContainer *tview.Flex
	inputSection  *tview.Flex
	inputFrame    *tview.Frame
	mainLayout    *tview.Flex

	// Components
	spinner *Spinner

	// Session
	sessionID string

	// State
	phase              AppPhase
	currentMode        Mode
	showWelcome        bool
	activeModal        Modal
	promptActive       bool
	promptResponse     chan bool
	promptMu           sync.Mutex
	askActive          bool
	askResponse        chan string
	toolOutputExpanded bool
	inputTokens        int64
	outputTokens       int64
	chatWidth          int
	lastCompact        bool
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

	// Bridge state (VS Code bridge integration)
	bridge *bridge.Bridge

	// LSP state
	lspManager *lsp.Manager
	lspTracker *lsp.DiagnosticTracker
	lspTools   []tool.Tool

	// Rewind state
	rewind      *rewind.Manager
	rewindReady chan struct{}
}

func New(ctx context.Context, agent *agent.Agent, resumeSessionID string) *App {
	app := tview.NewApplication()

	lspManager := lsp.NewManager(agent.Environment.RootDir())

	hasMessages := len(agent.Messages()) > 0

	sessionID := resumeSessionID
	if sessionID == "" {
		sessionID = newSessionID()
	}

	a := &App{
		ctx: ctx,
		app: app,

		agent: agent,

		sessionID:   sessionID,
		showWelcome: !hasMessages && os.Getenv("WINGMAN_CALLER") != "vscode",
		phase:       PhasePreparing,

		lspManager: lspManager,
		lspTracker: lsp.NewDiagnosticTracker(),
		lspTools:   lsptool.NewTools(lspManager),

		rewindReady: make(chan struct{}),
	}

	if agent.Environment != nil {
		agent.Environment.ExitPlanMode()
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

func newSessionID() string {
	return uuid.New().String()
}

func (a *App) saveSession() {
	a.agent.SaveSession(a.sessionID)
}

func (a *App) stop() {
	// Shut down bridge and LSP servers
	a.bridge.Close()

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

	// Save session before stopping
	a.saveSession()

	a.app.EnableMouse(false)
	a.app.Stop()

	// Explicitly disable mouse tracking modes that tview may have enabled.
	// tview's screen.Fini() should handle this, but a race between terminal
	// restore and pending mouse events can leak escape sequences to the shell.
	fmt.Fprint(os.Stdout, "\033[?1000l\033[?1002l\033[?1003l\033[?1006l")

	// Show session summary so the user can resume
	if len(a.agent.Messages()) > 0 {
		usage := a.agent.Usage()
		fmt.Fprintf(os.Stderr, "\n")
		fmt.Fprintf(os.Stderr, "  Tokens: \u2191%s \u2193%s\n", formatTokens(usage.InputTokens), formatTokens(usage.OutputTokens))
		fmt.Fprintf(os.Stderr, "  Resume: wingman --resume %s\n", a.sessionID)
		fmt.Fprintf(os.Stderr, "\n")
	}
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
			a.showMissingLSPHint()
		})
	}()

	mainLayout := a.buildLayout()
	a.spinner = NewSpinner(a.app, a.inputHint)
	a.pages = tview.NewPages()
	a.pages.SetBackgroundColor(tcell.ColorDefault)
	a.pages.AddPage("main", mainLayout, true, true)

	// Show "Preparing..." while rewind initializes, then transition to idle
	a.spinner.Start(PhasePreparing)

	// Initialize rewind asynchronously (only in git repos)
	go func() {
		defer close(a.rewindReady)

		workDir := a.agent.Environment.RootDir()
		gitDir := filepath.Join(workDir, ".git")

		if _, err := os.Stat(gitDir); os.IsNotExist(err) {
			return
		}

		if rm, err := rewind.New(workDir); err == nil {
			a.rewind = rm
		}
	}()

	go func() {
		<-a.rewindReady
		a.app.QueueUpdateDraw(func() {
			a.setPhase(PhaseIdle)

			// Render restored session (from --resume or /resume)
			if messages := a.agent.Messages(); len(messages) > 0 {
				a.switchToChat()
				a.renderChat(messages, "", "", "")

				usage := a.agent.Usage()
				a.inputTokens = usage.InputTokens
				a.outputTokens = usage.OutputTokens
				a.updateStatusBar()
			}
		})
	}()

	root := &pasteInterceptRoot{
		Primitive: a.pages,

		intercept: func(text string) bool {
			paths := detectFilePaths(text, a.agent.Environment.RootDir())

			if len(paths) == 0 {
				return false
			}

			for _, p := range paths {
				a.addFileToContext(normalizeFilePath(p, a.agent.Environment.RootDir()))
			}

			a.updateInputHint()

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
	a.bridge = bridge.Setup(a.ctx, a.agent.Environment.RootDir(), a.mcpManager)

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
		a.enterPlanMode(true)
		return
	}

	a.exitPlanMode(true)
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
	case ModalDiagnostics:
		a.closeDiagnosticsView()
	}
}

func (a *App) allTools() []tool.Tool {
	a.mcpMu.Lock()
	defer a.mcpMu.Unlock()

	tools := append([]tool.Tool{}, a.agent.Tools...)

	if a.currentMode != ModePlan {
		tools = append(tools, a.mcpTools...)
	}

	// When bridge is connected it provides all LSP operations via the IDE's
	// language services. Skip local LSP.
	if !a.bridge.IsConnected() {
		tools = append(tools, a.lspTools...)
	}

	return tools
}

func (a *App) lspDiagnostics(ctx context.Context, path string) string {
	absPath := path
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(a.lspManager.WorkingDir(), path)
	}

	// When bridge is connected, notify it and use its diagnostics.
	if a.bridge.IsConnected() {
		return a.bridgeDiagnostics(ctx, absPath)
	}

	return a.localLSPDiagnostics(ctx, absPath, path)
}

func (a *App) bridgeDiagnostics(ctx context.Context, absPath string) string {
	a.bridge.NotifyFileUpdated(ctx, absPath)

	// Give the IDE time to re-analyze the file after notification
	time.Sleep(500 * time.Millisecond)

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	result, err := a.bridge.GetDiagnostics(ctx, absPath)
	if err != nil || result == "" || result == "[]" {
		return ""
	}

	return result
}

func (a *App) localLSPDiagnostics(ctx context.Context, absPath, path string) string {
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

func (a *App) showMissingLSPHint() {
	// When bridge is connected, LSP comes from the IDE — no local servers needed.
	if a.bridge.IsConnected() {
		return
	}

	missing := a.lspManager.MissingServers()
	if len(missing) == 0 {
		return
	}

	t := theme.Default

	for _, m := range missing {
		fmt.Fprintf(a.chatView, "  [%s]┃[-] [%s]No LSP server found for %s (install %s)[-]\n",
			t.BrBlack, t.BrBlack, m.ProjectName, strings.Join(m.Servers, " or "))
	}
	fmt.Fprint(a.chatView, "\n")
}

func (a *App) isToolHidden(name string) bool {
	for _, t := range a.allTools() {
		if t.Name == name {
			return t.Hidden
		}
	}

	return false
}
