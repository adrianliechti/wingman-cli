package shell

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
)

const (
	defaultExecWait    = 10
	defaultSessionWait = 5
	maxExecWait        = 300
	maxExecSessions    = 16
	maxUnreadBytes     = 1 << 20
)

// ExecExit reports the exit of a backgrounded exec_command session that no
// tool call was waiting on, so the host can deliver it as a notification
// instead of leaving the model to poll.
type ExecExit struct {
	SessionID   int
	Command     string
	Description string
	Output      string
	Notice      string
	Failed      bool
	Elapsed     time.Duration
}

type ExecManager struct {
	onExit func(ExecExit)

	mu       sync.Mutex
	closed   bool
	nextID   int
	sessions map[int]*execSession
}

func NewExecManager(onExit func(ExecExit)) *ExecManager {
	return &ExecManager{onExit: onExit, sessions: map[int]*execSession{}}
}

func (m *ExecManager) Close() {
	m.mu.Lock()
	m.closed = true
	sessions := make([]*execSession, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.sessions = map[int]*execSession{}
	m.mu.Unlock()

	for _, s := range sessions {
		s.cancel()
	}
}

func (m *ExecManager) add(s *execSession) (int, error) {
	m.mu.Lock()

	if m.closed {
		m.mu.Unlock()
		s.cancel()
		return 0, fmt.Errorf("agent session is closed")
	}

	running := 0
	for _, existing := range m.sessions {
		if !existing.exited() {
			running++
		}
	}
	if running >= maxExecSessions {
		m.mu.Unlock()
		s.cancel()
		return 0, fmt.Errorf("too many running sessions (max %d); kill sessions you no longer need via exec_session", maxExecSessions)
	}

	// Exited sessions linger so late polls can still read their output, but
	// only until the map grows past twice the running cap.
	if len(m.sessions) >= 2*maxExecSessions {
		var exited []int
		for id, existing := range m.sessions {
			if existing.exited() {
				exited = append(exited, id)
			}
		}
		slices.Sort(exited)
		for _, id := range exited {
			if len(m.sessions) < 2*maxExecSessions {
				break
			}
			delete(m.sessions, id)
		}
	}

	m.nextID++
	s.id = m.nextID
	m.sessions[s.id] = s
	m.mu.Unlock()

	return s.id, nil
}

func (m *ExecManager) get(id int) *execSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id]
}

func (m *ExecManager) remove(id int) {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
}

func (m *ExecManager) markBackgrounded(s *execSession) {
	m.mu.Lock()
	s.backgrounded = true
	m.mu.Unlock()
	if s.exited() {
		m.notifyExit(s)
	}
}

func (m *ExecManager) beginWait(s *execSession) {
	m.mu.Lock()
	s.waiters++
	m.mu.Unlock()
}

func (m *ExecManager) endWait(s *execSession) {
	m.mu.Lock()
	s.waiters--
	m.mu.Unlock()
	if s.exited() {
		m.notifyExit(s)
	}
}

// notifyExit delivers a backgrounded session's exit as a background event.
// Removal from the session map is the exactly-once delivery claim; a waiting
// tool call suppresses the notification and reports the exit inline instead
// (endWait re-runs this check for a waiter that woke on its timeout while the
// process exited).
func (m *ExecManager) notifyExit(s *execSession) {
	m.mu.Lock()
	if m.closed || m.onExit == nil || !s.backgrounded || s.waiters > 0 || m.sessions[s.id] != s {
		m.mu.Unlock()
		return
	}
	delete(m.sessions, s.id)
	m.mu.Unlock()

	m.onExit(ExecExit{
		SessionID:   s.id,
		Command:     s.command,
		Description: s.description,
		Output:      s.drain(),
		Notice:      s.exitNotice(),
		Failed:      s.exitErr != nil,
		Elapsed:     time.Since(s.started),
	})
}

type execSession struct {
	id          int
	command     string
	description string
	started     time.Time
	tty         bool
	cancel      context.CancelFunc
	interrupt   func() error
	stdin       io.WriteCloser

	done     chan struct{}
	exitErr  error
	exitedAt time.Time

	// backgrounded and waiters are lifecycle state guarded by the manager mutex:
	// exit notifications fire only for backgrounded sessions no tool call is
	// currently waiting on.
	backgrounded bool
	waiters      int

	mu      sync.Mutex
	unread  bytes.Buffer
	dropped int

	inputMu        sync.Mutex
	pendingInput   string
	inputOverflow  bool
	inputUncertain bool
}

func (s *execSession) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unread.Write(p)
	if over := s.unread.Len() - maxUnreadBytes; over > 0 {
		s.unread.Next(over)
		s.dropped += over
	}
	return len(p), nil
}

func (s *execSession) drain() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := sanitizeOutput(s.unread.String())
	s.unread.Reset()
	if s.dropped > 0 {
		out = fmt.Sprintf("[%d bytes of earlier output dropped]\n", s.dropped) + out
		s.dropped = 0
	}
	return out
}

// sanitizeOutput resolves captured terminal control flow into plain text:
// escape sequences are stripped and a carriage-return overwritten line keeps
// its final content. TUI programs on a PTY otherwise fill results with cursor
// noise that neither the model nor any UI can read.
func sanitizeOutput(s string) string {
	if !strings.ContainsAny(s, "\x1b\r") {
		return s
	}

	s = ansi.Strip(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")

	if !strings.ContainsRune(s, '\r') {
		return s
	}

	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if !strings.ContainsRune(line, '\r') {
			continue
		}
		segments := strings.Split(line, "\r")
		keep := segments[len(segments)-1]
		if keep == "" {
			keep = segments[len(segments)-2]
		}
		lines[i] = keep
	}
	return strings.Join(lines, "\n")
}

func (s *execSession) exited() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

func (s *execSession) exitNotice() string {
	if s.exitErr != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](s.exitErr); ok {
			if code := exitErr.ExitCode(); code >= 0 {
				return fmt.Sprintf("Command exited with code %d", code)
			}
			return fmt.Sprintf("Command terminated (%s)", exitErr.ProcessState.String())
		}
		return fmt.Sprintf("Command failed to run: %v", s.exitErr)
	}
	return "Command completed"
}

func ExecTools(manager *ExecManager, workDir string, elicit *tool.Elicitation, appr *Approvals, opts *Options) []tool.Tool {
	if appr == nil {
		appr = NewApprovals()
	}

	commandLines := []string{
		fmt.Sprintf("Start a command for long-running or interactive work. Waits up to `wait` seconds (default %d) for it to finish; if still running, the command keeps running in the background and a session_id is returned.", defaultExecWait),
		"- Use for dev servers, watch tasks, log tails, and interactive programs (REPLs, CLIs that prompt for input). Use `shell` for commands expected to finish promptly.",
		"- Set `tty` for programs that need a terminal (REPLs, prompts, programs that buffer output when piped). Unix only — ignored on Windows. Written input is echoed back in the output.",
		"- Runs in the same host shell as `shell` and starts in the workspace directory (override with `workdir`). stdout and stderr are merged. Output between reads is buffered (oldest dropped past 1MB); poll with `exec_session` to collect it.",
		"- The process is NOT killed when the wait elapses. Kill sessions you no longer need via `exec_session`.",
	}

	if manager.onExit != nil {
		commandLines = append(commandLines,
			"- When a backgrounded command exits on its own, its remaining output arrives automatically as a background notification — keep working instead of polling `exec_session` just to watch for the exit.",
		)
	}

	commandLines = append(commandLines, safetyGuardLine(elicit))
	commandDescription := strings.Join(commandLines, "\n")

	sessionDescription := strings.Join([]string{
		"Interact with a session started by `exec_command`: poll new output (no input), write to its stdin (`input`), close stdin (`eof`), or terminate it (`kill`).",
		"- Waits up to `wait` seconds for output (or exit) before returning.",
		"- `input` supports C-style escapes (\\n Enter, \\e Esc, \\t, \\uHHHH; \\\\ for a literal backslash); anything else is sent verbatim with nothing appended. Include \\n to submit a line to an interactive program, else it is typed but not entered (e.g. save in vi with \"\\e:w file\\n\"). Destructive or privilege-escalating input lines require the same user confirmation as shell commands.",
		"- \\u0003 (Ctrl-C) interrupts the process: on tty sessions via the terminal, otherwise via SIGINT to the process group (Unix only).",
		"- On tty sessions, `eof` sends Ctrl-D instead of closing stdin.",
		"- Sessions end when the process exits, is killed, or the agent session closes.",
	}, "\n")

	return []tool.Tool{
		{
			Name:        "exec_command",
			Description: commandDescription,
			Effect:      ClassifyEffect,

			Parameters: map[string]any{
				"type": "object",

				"properties": map[string]any{
					"command":     map[string]any{"type": "string", "description": "Command to run."},
					"description": map[string]any{"type": "string", "description": "Short label (e.g. \"Start dev server\")."},
					"workdir":     map[string]any{"type": "string", "description": "Directory to run the command in (absolute, or relative to the workspace). Defaults to the workspace root."},
					"tty":         map[string]any{"type": "boolean", "description": "Run in a pseudo-terminal (Unix only)."},
					"wait":        map[string]any{"type": "integer", "description": fmt.Sprintf("Seconds to wait before backgrounding (default %d, max %d; 0 backgrounds immediately).", defaultExecWait, maxExecWait)},
				},

				"required":             []string{"command"},
				"additionalProperties": false,
			},

			Execute: func(ctx context.Context, args map[string]any) (tool.Result, error) {
				return executeExecCommand(ctx, manager, workDir, elicit, appr, opts, args)
			},
		},
		{
			Name:        "exec_session",
			Description: sessionDescription,
			Effect:      classifyExecSession,

			Parameters: map[string]any{
				"type": "object",

				"properties": map[string]any{
					"session_id": map[string]any{"type": "integer", "description": "Session id returned by exec_command."},
					"input":      map[string]any{"type": "string", "description": "Text to write to the process stdin."},
					"eof":        map[string]any{"type": "boolean", "description": "Close stdin after writing input."},
					"kill":       map[string]any{"type": "boolean", "description": "Terminate the process."},
					"wait":       map[string]any{"type": "integer", "description": fmt.Sprintf("Seconds to wait for output before returning (default %d, max %d; 0 returns immediately).", defaultSessionWait, maxExecWait)},
				},

				"required":             []string{"session_id"},
				"additionalProperties": false,
			},

			Execute: func(ctx context.Context, args map[string]any) (tool.Result, error) {
				return executeExecSession(ctx, manager, elicit, appr, args)
			},
		},
	}
}

func executeExecCommand(ctx context.Context, m *ExecManager, workDir string, elicit *tool.Elicitation, appr *Approvals, opts *Options, args map[string]any) (tool.Result, error) {
	command, ok := args["command"].(string)

	if !ok || command == "" {
		return tool.Result{}, fmt.Errorf("command is required")
	}

	wait := defaultExecWait
	if value, present, err := tool.NonNegIntArg(args, "wait"); present {
		if err != nil {
			return tool.Result{}, err
		}
		wait = value
	}
	if wait > maxExecWait {
		wait = maxExecWait
	}

	dir, err := resolveWorkdir(workDir, args)
	if err != nil {
		return tool.Result{}, err
	}

	if err := confirmDangerous(ctx, elicit, appr, args, approvalWorkdir(workDir, dir)); err != nil {
		return tool.Result{}, err
	}

	tty, _ := args["tty"].(bool)
	if runtime.GOOS == "windows" {
		tty = false
	}

	sctx, cancel := context.WithCancel(context.Background())

	cmd := buildToolCommand(sctx, command, dir, opts)
	cmd.Env = setEnvironment(cmd.Env, "NO_COLOR", "1")
	cmd.Env = setEnvironment(cmd.Env, "PAGER", "cat")
	cmd.Env = setEnvironment(cmd.Env, "GIT_PAGER", "cat")

	description, _ := args["description"].(string)

	s := &execSession{
		command:     command,
		description: strings.TrimSpace(description),
		started:     time.Now(),
		tty:         tty,
		cancel:      cancel,
		interrupt:   func() error { return interruptProcessGroup(cmd) },
		done:        make(chan struct{}),
	}

	if tty {
		master, err := startTTY(cmd)
		if err != nil {
			cancel()
			return tool.Result{}, fmt.Errorf("failed to start command: %w", err)
		}
		s.stdin = master

		copyDone := make(chan struct{})
		go func() {
			io.Copy(s, master)
			close(copyDone)
		}()
		go func() {
			err := cmd.Wait()
			exitedAt := time.Now()
			select {
			case <-copyDone:
			case <-time.After(2 * time.Second):
			}
			master.Close()
			s.exitErr = err
			s.exitedAt = exitedAt
			close(s.done)
			m.notifyExit(s)
		}()
	} else {
		stdin, err := cmd.StdinPipe()
		if err != nil {
			cancel()
			return tool.Result{}, err
		}
		s.stdin = stdin
		cmd.Stdout = s
		cmd.Stderr = s

		if err := cmd.Start(); err != nil {
			cancel()
			return tool.Result{}, fmt.Errorf("failed to start command: %w", err)
		}

		go func() {
			s.exitErr = cmd.Wait()
			s.exitedAt = time.Now()
			close(s.done)
			m.notifyExit(s)
		}()
	}

	id, err := m.add(s)
	if err != nil {
		return tool.Result{}, err
	}

	timer := time.NewTimer(time.Duration(wait) * time.Second)
	defer timer.Stop()

	select {
	case <-s.done:
		m.remove(id)
		return completedSessionResult(s), nil
	case <-timer.C:
	case <-ctx.Done():
	}

	m.markBackgrounded(s)

	notice := fmt.Sprintf("Still running with session_id %d — use exec_session to poll output, send input, or kill it", id)
	return tool.Text(sessionResult(s.drain(), notice)), nil
}

func executeExecSession(ctx context.Context, m *ExecManager, elicit *tool.Elicitation, appr *Approvals, args map[string]any) (tool.Result, error) {
	id, ok := tool.IntArg(args, "session_id")
	if !ok {
		return tool.Result{}, fmt.Errorf("session_id is required")
	}

	s := m.get(id)
	if s == nil {
		return tool.Result{}, fmt.Errorf("no session with id %d (it may have exited and been cleaned up)", id)
	}

	m.beginWait(s)
	defer m.endWait(s)

	wait := defaultSessionWait
	if value, present, err := tool.NonNegIntArg(args, "wait"); present {
		if err != nil {
			return tool.Result{}, err
		}
		wait = value
	}
	if wait > maxExecWait {
		wait = maxExecWait
	}

	if kill, _ := args["kill"].(bool); kill {
		alreadyExited := s.exited()
		s.cancel()
		select {
		case <-s.done:
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
		}

		notice := fmt.Sprintf("Session %d killed", id)
		if alreadyExited {
			notice = s.exitNotice()
		} else if !s.exited() {
			notice = fmt.Sprintf("Session %d kill signalled; the process has not exited yet", id)
		}

		m.remove(id)
		return tool.Text(sessionResult(s.drain(), notice)), nil
	}

	input, _ := args["input"].(string)
	input = decodeInput(input)

	if input == "\u0003" {
		s.inputMu.Lock()
		s.pendingInput = ""
		s.inputOverflow = false
		s.inputUncertain = false
		s.inputMu.Unlock()
		if !s.tty {
			if err := s.interrupt(); err != nil {
				return tool.Result{}, fmt.Errorf("cannot interrupt on this platform; use kill instead: %w", err)
			}
		} else if _, err := io.WriteString(s.stdin, input); err != nil {
			return tool.Result{}, fmt.Errorf("failed to write interrupt to stdin: %w", err)
		}
	} else if input != "" {
		if result, err := writeSessionInput(ctx, m, id, s, elicit, appr, input); err != nil || result != nil {
			if result != nil {
				return *result, err
			}
			return tool.Result{}, err
		}
	}

	if eof, _ := args["eof"].(bool); eof {
		if err := confirmPendingSessionInput(ctx, s, elicit, appr); err != nil {
			return tool.Result{}, err
		}
		if s.tty {
			if _, err := io.WriteString(s.stdin, "\x04"); err != nil && !s.exited() {
				return tool.Result{}, fmt.Errorf("failed to send EOF to terminal: %w", err)
			}
			// Readline treats Ctrl-D on a non-empty buffer as delete-char, not
			// EOF. Unless the process exits, its resulting line is unknowable.
			s.inputMu.Lock()
			s.inputUncertain = true
			s.inputMu.Unlock()
		} else {
			s.stdin.Close()
		}
	}

	timer := time.NewTimer(time.Duration(wait) * time.Second)
	defer timer.Stop()

	select {
	case <-s.done:
		m.remove(id)
		return completedSessionResult(s), nil
	case <-timer.C:
	case <-ctx.Done():
	}

	output := s.drain()
	if output == "" {
		return tool.Text(fmt.Sprintf("(no new output; session %d still running)", id)), nil
	}
	return tool.Text(sessionResult(output, fmt.Sprintf("Session %d still running", id))), nil
}

func writeSessionInput(ctx context.Context, m *ExecManager, id int, s *execSession, elicit *tool.Elicitation, appr *Approvals, input string) (*tool.Result, error) {
	// Serialize input writes and retain the current logical line. Without this,
	// an agent can submit "r" and "m -rf ...\n" in separate calls while each
	// individual call looks harmless to the classifier.
	s.inputMu.Lock()
	defer s.inputMu.Unlock()

	combined := applyInputEditing(s.pendingInput, input)
	submitted, remainder := splitSubmittedInput(combined)
	immediateControl := hasImmediateTerminalControl(input)
	dangerous := immediateControl || s.inputUncertain
	approvalText := strings.TrimRight(submitted, "\r\n")
	if dangerous && approvalText == "" {
		approvalText = s.pendingInput + input
	}
	if immediateControl {
		approvalText += "  [input contains an active terminal control]"
	}
	if s.inputUncertain {
		approvalText += "  [terminal input state is unknown]"
	}
	if submitted != "" {
		dangerous = dangerous || s.inputOverflow || IsDangerousCommand(submitted)
		if s.inputOverflow {
			approvalText += "  [input line exceeded classifier limit]"
		}
	}
	approvalCache := appr
	if immediateControl || s.inputUncertain || s.inputOverflow {
		// Control keys, unknown readline state, and truncated input are
		// state-dependent; identical text is not an identical capability.
		approvalCache = NewApprovals()
	}
	if err := confirmIfDangerous(ctx, elicit, approvalCache, approvalText, dangerous); err != nil {
		return nil, err
	}

	if _, err := io.WriteString(s.stdin, input); err != nil {
		if s.exited() {
			m.remove(id)
			result := completedSessionResult(s)
			result.Content += " (input was not delivered)"
			return &result, nil
		}
		return nil, fmt.Errorf("failed to write to stdin: %w", err)
	}

	s.inputOverflow = len(remainder) > maxClassifiableBytes
	if s.inputOverflow {
		remainder = remainder[:maxClassifiableBytes]
	}
	s.pendingInput = remainder
	s.inputUncertain = terminalInputUncertainAfter(s.inputUncertain, input)
	return nil, nil
}

func confirmPendingSessionInput(ctx context.Context, s *execSession, elicit *tool.Elicitation, appr *Approvals) error {
	s.inputMu.Lock()
	defer s.inputMu.Unlock()
	if s.pendingInput == "" && !s.inputOverflow && !s.inputUncertain {
		return nil
	}
	text := s.pendingInput
	if s.inputOverflow {
		text += "  [input line exceeded classifier limit]"
	}
	if s.inputUncertain {
		text += "  [terminal input state is unknown]"
	}
	approvalCache := appr
	if s.inputOverflow || s.inputUncertain {
		approvalCache = NewApprovals()
	}
	return confirmIfDangerous(ctx, elicit, approvalCache, text, s.inputOverflow || s.inputUncertain || IsDangerousCommand(s.pendingInput))
}

func hasImmediateTerminalControl(input string) bool {
	for _, r := range input {
		if isUnmodelledTerminalControl(r) {
			return true
		}
	}
	return false
}

func isUnmodelledTerminalControl(r rune) bool {
	switch r {
	case '\n', '\r', '\b', '\x7f', '\x15':
		return false
	}
	return r < ' '
}

// A newline after an unmodelled key submits the unknown readline buffer and
// restores a known empty line. A later control key makes it uncertain again.
func terminalInputUncertainAfter(uncertain bool, input string) bool {
	for _, r := range input {
		switch {
		case r == '\n' || r == '\r':
			uncertain = false
		case isUnmodelledTerminalControl(r):
			uncertain = true
		}
	}
	return uncertain
}

func applyInputEditing(pending, input string) string {
	line := []rune(pending)
	for _, r := range input {
		switch r {
		case '\b', '\x7f':
			if len(line) > 0 && line[len(line)-1] != '\n' && line[len(line)-1] != '\r' {
				line = line[:len(line)-1]
			}
		case '\x15': // Ctrl-U: erase back to the current line boundary.
			for len(line) > 0 && line[len(line)-1] != '\n' && line[len(line)-1] != '\r' {
				line = line[:len(line)-1]
			}
		default:
			line = append(line, r)
		}
	}
	return string(line)
}

func splitSubmittedInput(input string) (submitted, remainder string) {
	last := strings.LastIndexAny(input, "\r\n")
	if last < 0 {
		return "", input
	}
	return input[:last+1], input[last+1:]
}

func completedSessionResult(s *execSession) tool.Result {
	duration := s.exitedAt.Sub(s.started)
	if s.exitedAt.IsZero() {
		duration = time.Since(s.started)
	}
	result := tool.Result{
		Content: sessionResult(s.drain(), s.exitNotice()),
		IsError: s.exitErr != nil,
		Metadata: map[string]any{
			"duration_ms": duration.Milliseconds(),
		},
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](s.exitErr); ok {
		result.Metadata["exit_code"] = exitErr.ExitCode()
	} else if s.exitErr == nil {
		result.Metadata["exit_code"] = 0
	}
	return result
}

func classifyExecSession(args map[string]any) tool.Effect {
	if args == nil {
		return tool.EffectDynamic
	}
	if input, _ := args["input"].(string); input != "" && IsDangerousCommand(input) {
		return tool.EffectDangerous
	}
	if kill, _ := args["kill"].(bool); kill {
		return tool.EffectMutates
	}
	if eof, _ := args["eof"].(bool); eof {
		return tool.EffectMutates
	}
	if input, _ := args["input"].(string); input != "" {
		return tool.EffectMutates
	}
	return tool.EffectReadOnly
}

func sessionResult(output, notice string) string {
	if output == "" {
		return notice
	}
	return output + "\n\n" + notice
}

// decodeInput turns C-style escape sequences in interactive input into their
// real bytes, so models that emit escape TEXT like \u001b or \n instead of the
// actual control bytes still drive interactive programs. strconv.UnquoteChar
// handles the standard escapes; \e (Esc) is the only common alias it lacks.
// Unrecognized escapes keep their backslash so regexes and paths survive.
func decodeInput(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))

	for i := 0; i < len(s); {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			i++
			continue
		}

		if i+1 < len(s) && s[i+1] == 'e' {
			b.WriteByte(0x1b)
			i += 2
			continue
		}

		r, multibyte, tail, err := strconv.UnquoteChar(s[i:], 0)
		if err != nil {
			b.WriteByte(s[i])
			i++
			continue
		}

		if multibyte {
			b.WriteRune(r)
		} else {
			b.WriteByte(byte(r))
		}
		i = len(s) - len(tail)
	}

	return b.String()
}
