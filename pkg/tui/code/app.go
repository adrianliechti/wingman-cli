package code

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/task"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	coder "github.com/adrianliechti/wingman-agent/pkg/code/agent"
	"github.com/adrianliechti/wingman-agent/pkg/tui"
	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
	"github.com/adrianliechti/wingman-agent/pkg/tui/clipboard"
	"github.com/adrianliechti/wingman-agent/pkg/tui/inline"
	"github.com/adrianliechti/wingman-agent/pkg/tui/markdown"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

var _ code.UI = (*App)(nil)

type App struct {
	ctx   context.Context
	agent *coder.Agent
	term  *inline.Terminal

	queue    chan func()
	quit     chan struct{}
	quitOnce sync.Once

	sessionMu    sync.Mutex
	sessionID    string
	sessionEpoch uint64

	phase      atomic.Int32
	phaseStart time.Time

	spinnerFrame int
	quitDeadline time.Time
	termFocused  bool

	// footerHint is a transient warning shown in the footer instead of the
	// key hints; it clears on the next input action or when it expires.
	footerHint string

	currentMode Mode
	showWelcome bool

	editor  *Editor
	popup   *Popup
	overlay Overlay

	// cmdTokenStart is the rune index of the slash token driving the command
	// popup; cmdPopupInline tracks which command set the popup was built for.
	cmdTokenStart  int
	cmdPopupInline bool

	selecting bool
	selAnchor selPos
	selHead   selPos
	selActive bool

	chat          []string
	chatScroll    int
	follow        bool
	lastChatRows  int
	lastMaxScroll int
	lastTopPad    int

	printed     int
	flow        cellFlow
	annotations []chatAnnotation

	// turnBase is the message count already covered by earlier separators;
	// turn work is derived from the history beyond it, so replaying the chat
	// (resume, rebuild) cannot skew the numbers.
	turnBase  int
	turnStart time.Time

	pendingEcho []pendingEchoItem

	elicitMu     sync.Mutex
	promptActive bool
	confirmAll   atomic.Bool
	askActive    bool
	askMessage   string
	askHeader    []string
	askResponse  chan string

	inputTokens     int64
	outputTokens    int64
	lastInputTokens int64

	pendingContent []agent.Content
	pendingFiles   []string

	turns       *code.TurnManager
	turnMu      sync.Mutex
	turnCommits map[string]string

	taskPumpStop chan struct{}

	renderPending atomic.Bool
	renderLast    atomic.Int64
	dirty         bool

	streamStateMu       sync.Mutex
	currentToolID       string
	currentToolName     string
	currentToolHint     string
	currentToolProgress string
	streamingText       string
	streamingReasoning  string
	reasoningID         string
	reasoningPart       int
}

type pendingEchoItem struct {
	ID   string
	Text string
}

// chatAnnotation is a chat cell that is not derived from the message history
// (recap, resume banner); rebuilds re-render it at its recorded position
// instead of silently dropping it.
type chatAnnotation struct {
	afterMessages int
	render        func(width int) []string
}

func New(ctx context.Context, coderAgent *coder.Agent, sessionID string) *App {
	saveExecutablePath()

	hasMessages := sessionID != "" && len(coderAgent.Messages(sessionID)) > 0

	a := &App{
		ctx:   ctx,
		agent: coderAgent,
		term:  inline.NewTerminal(),

		queue: make(chan func(), 64),
		quit:  make(chan struct{}),

		sessionID:    sessionID,
		sessionEpoch: 1,
		turnBase:     len(coderAgent.Messages(sessionID)),
		showWelcome:  !hasMessages && os.Getenv("WINGMAN_CALLER") != "vscode",

		editor:      NewEditor(),
		turnCommits: map[string]string{},
		follow:      true,
		termFocused: true,
	}

	a.turns = code.NewTurnManager(tool.WithProgressSink(ctx, a.onToolProgress), coderAgent, a.handleTurnEvent)

	return a
}

// onToolProgress receives live status text from a running tool call; text for
// anything but the currently displayed call is dropped.
func (a *App) onToolProgress(callID, text string) {
	a.streamStateMu.Lock()
	if callID != a.currentToolID {
		a.streamStateMu.Unlock()
		return
	}
	a.currentToolProgress = text
	a.streamStateMu.Unlock()
	a.requestRender()
}

// WithTerminal replaces the terminal, used by tests.
func (a *App) WithTerminal(t *inline.Terminal) {
	a.term = t
}

func (a *App) SetSessionID(id string) {
	a.sessionMu.Lock()
	a.sessionID = id
	a.sessionEpoch++
	a.sessionMu.Unlock()
}

// activateSession changes the session and resets all state that belongs to
// the previous turn. The epoch prevents already-queued UI callbacks from an
// older activation of the same session from rendering later.
func (a *App) activateSession(id string) {
	a.sessionMu.Lock()
	a.sessionID = id
	a.sessionEpoch++
	a.clearStreamingState()
	a.setPhase(PhaseIdle)
	a.sessionMu.Unlock()

	a.printed = 0
	a.flow = cellFlow{}
	a.annotations = nil
	a.resetTurnStats()

	a.startTaskPump()
}

// startTaskPump forwards background-agent completions of the current session
// into the turn queue. Only run on the UI loop. The previous pump stops;
// undelivered events stay buffered in the registry for a later pump.
func (a *App) startTaskPump() {
	if a.taskPumpStop != nil {
		close(a.taskPumpStop)
		a.taskPumpStop = nil
	}

	if a.agent == nil {
		return
	}

	sessionID := a.sessionID
	reg := a.agent.Tasks(sessionID)
	if reg == nil {
		return
	}

	stop := make(chan struct{})
	a.taskPumpStop = stop

	go func() {
		for {
			select {
			case <-stop:
				return
			case <-a.quit:
				return
			case ev := <-reg.Events():
				// Completions that piled up (parallel agents, or while the
				// pump was detached) deliver as one turn, not one turn each.
				batch := []task.Event{ev}
				for {
					select {
					case more := <-reg.Events():
						batch = append(batch, more)
						continue
					default:
					}
					break
				}
				a.deliverTaskResults(sessionID, batch)
			}
		}
	}()
}

func taskResultColor(status task.Status) ansi.Color {
	switch status {
	case task.StatusFailed:
		return theme.Default.Red
	case task.StatusStopped:
		return theme.Default.Yellow
	default:
		return theme.Default.Green
	}
}

// deliverTaskResults surfaces finished background agents: a status notice per
// agent for the user, and one hidden steer/follow-up turn so the model
// receives the results — injected mid-turn when one is active, as a new turn
// otherwise.
func (a *App) deliverTaskResults(sessionID string, batch []task.Event) {
	a.post(func() {
		if a.sessionID != sessionID {
			return
		}
		a.flushToolGap()
		for _, ev := range batch {
			a.appendChat(cellNotice(fmt.Sprintf("%s %s %s (%s, %s)", ev.Label(), ev.ID, ev.Verb(), ev.Description, ev.Elapsed.Round(time.Second)), taskResultColor(ev.Status), a.width()))
		}
		a.invalidate()
	})

	var blocks []string
	var labels []string
	for _, ev := range batch {
		blocks = append(blocks, ev.Notification())
		labels = append(labels, ev.Description)
	}

	first := batch[0]
	id := fmt.Sprintf("task-%s-%d", first.ID, first.Seq)
	a.rememberTurn(id, []agent.Content{{Text: "background agents: " + strings.Join(labels, ", ")}})

	_, err := a.turns.Submit(a.ctx, sessionID, code.TurnInput{
		ID:     id,
		Intent: code.TurnInputSteer,
		Content: []agent.Content{{
			Text:   strings.Join(blocks, "\n\n"),
			Hidden: true,
		}},
	})
	if err != nil {
		a.takeTurnCommit(id)
		a.post(func() {
			if a.sessionID != sessionID {
				return
			}
			a.appendChat(cellNotice(fmt.Sprintf("Could not deliver background agent results: %v (use task_output to retrieve them)", err), theme.Default.Red, a.width()))
		})
	}
}

func (a *App) withCurrentSession(id string, fn func()) {
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	if a.sessionID == id {
		fn()
	}
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

// post schedules fn on the UI loop from any goroutine.
func (a *App) post(fn func()) {
	select {
	case a.queue <- fn:
	case <-a.quit:
	}
}

func (a *App) invalidate() {
	a.dirty = true
}

func (a *App) stop() {
	a.quitOnce.Do(func() {
		close(a.quit)
	})
}

func (a *App) setFooterHint(hint string) {
	a.footerHint = hint
	a.invalidate()
}

func (a *App) runningTaskCount() int {
	if a.agent != nil {
		return a.agent.RunningTaskCount()
	}
	return 0
}

func backgroundQuitHint(running int, retry string) string {
	if running == 1 {
		return "1 background agent is still running — " + retry + " to exit and stop it"
	}
	return fmt.Sprintf("%d background agents are still running — %s to exit and stop them", running, retry)
}

func (a *App) ctrlCQuitHint() string {
	if running := a.runningTaskCount(); running > 0 {
		return backgroundQuitHint(running, "press ctrl+c again")
	}
	return "Press ctrl+c again to exit"
}

const quitConfirmWindow = 3 * time.Second

// gateQuit arms the quit confirmation on the first attempt and admits the
// next attempt within the confirmation window.
func (a *App) gateQuit(warning string) bool {
	now := time.Now()
	if now.Before(a.quitDeadline) {
		a.disarmQuitGate()
		return true
	}

	a.quitDeadline = now.Add(quitConfirmWindow)
	a.setFooterHint(warning)
	return false
}

// disarmQuitGate retires the confirmation window and its footer hint as one
// state, so the hint on screen is always truthful about what the next quit
// attempt does.
func (a *App) disarmQuitGate() {
	if a.quitDeadline.IsZero() && a.footerHint == "" {
		return
	}
	a.quitDeadline = time.Time{}
	a.setFooterHint("")
}

func (a *App) expireQuitGate(now time.Time) {
	if a.quitDeadline.IsZero() || now.Before(a.quitDeadline) {
		return
	}
	a.disarmQuitGate()
}

// confirmQuit returns whether quitting may proceed. With background agents
// still running it warns and defers to ctrl+c for confirmation — the one quit
// gesture that involves no typing, so it cannot disarm the gate on the way in.
func (a *App) confirmQuit() bool {
	running := a.runningTaskCount()
	if running == 0 {
		return true
	}
	return a.gateQuit(backgroundQuitHint(running, "press ctrl+c"))
}

func (a *App) saveSession() {
	_ = a.agent.Save(a.sessionID)
}

func (a *App) Run() error {
	if err := a.term.Start(); err != nil {
		return err
	}

	a.term.EnterAlt()
	a.term.SetTitle(a.terminalTitle())
	a.term.EnableMouse(true)

	a.agent.FetchModels(a.ctx)

	a.setPhase(PhasePreparing)

	go func() {
		a.agent.Workspace().WarmUp()

		if err := a.agent.Workspace().InitMCP(a.ctx); err != nil {
			a.post(func() {
				a.appendChat(cellError("MCP initialization failed", err.Error(), a.width()))
			})
		}

		a.post(func() {
			a.setPhase(PhaseIdle)
			if !a.agent.Workspace().HasRewind() {
				a.appendChat(cellNotice(
					"Limited mode: working dir is too large for full features. Diffs, checkpoints, and code intelligence are disabled.",
					theme.Default.Yellow, a.width(),
				))
			}
			a.invalidate()
		})
	}()

	if messages := a.agent.Messages(a.sessionID); len(messages) > 0 {
		usage := a.agent.Usage(a.sessionID)
		a.inputTokens = usage.InputTokens
		a.outputTokens = usage.OutputTokens
		a.lastInputTokens = usage.LastInputTokens
		a.syncMessages()
	}

	a.startTaskPump()

	a.invalidate()
	a.render()

	ticker := time.NewTicker(spinnerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-a.quit:
			a.shutdown()
			return nil

		case <-a.ctx.Done():
			a.shutdown()
			return nil

		case ev := <-a.term.Events():
			a.handleEvent(ev)

		case fn := <-a.queue:
			fn()

		case now := <-ticker.C:
			a.expireQuitGate(now)
			if a.getPhase() != PhaseIdle {
				a.spinnerFrame++
				a.invalidate()
			} else if o, ok := a.overlay.(*taskOverlay); ok && o.task.Status() == task.StatusRunning {
				a.invalidate()
			}
		}

		// Drain whatever queued up before painting once.
		for {
			select {
			case fn := <-a.queue:
				fn()
				continue
			default:
			}
			break
		}

		if a.dirty {
			a.dirty = false
			a.syncMessages()
			a.render()
		}
	}
}

func (a *App) shutdown() {
	a.saveSession()

	a.turns.SetHandler(nil)
	a.turns.Close()
	a.agent.Close()
	a.agent.Workspace().Close()

	a.term.Stop()

	if len(a.agent.Messages(a.sessionID)) > 0 {
		usage := a.agent.Usage(a.sessionID)
		fmt.Fprintf(os.Stderr, "\n")
		if usage.CachedTokens > 0 {
			fmt.Fprintf(os.Stderr, "  Tokens: ↑%s (%s cached) ↓%s\n", tui.FormatTokens(usage.InputTokens), tui.FormatTokens(usage.CachedTokens), tui.FormatTokens(usage.OutputTokens))
		} else {
			fmt.Fprintf(os.Stderr, "  Tokens: ↑%s ↓%s\n", tui.FormatTokens(usage.InputTokens), tui.FormatTokens(usage.OutputTokens))
		}
		fmt.Fprintf(os.Stderr, "  Resume: wingman --resume %s\n", a.sessionID)
		fmt.Fprintf(os.Stderr, "\n")
	}
}

func (a *App) width() int {
	w, _ := a.term.Size()
	if w <= 0 {
		return 80
	}
	return w
}

// appendChat adds finalized cells to the scrollable chat buffer.
func (a *App) appendChat(lines []string) {
	a.chat = append(a.chat, lines...)
	if len(lines) > 0 {
		a.showWelcome = false
	}
	a.invalidate()
}

// appendAnnotation adds a chat cell that is not derived from the message
// history and records it so rebuildChat restores it at the same position.
func (a *App) appendAnnotation(render func(width int) []string) {
	a.flushToolGap()
	a.annotations = append(a.annotations, chatAnnotation{afterMessages: a.printed, render: render})
	a.appendChat(render(a.width()))
}

// restoreChatLines renders the full message history with annotations
// interleaved at their recorded positions.
func (a *App) restoreChatLines(width int) []string {
	messages := a.agent.Messages(a.sessionID)

	var lines []string

	emit := func(ann chatAnnotation) {
		if a.flow.gap() {
			lines = append(lines, "")
		}
		lines = append(lines, ann.render(width)...)
	}

	for i, m := range messages {
		for _, ann := range a.annotations {
			if ann.afterMessages == i {
				emit(ann)
			}
		}
		lines = append(lines, a.formatMessageCells(m, width)...)
	}

	for _, ann := range a.annotations {
		if ann.afterMessages >= len(messages) {
			emit(ann)
		}
	}

	a.printed = len(messages)

	return lines
}

// rebuildChat re-renders the whole chat buffer from the message history, used
// on resize and when toggling verbose rendering.
func (a *App) rebuildChat() {
	a.chat = nil
	a.printed = 0
	a.flow = cellFlow{}
	a.clearSelection()

	if lines := a.restoreChatLines(a.width()); len(lines) > 0 {
		a.appendChat(lines)
	}

	a.invalidate()
}

type selPos struct {
	Line int
	Col  int
}

func (p selPos) before(q selPos) bool {
	return p.Line < q.Line || (p.Line == q.Line && p.Col < q.Col)
}

// handleMouse routes wheel to chat scrolling and left-button drags to
// text selection; the two coexist without a mode switch.
func (a *App) handleMouse(ev inline.MouseEvent) {
	switch ev.Kind {
	case inline.MouseWheel:
		a.scrollChat(ev.WheelDelta * 3)

	case inline.MousePress:
		a.clearSelection()
		row := ev.Y - 1
		line := a.chatScroll + row - a.lastTopPad
		if row >= 0 && row < a.lastChatRows && line >= 0 && !a.showWelcome {
			a.selecting = true
			a.selAnchor = selPos{Line: line, Col: ev.X - 1}
			a.selHead = a.selAnchor
		}
		a.invalidate()

	case inline.MouseDrag:
		if !a.selecting {
			return
		}
		row := ev.Y - 1
		if row < 0 {
			row = 0
		}
		if row >= a.lastChatRows {
			row = a.lastChatRows - 1
		}
		line := a.chatScroll + row - a.lastTopPad
		if line < 0 {
			line = 0
		}
		a.selHead = selPos{Line: line, Col: ev.X - 1}
		a.selActive = true
		a.invalidate()

	case inline.MouseRelease:
		if a.selecting {
			a.selecting = false
			if a.selActive {
				a.copySelection()
			}
		}
	}
}

func (a *App) clearSelection() {
	a.selecting = false
	a.selActive = false
}

func (a *App) orderedSelection() (selPos, selPos) {
	if a.selHead.before(a.selAnchor) {
		return a.selHead, a.selAnchor
	}
	return a.selAnchor, a.selHead
}

// removePendingEcho drops the queued-input preview for id.
func (a *App) removePendingEcho(id string) {
	for i, item := range a.pendingEcho {
		if item.ID == id {
			a.pendingEcho = append(a.pendingEcho[:i], a.pendingEcho[i+1:]...)
			return
		}
	}
}

// chatViewLines composes the scrollable chat content: committed cells, the
// live streaming tail, and previews of inputs still queued behind the active
// turn.
func (a *App) chatViewLines(width int) []string {
	view := a.chat

	stream := a.streamCells(width)

	if len(stream) > 0 || len(a.pendingEcho) > 0 {
		view = append(append([]string(nil), a.chat...), stream...)
		for _, item := range a.pendingEcho {
			text := markdown.Sanitize(strings.ReplaceAll(item.Text, "\n", " "))
			view = append(view, cellIndent+dim(ansi.Truncate("› "+text, width-10, "…")+" (queued)"))
		}
	}

	return view
}

// copySelection extracts the selected plain text and puts it on the
// clipboard, silently.
func (a *App) copySelection() {
	start, end := a.orderedSelection()
	view := a.chatViewLines(a.width())

	var parts []string
	for l := start.Line; l <= end.Line && l < len(view); l++ {
		from, to := 0, int(^uint(0)>>1)
		if l == start.Line {
			from = start.Col
		}
		if l == end.Line {
			to = end.Col + 1
		}
		parts = append(parts, strings.TrimRight(ansi.CutPlain(view[l], from, to), " "))
	}

	text := strings.Join(parts, "\n")
	if strings.TrimSpace(text) == "" {
		return
	}

	go func() {
		if err := clipboard.WriteText(text); err != nil {
			a.post(func() {
				a.appendChat(cellNotice(fmt.Sprintf("Clipboard copy failed: %v", err), theme.Default.Red, a.width()))
			})
		}
	}()
}

// scrollChat adjusts the chat viewport; render() clamps and re-engages
// follow mode when the bottom is reached.
func (a *App) scrollChat(delta int) {
	if delta < 0 && a.follow {
		a.chatScroll = a.lastMaxScroll
	}
	a.follow = false
	a.chatScroll += delta
	if a.chatScroll < 0 {
		a.chatScroll = 0
	}
	a.invalidate()
}

func (a *App) isStreaming() bool {
	return a.getPhase() != PhaseIdle
}

func (a *App) handleEvent(ev inline.Event) {
	switch ev := ev.(type) {
	case inline.FocusEvent:
		a.termFocused = ev.Focused

	case inline.ResizeEvent:
		a.term.Resized(ev.Width, ev.Height)
		a.rebuildChat()

	case inline.MouseEvent:
		if a.overlay != nil {
			if m, ok := a.overlay.(interface{ HandleMouse(inline.MouseEvent) }); ok {
				m.HandleMouse(ev)
				a.invalidate()
			}
			return
		}
		a.handleMouse(ev)

	case inline.PasteEvent:
		a.handlePaste(ev.Text)
		a.invalidate()

	case inline.KeyEvent:
		a.handleKey(ev)
		a.invalidate()
	}
}

func (a *App) terminalTitle() string {
	project := filepath.Base(filepath.Clean(a.agent.Workspace().RootPath))
	if project == "" || project == "." || project == string(filepath.Separator) {
		return "Wingman"
	}
	return "Wingman — " + project
}

func (a *App) bellIfUnfocused() {
	if !a.termFocused {
		a.term.Bell()
	}
}

func (a *App) handlePaste(text string) {
	if a.overlay != nil {
		return
	}

	a.disarmQuitGate()

	paths := detectFilePaths(text, a.agent.Workspace().RootPath)
	if len(paths) > 0 {
		for _, p := range paths {
			a.addFileToContext(normalizeFilePath(p, a.agent.Workspace().RootPath))
		}
		return
	}

	a.editor.Insert(text)
	a.syncCommandPopup()
}

func (a *App) handleKey(ev inline.KeyEvent) {
	if ev.Key != inline.KeyCtrl || ev.Rune != 'c' {
		a.disarmQuitGate()
	}

	if a.overlay != nil {
		if a.overlay.HandleKey(ev) {
			a.closeOverlay()
		}
		return
	}

	if a.popup != nil {
		if a.handlePopupKey(ev) {
			return
		}
	}

	switch ev.Key {
	case inline.KeyEsc:
		if a.isStreaming() {
			a.cancelStream()
			return
		}
		a.editor.SetText("")
		a.clearPendingContent()
		a.syncCommandPopup()
		return

	case inline.KeyCtrl:
		switch ev.Rune {
		case 'c':
			// Never trap the user: during startup, or on a second press
			// while a turn refuses to die, ctrl+c always exits.
			if a.getPhase() == PhasePreparing {
				a.stop()
				return
			}
			if a.isStreaming() {
				if a.gateQuit(a.ctrlCQuitHint()) {
					a.stop()
					return
				}
				a.cancelStream()
				return
			}
			if a.editor.Text() != "" || len(a.pendingContent) > 0 || len(a.pendingFiles) > 0 {
				a.disarmQuitGate()
				a.editor.SetText("")
				a.clearPendingContent()
				a.syncCommandPopup()
				return
			}
			if a.gateQuit(a.ctrlCQuitHint()) {
				a.stop()
			}
			return
		case 'o':
			a.showTranscript()
			return
		case 'y':
			a.copyLastResponse()
			return
		case 'l':
			a.clearChat()
			return
		case 'v':
			a.pasteFromClipboard()
			return
		}

	case inline.KeyTab:
		if !a.isStreaming() && a.popup == nil {
			a.toggleMode()
			return
		}

	case inline.KeyBacktab:
		if !a.isStreaming() {
			a.cycleModel()
			return
		}

	case inline.KeyEnter:
		if a.askActive {
			a.answerPrompt()
			return
		}
		a.submitInput()
		return

	case inline.KeyUp:
		if a.editor.HandleKey(ev) {
			return
		}
		a.editor.HistoryPrev()
		return

	case inline.KeyDown:
		if a.editor.HandleKey(ev) {
			return
		}
		a.editor.HistoryNext()
		return

	case inline.KeyPgUp:
		a.scrollChat(-max(1, a.lastChatRows-1))
		return

	case inline.KeyPgDn:
		a.scrollChat(max(1, a.lastChatRows-1))
		return
	}

	if ev.Key == inline.KeyRune && ev.Rune == '@' && !ev.Alt && a.popup == nil && !a.isStreaming() {
		a.showFilePicker()
		return
	}

	if a.editor.HandleKey(ev) {
		a.syncCommandPopup()
	}
}

// handlePopupKey routes keys to the active popup; returns true when consumed.
func (a *App) handlePopupKey(ev inline.KeyEvent) bool {
	popup := a.popup

	if popup.kind == popupCommands {
		switch ev.Key {
		case inline.KeyTab:
			if item, ok := popup.Current(); ok {
				a.completeCommand(item.ID)
			}
			return true
		case inline.KeyEnter:
			// A lone leading command completes and submits in one press;
			// elsewhere Enter completes the token, and submits once there is
			// nothing left to complete.
			if a.cmdTokenStart == 0 && a.editor.cursor == len(a.editor.value) {
				if item, ok := popup.Current(); ok && a.editor.Text() != item.ID {
					a.editor.SetText(item.ID)
				}
				a.closePopup()
				a.submitInput()
				return true
			}
			if item, ok := popup.Current(); ok && !a.completeCommand(item.ID) {
				a.closePopup()
				a.submitInput()
			}
			return true
		case inline.KeyEsc:
			a.closePopup()
			return true
		case inline.KeyUp, inline.KeyDown, inline.KeyPgUp, inline.KeyPgDn:
			consumed, _ := popup.HandleKey(ev)
			return consumed
		}
		return false
	}

	consumed, closed := popup.HandleKey(ev)
	if closed {
		a.closePopup()
	}
	return consumed
}

func (a *App) closePopup() {
	popup := a.popup
	a.popup = nil

	if popup != nil && !popup.accepted && popup.onCancel != nil {
		popup.onCancel()
	}
}

func (a *App) answerPrompt() {
	if a.askActive {
		text := strings.TrimSpace(a.editor.Text())
		if text == "" {
			return
		}
		a.editor.SetText("")
		a.flushToolGap()
		a.appendChat(cellPrompt("", a.askMessage, "", a.width()))
		a.appendChat(cellUser(text, a.width()))
		a.setPhase(PhaseThinking)
		select {
		case a.askResponse <- text:
		default:
		}
	}
}

func (a *App) cancelStream() {
	a.turns.CancelAll(a.sessionID)

	if a.askActive {
		a.editor.SetText("")

		select {
		case a.askResponse <- "":
		default:
		}
	}
}

func (a *App) clearPendingContent() {
	a.pendingContent = nil
	a.pendingFiles = nil
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

func (a *App) clearChat() {
	previousID := a.sessionID
	id, err := a.agent.NewSession(a.ctx)
	if err != nil {
		a.appendChat(cellNotice(fmt.Sprintf("Could not create session: %v", err), theme.Default.Red, a.width()))
		return
	}
	a.turns.CancelAll(previousID)
	a.activateSession(id)
	a.clearPendingContent()
	a.inputTokens = 0
	a.outputTokens = 0
	a.lastInputTokens = 0
	a.chat = nil
	a.chatScroll = 0
	a.follow = true
	a.clearSelection()
	a.invalidate()
}

func (a *App) resumeSession() {
	t := theme.Default

	sessions, err := a.agent.ListSessions(a.ctx)
	if err != nil || len(sessions) == 0 {
		a.appendChat(cellNotice("No sessions to resume", t.Yellow, a.width()))
		return
	}

	last := sessions[0]
	if err := a.agent.LoadSession(a.ctx, last.ID); err != nil {
		a.appendChat(cellNotice(fmt.Sprintf("Failed to load session: %v", err), t.Red, a.width()))
		return
	}

	a.turns.CancelAll(a.sessionID)
	a.activateSession(last.ID)
	a.clearPendingContent()

	usage := a.agent.Usage(a.sessionID)
	a.inputTokens = usage.InputTokens
	a.outputTokens = usage.OutputTokens
	a.lastInputTokens = usage.LastInputTokens

	a.showWelcome = false
	a.chat = nil
	a.chatScroll = 0
	a.follow = true
	a.clearSelection()
	a.syncMessages()

	banner := fmt.Sprintf("Resumed session from %s", last.UpdatedAt.Format("Jan 2 15:04"))
	a.appendAnnotation(func(width int) []string {
		return cellNotice(banner, t.Green, width)
	})

	if len(a.agent.Messages(a.sessionID)) > 0 {
		a.showRecap()
	}
}

// showRecap asynchronously summarizes the session and posts the result as a
// notice-style cell; the session may keep working meanwhile.
func (a *App) showRecap() {
	id := a.sessionID

	if len(a.agent.Messages(id)) == 0 {
		a.appendChat(cellNotice("Nothing to recap yet", theme.Default.Yellow, a.width()))
		return
	}

	a.appendChat(cellNotice("Generating recap…", theme.Default.BrBlack, a.width()))

	go func() {
		recap, err := a.agent.Recap(a.ctx, id)

		a.post(func() {
			if a.sessionID != id {
				return
			}

			switch {
			case err != nil:
				a.appendChat(cellNotice(fmt.Sprintf("Recap failed: %v", err), theme.Default.Yellow, a.width()))
			case recap == "":
				a.appendChat(cellNotice("Nothing to recap yet", theme.Default.Yellow, a.width()))
			default:
				a.appendAnnotation(func(width int) []string {
					return cellAssistant(recap, width, theme.Default.Cyan)
				})
			}
			a.invalidate()
		})
	}()
}

func (a *App) copyTextToClipboard(text string) {
	go func() {
		err := clipboard.WriteText(text)

		a.post(func() {
			message := "Copied to clipboard"
			color := theme.Default.BrBlack

			if err != nil {
				message = fmt.Sprintf("Clipboard copy failed: %v", err)
				color = theme.Default.Red
			}

			a.appendChat(cellNotice(message, color, a.width()))
		})
	}()
}

func (a *App) copyLastResponse() {
	messages := a.agent.Messages(a.sessionID)

	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == agent.RoleAssistant {
			for _, c := range messages[i].Content {
				if c.Text != "" {
					a.copyTextToClipboard(c.Text)

					return
				}
			}
		}
	}
}

func (a *App) pasteFromClipboard() {
	go func() {
		contents, err := clipboard.Read()

		if err != nil || len(contents) == 0 {
			return
		}

		a.post(func() {
			for _, c := range contents {
				if c.Image != nil {
					a.pendingContent = append(a.pendingContent, agent.Content{File: &agent.File{Data: *c.Image}})
				}

				if c.Text != "" {
					paths := detectFilePaths(c.Text, a.agent.Workspace().RootPath)
					if len(paths) > 0 {
						for _, p := range paths {
							a.addFileToContext(normalizeFilePath(p, a.agent.Workspace().RootPath))
						}

						continue
					}

					a.editor.Insert(strings.ReplaceAll(c.Text, "\r\n", "\n"))
					a.syncCommandPopup()
				}
			}

			a.invalidate()
		})
	}()
}

func (a *App) showError(title string, err error) {
	a.appendChat(cellError(title, err.Error(), a.width()))
}

func (a *App) isToolHidden(name string) bool {
	for _, t := range a.agent.Tools(a.sessionID) {
		if t.Name == name {
			return t.Hidden
		}
	}

	return false
}

func (a *App) toggleMode() {
	if a.currentMode == ModeAgent {
		a.enterPlanMode()
		return
	}

	a.exitPlanMode()
}

func (a *App) enterPlanMode() {
	if a.currentMode == ModePlan {
		return
	}

	_ = a.agent.SetMode(a.ctx, a.sessionID, "plan")
	a.currentMode = ModePlan
}

func (a *App) exitPlanMode() {
	if a.currentMode == ModeAgent {
		return
	}

	_ = a.agent.SetMode(a.ctx, a.sessionID, "agent")
	a.currentMode = ModeAgent
}
