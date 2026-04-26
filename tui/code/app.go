package code

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
	"github.com/adrianliechti/wingman-agent/pkg/agent/hook/truncation"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/adrianliechti/wingman-agent/pkg/lsp"
	"github.com/adrianliechti/wingman-agent/pkg/rewind"
	"github.com/adrianliechti/wingman-agent/pkg/session"
	"github.com/adrianliechti/wingman-agent/pkg/tui"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

type App struct {
	// Core dependencies
	ctx   context.Context
	app   *tview.Application
	agent *code.Agent

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

	// Current tool progress
	currentToolName string
	currentToolHint string

	// LSP diagnostics tracker
	lspTracker *lsp.DiagnosticTracker

	// Rewind state
	rewind      *rewind.Manager
	rewindReady chan struct{}

	// Mouse capture state (toggle to allow native terminal text selection)
	mouseEnabled bool
}

func New(ctx context.Context, agent *code.Agent, sessionID string) *App {
	saveExecutablePath()

	if sessionID == "" {
		sessionID = newSessionID()
	}

	hasMessages := len(agent.Messages) > 0

	a := &App{
		ctx:   ctx,
		app:   tview.NewApplication(),
		agent: agent,

		sessionID:   sessionID,
		showWelcome: !hasMessages && os.Getenv("WINGMAN_CALLER") != "vscode",
		phase:       PhasePreparing,

		lspTracker:  lsp.NewDiagnosticTracker(),
		rewindReady: make(chan struct{}),

		mouseEnabled: true,
	}

	agent.Config.Instructions = a.currentInstructions

	agent.Config.Hooks.PostToolUse = append(agent.Config.Hooks.PostToolUse,
		truncation.New(truncation.DefaultMaxBytes, agent.ScratchPath),
	)

	return a
}

func newSessionID() string {
	return uuid.New().String()
}

func saveExecutablePath() {
	path := os.Getenv("WINGMAN_PATH")

	if path == "" {
		exe, err := os.Executable()
		if err != nil {
			return
		}

		path = exe
	}

	if path == "" {
		return
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	dir := filepath.Join(home, ".wingman")
	os.MkdirAll(dir, 0755)

	os.WriteFile(filepath.Join(dir, "path"), []byte(path), 0644)
}

func (a *App) saveSession() {
	_ = session.Save(filepath.Join(filepath.Dir(a.agent.MemoryPath), "sessions"), a.sessionID, agent.State{
		Messages: a.agent.Messages,
		Usage:    a.agent.Usage,
	})
}

func (a *App) stop() {
	// Shut down bridge, MCP, and LSP servers
	a.agent.Close()

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
	if len(a.agent.Messages) > 0 {
		usage := a.agent.Usage
		fmt.Fprintf(os.Stderr, "\n")
		fmt.Fprintf(os.Stderr, "  Tokens: \u2191%s \u2193%s\n", tui.FormatTokens(usage.InputTokens), tui.FormatTokens(usage.OutputTokens))
		fmt.Fprintf(os.Stderr, "  Resume: wingman --resume %s\n", a.sessionID)
		fmt.Fprintf(os.Stderr, "\n")
	}
}

func (a *App) Run() error {
	a.setupUI()

	// Auto-select model if not configured
	a.autoSelectModel()

	go func() {
		if err := a.agent.InitMCP(a.ctx); err != nil {
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

		workDir := a.agent.RootPath
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
			if messages := a.agent.Messages; len(messages) > 0 {
				a.switchToChat()
				a.renderChat(messages, "", "", "")

				usage := a.agent.Usage
				a.inputTokens = usage.InputTokens
				a.outputTokens = usage.OutputTokens
				a.updateStatusBar()
			}
		})
	}()

	root := &pasteInterceptRoot{
		Primitive: a.pages,

		intercept: func(text string) bool {
			paths := detectFilePaths(text, a.agent.RootPath)

			if len(paths) == 0 {
				return false
			}

			for _, p := range paths {
				a.addFileToContext(normalizeFilePath(p, a.agent.RootPath))
			}

			a.updateInputHint()

			return true
		},
	}

	return a.app.SetRoot(root, true).EnableMouse(a.mouseEnabled).EnablePaste(true).Run()
}

func (a *App) toggleMode() {
	if a.currentMode == ModeAgent {
		a.enterPlanMode()
		return
	}

	a.exitPlanMode()
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

func (a *App) lspDiagnostics(ctx context.Context, path string) string {
	absPath := path
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(a.agent.LSP.WorkingDir(), path)
	}

	// When bridge is connected, notify it and use its diagnostics.
	if a.agent.Bridge != nil && a.agent.Bridge.IsConnected() {
		return a.bridgeDiagnostics(ctx, absPath)
	}

	return a.localLSPDiagnostics(ctx, absPath, path)
}

func (a *App) bridgeDiagnostics(ctx context.Context, absPath string) string {
	a.agent.Bridge.NotifyFileUpdated(ctx, absPath)

	// Give the IDE time to re-analyze the file after notification
	time.Sleep(500 * time.Millisecond)

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	result, err := a.agent.Bridge.GetDiagnostics(ctx, absPath)
	if err != nil || result == "" || result == "[]" {
		return ""
	}

	return result
}

func (a *App) localLSPDiagnostics(ctx context.Context, absPath, path string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	uri := lsp.FileURI(absPath)

	session, err := a.agent.LSP.GetSession(ctx, absPath)
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

	return lsp.FormatNewDiagnostics(newDiags, path, a.agent.LSP.WorkingDir())
}

func (a *App) showMissingLSPHint() {
	// When bridge is connected, LSP comes from the IDE — no local servers needed.
	if a.agent.Bridge != nil && a.agent.Bridge.IsConnected() {
		return
	}

	missing := a.agent.LSP.MissingServers()
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
	for _, t := range a.agent.Config.Tools() {
		if t.Name == name {
			return t.Hidden
		}
	}

	return false
}
