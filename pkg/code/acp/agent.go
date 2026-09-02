package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/adrianliechti/wingman-agent/internal/process"
	"github.com/adrianliechti/wingman-agent/pkg/acp"
	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/adrianliechti/wingman-agent/pkg/model"
)

type Agent struct {
	workspace *code.Workspace
	def       code.AgentDef

	cmd       *exec.Cmd
	stdin     io.WriteCloser
	conn      *acpsdk.ClientSideConnection
	closeOnce sync.Once

	serverDone <-chan struct{}
	serverW    io.Closer
	cleanup    func() error
	steer      func(context.Context, acpsdk.SessionId, []acpsdk.ContentBlock, string) error

	caps acpsdk.AgentCapabilities

	uiMu sync.RWMutex
	ui   code.UI

	configMu      sync.RWMutex
	models        []model.Model
	modelID       string
	modelDefault  string
	effortID      string
	effortDefault string
	effortOpts    []string

	mu       sync.Mutex
	sessions map[string]*sessionState
	pending  map[string][]acpsdk.SessionUpdate
}

var (
	_ code.Agent                   = (*Agent)(nil)
	_ code.HistorySnapshotProvider = (*Agent)(nil)
	_ code.HistoryVersionProvider  = (*Agent)(nil)
)

type sessionState struct {
	id acpsdk.SessionId

	mu       sync.Mutex
	messages []agent.Message
	usage    agent.Usage
	inflight *turn
	loaded   bool

	toolCallsMu sync.Mutex
	toolCalls   map[string]toolCall

	modes  []code.Mode
	modeID string

	models      []model.Model
	modelID     string
	modelOptID  string
	effortID    string
	effortOptID string
	effortOpts  []string

	commands  []code.Command
	title     string
	updatedAt time.Time
}

type toolCall struct {
	name      string
	kind      string
	args      string
	locations []agent.ToolLocation
	diff      string
}

type turn struct {
	ctx    context.Context
	events chan event
	done   chan struct{}
	cancel context.CancelFunc

	mu                sync.Mutex
	emitted           []agent.Message
	lastContentKey    string
	ignoreUserUpdates bool
}

// send drops the message once the turn is finished.
func (t *turn) send(msg agent.Message) {
	select {
	case t.events <- event{msg: msg}:
	case <-t.done:
	}
}

func (t *turn) messages() []agent.Message {
	t.mu.Lock()
	defer t.mu.Unlock()
	return agent.CloneMessages(t.emitted)
}

// cancelOutstandingToolCalls renders tool calls that never reached a terminal status.
func (s *sessionState) cancelOutstandingToolCalls(t *turn) []agent.Message {
	s.toolCallsMu.Lock()
	pending := s.toolCalls
	s.toolCalls = map[string]toolCall{}
	s.toolCallsMu.Unlock()

	ids := make([]string, 0, len(pending))
	for id := range pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	messages := make([]agent.Message, 0, len(ids))
	for _, id := range ids {
		call := pending[id]
		msg := agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{ToolResult: &agent.ToolResult{
			ID:        id,
			Name:      call.name,
			Kind:      call.kind,
			Args:      call.args,
			Locations: call.locations,
			Presentation: agent.NewToolPresentation(
				call.name, call.kind, call.args, call.locations,
			),
			Content: "tool call cancelled",
			IsError: true,
		}}}}
		t.mu.Lock()
		t.emitted = append(t.emitted, msg)
		t.lastContentKey = ""
		t.mu.Unlock()
		messages = append(messages, msg)
	}
	return messages
}

func (s *sessionState) finalizeTurn(t *turn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	emitted := t.messages()
	if s.inflight == t {
		s.inflight = nil
	}
	if len(emitted) > 0 {
		s.messages = append(s.messages, emitted...)
	}
}

type event struct {
	msg             agent.Message
	err             error
	done            bool
	remoteCancelled bool
}

const (
	modelConfigID  = "model"
	effortConfigID = "effort"
	initTimeout    = 30 * time.Second
)

func New(ctx context.Context, ws *code.Workspace, def code.AgentDef) (*Agent, error) {
	if def.Command == "" {
		return nil, fmt.Errorf("agent %q: empty command", def.Name)
	}

	cwd := ws.RootPath
	cmd := exec.Command(def.Command, def.Args...)
	process.Hide(cmd)
	cmd.Dir = cwd
	cmd.Env = os.Environ()
	for k, v := range def.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stderr = io.Discard

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("agent %q: stdin pipe: %w", def.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("agent %q: stdout pipe: %w", def.Name, err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("agent %q: start: %w", def.Name, err)
	}

	a := &Agent{
		workspace: ws,
		def:       def,
		cmd:       cmd,
		stdin:     stdin,
		sessions:  map[string]*sessionState{},
		pending:   map[string][]acpsdk.SessionUpdate{},
	}
	a.conn = acpsdk.NewClientSideConnection(a, stdin, stdout)

	a.conn.SetLogger(slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	})))

	resp, err := a.initialize(ctx)
	if err != nil {
		a.shutdown()
		return nil, fmt.Errorf("agent %q: initialize: %w", def.Name, err)
	}
	a.caps = resp.AgentCapabilities
	return a, nil
}

func NewInProcess(
	ctx context.Context,
	ws *code.Workspace,
	name string,
	serverAgent acpsdk.Agent,
	setupServer func(*acpsdk.AgentSideConnection),
	cleanup func() error,
) (*Agent, error) {
	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()

	a := &Agent{
		workspace: ws,
		def:       code.AgentDef{Name: name},
		stdin:     clientW,
		sessions:  map[string]*sessionState{},
		pending:   map[string][]acpsdk.SessionUpdate{},
		cleanup:   cleanup,
	}
	if s, ok := serverAgent.(interface {
		Steer(context.Context, acpsdk.SessionId, []acpsdk.ContentBlock, string) error
	}); ok {
		a.steer = s.Steer
	}

	srvConn := acpsdk.NewAgentSideConnection(serverAgent, serverW, serverR)
	if setupServer != nil {
		setupServer(srvConn)
	}
	a.serverDone = srvConn.Done()
	a.serverW = serverW

	a.conn = acpsdk.NewClientSideConnection(a, clientW, clientR)
	a.conn.SetLogger(slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	})))

	resp, err := a.initialize(ctx)
	if err != nil {
		a.shutdown()
		return nil, fmt.Errorf("agent %q: initialize: %w", name, err)
	}
	a.caps = resp.AgentCapabilities
	return a, nil
}

func (a *Agent) initialize(ctx context.Context) (acpsdk.InitializeResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	initCtx, cancel := context.WithTimeout(ctx, initTimeout)
	defer cancel()
	title := "Wingman"
	resp, err := a.conn.Initialize(initCtx, acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
		ClientInfo: &acpsdk.Implementation{
			Name: "wingman", Title: &title, Version: "0.1.0",
		},
		ClientCapabilities: acpsdk.ClientCapabilities{
			Fs: acpsdk.FileSystemCapabilities{
				ReadTextFile:  true,
				WriteTextFile: true,
			},
			Elicitation: &acpsdk.ElicitationCapabilities{
				Form: &acpsdk.ElicitationFormCapabilities{},
			},
		},
	})
	if err != nil {
		return acpsdk.InitializeResponse{}, err
	}
	// ACP negotiates down: the agent may answer with any version at or below
	// the one we offered, and both sides then speak that. Only a version above
	// ours is unusable.
	if resp.ProtocolVersion > acpsdk.ProtocolVersionNumber {
		return acpsdk.InitializeResponse{}, fmt.Errorf("unsupported ACP protocol version %d (want %d or lower)", resp.ProtocolVersion, acpsdk.ProtocolVersionNumber)
	}
	return resp, nil
}

func (a *Agent) Name() string               { return a.def.Name }
func (a *Agent) Workspace() *code.Workspace { return a.workspace }

func (a *Agent) Models(sessionID string) ([]model.Model, string) {
	if sess := a.session(sessionID); sess != nil {
		sess.mu.Lock()
		out := append([]model.Model(nil), sess.models...)
		current := sess.modelID
		sess.mu.Unlock()
		return out, current
	}
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	out := make([]model.Model, len(a.models))
	copy(out, a.models)
	if a.modelDefault != "" {
		return out, a.modelDefault
	}
	return out, a.modelID
}

func (a *Agent) SetModel(ctx context.Context, sessionID, id string) error {
	if sess := a.session(sessionID); sess != nil {
		sess.mu.Lock()
		optionID := sess.modelOptID
		sess.mu.Unlock()
		return a.setSessionConfig(ctx, sess, optionID, id)
	}
	a.configMu.Lock()
	a.modelDefault = id
	a.configMu.Unlock()
	return nil
}

func (a *Agent) Effort(sessionID string) (string, []string) {
	if sess := a.session(sessionID); sess != nil {
		sess.mu.Lock()
		current := sess.effortID
		opts := append([]string(nil), sess.effortOpts...)
		sess.mu.Unlock()
		return current, opts
	}
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	opts := make([]string, len(a.effortOpts))
	copy(opts, a.effortOpts)
	if a.effortDefault != "" {
		return a.effortDefault, opts
	}
	return a.effortID, opts
}

func (a *Agent) SetEffort(ctx context.Context, sessionID, value string) error {
	if sess := a.session(sessionID); sess != nil {
		sess.mu.Lock()
		optionID := sess.effortOptID
		sess.mu.Unlock()
		return a.setSessionConfig(ctx, sess, optionID, value)
	}
	a.configMu.Lock()
	a.effortDefault = value
	a.configMu.Unlock()
	return nil
}

func (a *Agent) setSessionConfig(ctx context.Context, sess *sessionState, configID, value string) error {
	if configID == "" {
		return errors.ErrUnsupported
	}
	resp, err := a.conn.SetSessionConfigOption(ctx, acpsdk.SetSessionConfigOptionRequest{
		ValueId: &acpsdk.SetSessionConfigOptionValueId{
			SessionId: sess.id,
			ConfigId:  acpsdk.SessionConfigId(configID),
			Value:     acpsdk.SessionConfigValueId(value),
		},
	})
	if err != nil {
		return err
	}
	a.refreshConfig(sess, resp.ConfigOptions)
	return nil
}

func (a *Agent) applySessionDefaults(ctx context.Context, sess *sessionState) error {
	a.configMu.RLock()
	modelID, effortID := a.modelDefault, a.effortDefault
	a.configMu.RUnlock()

	sess.mu.Lock()
	currentModel, currentEffort := sess.modelID, sess.effortID
	modelOptID, effortOptID := sess.modelOptID, sess.effortOptID
	sess.mu.Unlock()
	if modelID != "" && modelID != currentModel {
		if err := a.setSessionConfig(ctx, sess, modelOptID, modelID); err != nil {
			return fmt.Errorf("set default model: %w", err)
		}
	}
	if effortID != "" && effortID != currentEffort {
		sess.mu.Lock()
		effortOptID = sess.effortOptID
		sess.mu.Unlock()
		if err := a.setSessionConfig(ctx, sess, effortOptID, effortID); err != nil {
			return fmt.Errorf("set default effort: %w", err)
		}
	}
	return nil
}

func (a *Agent) Modes(sessionID string) ([]code.Mode, string) {
	sess := a.session(sessionID)
	if sess == nil {
		return nil, ""
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return append([]code.Mode(nil), sess.modes...), sess.modeID
}

func (a *Agent) SetMode(ctx context.Context, sessionID, modeID string) error {
	sess := a.session(sessionID)
	if sess == nil {
		return errors.ErrUnsupported
	}
	sess.mu.Lock()
	hasModes := len(sess.modes) > 0
	sess.mu.Unlock()
	if !hasModes {
		return errors.ErrUnsupported
	}
	if _, err := a.conn.SetSessionMode(ctx, acpsdk.SetSessionModeRequest{
		SessionId: sess.id,
		ModeId:    acpsdk.SessionModeId(modeID),
	}); err != nil {
		return err
	}
	sess.mu.Lock()
	sess.modeID = modeID
	sess.mu.Unlock()
	return nil
}

func (s *sessionState) applyModes(modes *acpsdk.SessionModeState) {
	if modes == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modes = s.modes[:0]
	for _, m := range modes.AvailableModes {
		mode := code.Mode{ID: string(m.Id), Name: m.Name}
		if m.Description != nil {
			mode.Description = *m.Description
		}
		s.modes = append(s.modes, mode)
	}
	s.modeID = string(modes.CurrentModeId)
}

func (a *Agent) ListSessions(ctx context.Context) ([]code.SessionInfo, error) {
	if a.caps.SessionCapabilities.List == nil {
		return nil, nil
	}
	cwd := a.workspace.RootPath
	var cursor *string
	seen := map[string]bool{}
	var out []code.SessionInfo
	for {
		resp, err := a.conn.ListSessions(ctx, acpsdk.ListSessionsRequest{Cwd: &cwd, Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, s := range resp.Sessions {
			info := code.SessionInfo{ID: string(s.SessionId)}
			if s.Title != nil {
				info.Title = *s.Title
			}
			if s.UpdatedAt != nil {
				if t, err := time.Parse(time.RFC3339, *s.UpdatedAt); err == nil {
					info.UpdatedAt = t
				}
			}
			if local := a.session(info.ID); local != nil {
				local.mu.Lock()
				if local.title != "" {
					info.Title = local.title
				}
				if !local.updatedAt.IsZero() {
					info.UpdatedAt = local.updatedAt
				}
				local.mu.Unlock()
			}
			out = append(out, info)
		}
		if resp.NextCursor == nil || *resp.NextCursor == "" {
			return out, nil
		}
		if seen[*resp.NextCursor] {
			return nil, fmt.Errorf("agent returned repeated session cursor %q", *resp.NextCursor)
		}
		seen[*resp.NextCursor] = true
		cursor = resp.NextCursor
	}
}

func (a *Agent) NewSession(ctx context.Context) (string, error) {
	resp, err := a.conn.NewSession(ctx, acpsdk.NewSessionRequest{
		Cwd:        a.workspace.RootPath,
		McpServers: []acpsdk.McpServer{},
	})
	if err != nil {
		return "", err
	}
	if resp.SessionId == "" {
		return "", errors.New("ACP agent returned an empty session ID")
	}

	id := string(resp.SessionId)
	sess := &sessionState{
		id:        resp.SessionId,
		toolCalls: map[string]toolCall{},
	}
	a.refreshConfig(sess, resp.ConfigOptions)
	sess.applyModes(resp.Modes)
	sess.loaded = true
	a.mu.Lock()
	a.sessions[id] = sess
	pending := a.pending[id]
	delete(a.pending, id)
	a.mu.Unlock()
	for _, update := range pending {
		a.applySessionStateUpdate(sess, update)
	}
	if err := a.applySessionDefaults(ctx, sess); err != nil {
		a.mu.Lock()
		delete(a.sessions, id)
		a.mu.Unlock()
		if a.caps.SessionCapabilities.Close != nil {
			closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			_, _ = a.conn.CloseSession(closeCtx, acpsdk.CloseSessionRequest{SessionId: sess.id})
			cancel()
		}
		return "", err
	}
	return id, nil
}

func (a *Agent) LoadSession(ctx context.Context, id string) error {
	var ferr error
	for _, err := range a.LoadSessionStream(ctx, id) {
		if err != nil {
			ferr = err
		}
	}
	return ferr
}

func (a *Agent) LoadSessionStream(ctx context.Context, id string) iter.Seq2[[]agent.Message, error] {
	if !a.caps.LoadSession && a.caps.SessionCapabilities.Resume == nil {
		return func(yield func([]agent.Message, error) bool) {
			yield(nil, errors.ErrUnsupported)
		}
	}

	a.mu.Lock()
	sess, exists := a.sessions[id]
	if !exists {
		sess = &sessionState{
			id:        acpsdk.SessionId(id),
			toolCalls: map[string]toolCall{},
		}
		a.sessions[id] = sess
	}
	pending := a.pending[id]
	delete(a.pending, id)
	a.mu.Unlock()
	for _, update := range pending {
		a.applySessionStateUpdate(sess, update)
	}

	sess.mu.Lock()
	if sess.loaded {
		snap := agent.CloneMessages(sess.messages)
		sess.mu.Unlock()
		return func(yield func([]agent.Message, error) bool) {
			yield(snap, nil)
		}
	}
	sess.mu.Unlock()

	return func(yield func([]agent.Message, error) bool) {
		loadCtx, cancel := context.WithCancel(ctx)
		t := &turn{
			ctx:    loadCtx,
			events: make(chan event, 256),
			done:   make(chan struct{}),
			cancel: cancel,
		}
		sess.mu.Lock()
		if sess.inflight != nil {
			sess.mu.Unlock()
			cancel()
			yield(nil, fmt.Errorf("session %s is busy", id))
			return
		}
		sess.inflight = t
		sess.mu.Unlock()

		ok := false
		defer func() {
			close(t.done)
			cancel()
			sess.toolCallsMu.Lock()
			clear(sess.toolCalls)
			sess.toolCallsMu.Unlock()
			sess.mu.Lock()
			sess.inflight = nil
			if ok {
				if emitted := t.messages(); len(emitted) > 0 {
					sess.messages = append(sess.messages, emitted...)
				}
				sess.loaded = true
			}
			sess.mu.Unlock()
		}()

		snapshot := func() []agent.Message {
			return t.messages()
		}

		loadErrCh := make(chan error, 1)
		go func() {
			if a.caps.LoadSession {
				resp, err := a.conn.LoadSession(loadCtx, acpsdk.LoadSessionRequest{
					SessionId:  acpsdk.SessionId(id),
					Cwd:        a.workspace.RootPath,
					McpServers: []acpsdk.McpServer{},
				})
				if err == nil {
					a.refreshConfig(sess, resp.ConfigOptions)
					sess.applyModes(resp.Modes)
				}
				loadErrCh <- err
				return
			}
			resp, err := a.conn.ResumeSession(loadCtx, acpsdk.ResumeSessionRequest{
				SessionId:  acpsdk.SessionId(id),
				Cwd:        a.workspace.RootPath,
				McpServers: []acpsdk.McpServer{},
			})
			if err == nil {
				a.refreshConfig(sess, resp.ConfigOptions)
				sess.applyModes(resp.Modes)
			}
			loadErrCh <- err
		}()

		for {
			select {
			case <-ctx.Done():
				yield(nil, ctx.Err())
				return
			case err := <-loadErrCh:
				if err != nil {
					yield(nil, err)
					return
				}
				ok = true
				yield(snapshot(), nil)
				return
			case ev := <-t.events:
				if ev.done {
					if ev.err != nil {
						yield(nil, ev.err)
						return
					}
					continue
				}
				if !yield(snapshot(), nil) {
					return
				}
			}
		}
	}
}

func (a *Agent) SupportsDelete() bool {
	return a.caps.SessionCapabilities.Delete != nil
}

func (a *Agent) DeleteSession(ctx context.Context, id string) error {
	if !a.SupportsDelete() {
		return errors.ErrUnsupported
	}
	_, err := a.conn.UnstableDeleteSession(ctx, acpsdk.UnstableDeleteSessionRequest{
		SessionId: acpsdk.SessionId(id),
	})
	if err == nil {
		a.mu.Lock()
		delete(a.sessions, id)
		a.mu.Unlock()
	}
	return err
}

func (a *Agent) Messages(id string) []agent.Message {
	sess := a.session(id)
	if sess == nil {
		return nil
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return agent.CloneMessages(sess.messages)
}

func (a *Agent) HistorySnapshot(id string) code.HistorySnapshot {
	sess := a.session(id)
	if sess == nil {
		return code.HistorySnapshot{}
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return code.HistorySnapshot{Messages: agent.CloneMessages(sess.messages)}
}

func (a *Agent) HistoryVersion(id string) code.HistoryVersion {
	sess := a.session(id)
	if sess == nil {
		return code.HistoryVersion{}
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return code.HistoryVersion{MessageCount: len(sess.messages)}
}

func (a *Agent) Commands(id string) []code.Command {
	sess := a.session(id)
	if sess == nil {
		return nil
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return append([]code.Command(nil), sess.commands...)
}

func (a *Agent) Usage(id string) agent.Usage {
	sess := a.session(id)
	if sess == nil {
		return agent.Usage{}
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.usage
}

func (a *Agent) session(id string) *sessionState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessions[id]
}

func (a *Agent) Send(ctx context.Context, id string, input []agent.Content) (iter.Seq2[agent.Message, error], error) {
	if len(input) == 0 {
		return nil, code.ErrEmptyInput
	}
	input = agent.CloneContent(input)
	a.mu.Lock()
	sess, ok := a.sessions[id]
	a.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("session %s not found; call NewSession first", id)
	}

	sendCtx, cancel := context.WithCancel(ctx)
	t := &turn{
		ctx:               sendCtx,
		events:            make(chan event, 256),
		done:              make(chan struct{}),
		cancel:            cancel,
		ignoreUserUpdates: true,
	}
	sess.mu.Lock()
	if sess.inflight != nil {
		sess.mu.Unlock()
		cancel()
		return nil, code.ErrTurnInProgress
	}
	sess.inflight = t
	sess.messages = append(sess.messages, agent.Message{
		Role:    agent.RoleUser,
		Content: input,
	})
	sess.mu.Unlock()

	go func() {
		resp, err := a.conn.Prompt(sendCtx, acpsdk.PromptRequest{
			SessionId: sess.id,
			Prompt:    acp.ContentToBlocks(input),
		})
		remoteCancelled := err == nil && resp.StopReason == acpsdk.StopReasonCancelled
		if remoteCancelled {
			err = context.Canceled
		} else if err == nil {
			if reason := stopReasonError(resp.StopReason); reason != nil {
				err = reason
			}
		}

		if resp.Usage != nil {
			sess.mu.Lock()
			lastInputTokens := sess.usage.LastInputTokens
			contextWindow := sess.usage.ContextWindow
			usage := usageFromACP(resp.Usage)
			usage.LastInputTokens = lastInputTokens
			usage.ContextWindow = contextWindow
			sess.usage = usage
			sess.mu.Unlock()
		}
		select {
		case t.events <- event{done: true, err: err, remoteCancelled: remoteCancelled}:
		case <-t.done:
		}
	}()

	return func(yield func(agent.Message, error) bool) {
		completed := false
		defer func() {
			cancel()
			close(t.done)
			if !completed {
				a.cancelPrompt(sess.id)
			}
			sess.finalizeTurn(t)
		}()
		flushCancelled := func() bool {
			for _, msg := range sess.cancelOutstandingToolCalls(t) {
				if !yield(msg, nil) {
					return false
				}
			}
			return true
		}
		for {
			select {
			case <-ctx.Done():
				if flushCancelled() {
					yield(agent.Message{}, ctx.Err())
				}
				return
			case ev := <-t.events:
				if ev.done {
					completed = ev.remoteCancelled || ev.err == nil ||
						(!errors.Is(ev.err, context.Canceled) && !errors.Is(ev.err, context.DeadlineExceeded))
					if !flushCancelled() {
						return
					}
					if ev.err != nil {
						yield(agent.Message{}, ev.err)
					}
					return
				}
				if !yield(ev.msg, nil) {
					return
				}
			}
		}
	}, nil
}

// stopReasonError reports a turn cut short; end_turn and cancelled are the caller's.
func stopReasonError(reason acpsdk.StopReason) error {
	switch reason {
	case acpsdk.StopReasonRefusal:
		return errors.New("the agent refused to continue this turn")
	case acpsdk.StopReasonMaxTokens:
		return errors.New("the agent stopped: output token limit reached")
	case acpsdk.StopReasonMaxTurnRequests:
		return errors.New("the agent stopped: maximum requests per turn reached")
	default:
		return nil
	}
}

func usageFromACP(usage *acpsdk.Usage) agent.Usage {
	if usage == nil {
		return agent.Usage{}
	}
	cacheRead := optionalACPTokenCount(usage.CachedReadTokens)
	cacheCreation := optionalACPTokenCount(usage.CachedWriteTokens)
	reasoning := optionalACPTokenCount(usage.ThoughtTokens)
	return agent.Usage{
		InputTokens:              tokenCount64(usage.InputTokens) + cacheRead + cacheCreation,
		OutputTokens:             tokenCount64(usage.OutputTokens) + reasoning,
		ReasoningTokens:          reasoning,
		CacheReadInputTokens:     cacheRead,
		CacheCreationInputTokens: cacheCreation,
	}
}

func optionalACPTokenCount(value *int) int64 {
	if value == nil {
		return 0
	}
	return tokenCount64(*value)
}

func tokenCount64(value int) int64 {
	if value <= 0 {
		return 0
	}
	return int64(value)
}

func (a *Agent) cancelPrompt(id acpsdk.SessionId) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = a.conn.Cancel(ctx, acpsdk.CancelNotification{SessionId: id})
}

func (a *Agent) TurnFeatures(string) code.TurnFeatures {
	return code.TurnFeatures{Steer: a.steer != nil}
}

func (a *Agent) Steer(ctx context.Context, id string, input code.TurnInput) error {
	if a.steer == nil {
		return code.ErrNoActiveTurn
	}
	sess := a.session(id)
	if sess == nil {
		return fmt.Errorf("session %s not found", id)
	}
	sess.mu.Lock()
	t := sess.inflight
	sess.mu.Unlock()
	if t == nil {
		return code.ErrNoActiveTurn
	}
	input.Content = agent.CloneContent(input.Content)
	if err := a.steer(ctx, sess.id, acp.ContentToBlocks(input.Content), input.ID); err != nil {
		return err
	}
	sess.mu.Lock()
	steered := agent.Message{Role: agent.RoleUser, Content: input.Content}
	if sess.inflight == t {
		t.mu.Lock()
		t.emitted = append(t.emitted, steered)
		t.mu.Unlock()
	} else {
		// The backend accepted the steer at the turn boundary, after our stream
		// finalized. Keep the accepted user input visible without retrying it.
		sess.messages = append(sess.messages, steered)
	}
	sess.mu.Unlock()
	return nil
}

func (a *Agent) Cancel(id string) {
	sess := a.session(id)
	if sess == nil {
		return
	}
	sess.mu.Lock()
	t := sess.inflight
	sess.mu.Unlock()
	if t != nil {
		t.cancel()
	}
}

func (a *Agent) Close() error {
	a.shutdown()
	return nil
}

func (a *Agent) shutdown() {
	a.closeOnce.Do(func() {
		a.mu.Lock()
		sessions := make([]*sessionState, 0, len(a.sessions))
		for _, sess := range a.sessions {
			sessions = append(sessions, sess)
			sess.mu.Lock()
			if sess.inflight != nil {
				sess.inflight.cancel()
			}
			sess.mu.Unlock()
		}
		a.mu.Unlock()

		if a.caps.SessionCapabilities.Close != nil && len(sessions) > 0 {
			for _, sess := range sessions {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				_, _ = a.conn.CloseSession(ctx, acpsdk.CloseSessionRequest{SessionId: sess.id})
				cancel()
			}
		}

		if a.stdin != nil {
			_ = a.stdin.Close()
		}

		if a.serverDone != nil {
			select {
			case <-a.serverDone:
			case <-time.After(2 * time.Second):
			}
		}
		if a.serverW != nil {
			_ = a.serverW.Close()
		}
		if a.cleanup != nil {
			_ = a.cleanup()
		}

		if a.cmd == nil || a.cmd.Process == nil {
			return
		}
		exited := make(chan struct{})
		go func() {
			_ = a.cmd.Wait()
			close(exited)
		}()
		select {
		case <-exited:
		case <-time.After(2 * time.Second):
			_ = a.cmd.Process.Kill()
			<-exited
		}
	})
}

func (a *Agent) SessionUpdate(_ context.Context, n acpsdk.SessionNotification) error {
	sess := a.session(string(n.SessionId))
	if sess == nil {
		if isSessionStateUpdate(n.Update) {
			a.rememberPendingUpdate(string(n.SessionId), n.Update)
		}
		return nil
	}
	if a.applySessionStateUpdate(sess, n.Update) {
		return nil
	}
	sess.mu.Lock()
	t := sess.inflight
	sess.mu.Unlock()
	if t == nil {
		return nil
	}
	msg, ok := a.translateUpdate(sess, t, n.Update)
	if !ok {
		return nil
	}
	select {
	case t.events <- event{msg: msg}:
	case <-t.done:
	}
	return nil
}

func isSessionStateUpdate(update acpsdk.SessionUpdate) bool {
	return update.ConfigOptionUpdate != nil || update.CurrentModeUpdate != nil ||
		update.AvailableCommandsUpdate != nil || update.SessionInfoUpdate != nil || update.UsageUpdate != nil
}

func (a *Agent) rememberPendingUpdate(id string, update acpsdk.SessionUpdate) {
	if id == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	updates, exists := a.pending[id]
	if !exists && len(a.pending) >= 128 {
		return
	}
	if len(updates) < 32 {
		if a.pending == nil {
			a.pending = make(map[string][]acpsdk.SessionUpdate)
		}
		a.pending[id] = append(updates, update)
	}
}

func (a *Agent) applySessionStateUpdate(sess *sessionState, update acpsdk.SessionUpdate) bool {
	if config := update.ConfigOptionUpdate; config != nil {
		a.refreshConfig(sess, config.ConfigOptions)
		return true
	}
	if mode := update.CurrentModeUpdate; mode != nil {
		sess.mu.Lock()
		sess.modeID = string(mode.CurrentModeId)
		sess.mu.Unlock()
		return true
	}
	if commandsUpdate := update.AvailableCommandsUpdate; commandsUpdate != nil {
		commands := make([]code.Command, 0, len(commandsUpdate.AvailableCommands))
		for _, command := range commandsUpdate.AvailableCommands {
			item := code.Command{Name: command.Name, Description: command.Description}
			if command.Input != nil && command.Input.Unstructured != nil {
				item.InputHint = command.Input.Unstructured.Hint
			}
			commands = append(commands, item)
		}
		sess.mu.Lock()
		sess.commands = commands
		sess.mu.Unlock()
		return true
	}
	if info := update.SessionInfoUpdate; info != nil {
		sess.mu.Lock()
		if info.Title != nil {
			sess.title = *info.Title
		}
		if info.UpdatedAt != nil {
			if parsed, err := time.Parse(time.RFC3339, *info.UpdatedAt); err == nil {
				sess.updatedAt = parsed
			}
		}
		sess.mu.Unlock()
		return true
	}
	if usage := update.UsageUpdate; usage != nil {
		sess.mu.Lock()
		sess.usage.LastInputTokens = int64(usage.Used)
		sess.usage.ContextWindow = int64(usage.Size)
		sess.mu.Unlock()
		return true
	}
	return false
}

func (a *Agent) translateUpdate(sess *sessionState, t *turn, u acpsdk.SessionUpdate) (agent.Message, bool) {
	emit := func(role agent.MessageRole, c agent.Content, contentKey string) agent.Message {
		t.mu.Lock()
		if n := len(t.emitted); n > 0 && t.emitted[n-1].Role == role {
			contents := t.emitted[n-1].Content
			if len(contents) == 0 || t.lastContentKey != contentKey || !mergeACPContent(&contents[len(contents)-1], c) {
				t.emitted[n-1].Content = append(contents, c)
			}
		} else {
			t.emitted = append(t.emitted, agent.Message{Role: role, Content: []agent.Content{c}})
		}
		t.lastContentKey = contentKey
		t.mu.Unlock()
		return agent.Message{Role: role, Content: []agent.Content{c}}
	}

	switch {
	case u.UserMessageChunk != nil:
		if t.ignoreUserUpdates {
			return agent.Message{}, false
		}
		content, ok := messageBlockContent(u.UserMessageChunk.Content)
		if !ok {
			return agent.Message{}, false
		}
		id := ""
		if u.UserMessageChunk.MessageId != nil {
			id = *u.UserMessageChunk.MessageId
		}
		return emit(agent.RoleUser, content, "user:"+id), true

	case u.AgentMessageChunk != nil:
		content, ok := messageBlockContent(u.AgentMessageChunk.Content)
		if !ok {
			return agent.Message{}, false
		}
		id := ""
		if u.AgentMessageChunk.MessageId != nil {
			id = *u.AgentMessageChunk.MessageId
		}
		if content.Text != "" {
			content.TextID = id
		}
		return emit(agent.RoleAssistant, content, "agent:"+id), true

	case u.AgentThoughtChunk != nil:
		text := blockText(u.AgentThoughtChunk.Content)
		if text == "" {
			return agent.Message{}, false
		}
		id := ""
		if u.AgentThoughtChunk.MessageId != nil {
			id = *u.AgentThoughtChunk.MessageId
		}
		return emit(agent.RoleAssistant, agent.Content{Reasoning: &agent.Reasoning{ID: id, Summary: text}}, "thought:"+id), true

	case u.ToolCall != nil:
		tc := u.ToolCall
		args := rawValueToString(tc.RawInput)
		kind := semanticToolKind(tc.Kind, tc.Content)
		locations := agentToolLocations(a.workspaceRoot(), tc.Locations)

		name := tc.Title
		if name == "" {
			name = string(tc.Kind)
		}
		presentation := agent.NewToolPresentation(name, kind, args, locations)
		sess.toolCallsMu.Lock()
		sess.toolCalls[string(tc.ToolCallId)] = toolCall{
			name: name, kind: kind, args: args, locations: locations,
			diff: toolCallDiffText(tc.Content),
		}
		sess.toolCallsMu.Unlock()
		callMsg := emit(agent.RoleAssistant, agent.Content{ToolCall: &agent.ToolCall{
			ID:           string(tc.ToolCallId),
			Name:         name,
			Kind:         kind,
			Args:         args,
			Locations:    locations,
			Presentation: presentation,
		}}, "")

		// A tool_call may arrive already terminal, with no tool_call_update to follow.
		if tc.Status == acpsdk.ToolCallStatusCompleted || tc.Status == acpsdk.ToolCallStatusFailed {
			sess.toolCallsMu.Lock()
			delete(sess.toolCalls, string(tc.ToolCallId))
			sess.toolCallsMu.Unlock()

			body := toolCallContentText(tc.Content)
			if body == "" {
				body = toolCallDiffText(tc.Content)
			}
			if body == "" && tc.RawOutput != nil {
				body = rawValueToString(tc.RawOutput)
			}
			failed := tc.Status == acpsdk.ToolCallStatusFailed
			if body == "" && failed {
				body = "tool call failed"
			}
			t.send(callMsg)
			return emit(agent.RoleAssistant, agent.Content{ToolResult: &agent.ToolResult{
				ID:           string(tc.ToolCallId),
				Name:         name,
				Kind:         kind,
				Args:         args,
				Locations:    locations,
				Presentation: presentation,
				Content:      body,
				IsError:      failed,
			}}, ""), true
		}
		return callMsg, true

	case u.ToolCallUpdate != nil:
		tu := u.ToolCallUpdate
		sess.toolCallsMu.Lock()
		prior := sess.toolCalls[string(tu.ToolCallId)]
		if prior.name == "" && tu.Title != nil && *tu.Title != "" {
			prior.name = *tu.Title
		}
		if tu.RawInput != nil {
			prior.args = rawValueToString(tu.RawInput)
		}
		if tu.Kind != nil {
			prior.kind = semanticToolKind(*tu.Kind, tu.Content)
		} else if prior.kind == "" {
			prior.kind = semanticToolKind("", tu.Content)
		}
		if tu.Locations != nil {
			prior.locations = agentToolLocations(a.workspaceRoot(), tu.Locations)
		}
		if diff := toolCallDiffText(tu.Content); diff != "" {
			prior.diff = diff
		}
		sess.toolCalls[string(tu.ToolCallId)] = prior
		sess.toolCallsMu.Unlock()
		if tu.Status == nil {
			a.reportToolProgress(t, tu)
			return agent.Message{}, false
		}
		status := *tu.Status
		if status != acpsdk.ToolCallStatusCompleted && status != acpsdk.ToolCallStatusFailed {
			a.reportToolProgress(t, tu)
			return agent.Message{}, false
		}
		sess.toolCallsMu.Lock()
		prior = sess.toolCalls[string(tu.ToolCallId)]
		delete(sess.toolCalls, string(tu.ToolCallId))
		sess.toolCallsMu.Unlock()
		body := toolCallContentText(tu.Content)
		if body == "" {
			body = prior.diff
		}
		if body == "" && tu.RawOutput != nil {
			body = rawValueToString(tu.RawOutput)
		}
		if body == "" && status == acpsdk.ToolCallStatusFailed {
			body = "tool call failed"
		}
		return emit(agent.RoleAssistant, agent.Content{ToolResult: &agent.ToolResult{
			ID:        string(tu.ToolCallId),
			Name:      prior.name,
			Kind:      prior.kind,
			Args:      prior.args,
			Locations: prior.locations,
			Presentation: agent.NewToolPresentation(
				prior.name, prior.kind, prior.args, prior.locations,
			),
			Content: body,
			IsError: status == acpsdk.ToolCallStatusFailed,
		}}, ""), true

	}
	return agent.Message{}, false
}

func messageBlockContent(block acpsdk.ContentBlock) (agent.Content, bool) {
	contents := acp.ContentFromBlocks([]acpsdk.ContentBlock{block})
	if len(contents) != 1 {
		return agent.Content{}, false
	}
	return contents[0], true
}

func (a *Agent) reportToolProgress(t *turn, update *acpsdk.SessionToolCallUpdate) {
	if t == nil || t.ctx == nil || update == nil {
		return
	}
	text := ""
	if update.Title != nil {
		text = *update.Title
	}
	if text == "" {
		text = toolCallContentText(update.Content)
	}
	if text == "" && update.RawOutput != nil {
		text = rawValueToString(update.RawOutput)
	}
	if text == "" {
		return
	}
	if progress := tool.Progress(tool.WithProgressCall(t.ctx, string(update.ToolCallId))); progress != nil {
		progress(text)
	}
}

// mergeACPContent keeps transport chunks as deltas for the live stream while
// storing them as one logical content block for transcript rendering. Without
// this, a tokenized Codex response becomes one finalized TUI cell per token.
func mergeACPContent(dst *agent.Content, src agent.Content) bool {
	if dst.Text != "" && src.Text != "" {
		dst.Text += src.Text
		return true
	}
	if dst.Reasoning != nil && src.Reasoning != nil && dst.Reasoning.ID == src.Reasoning.ID {
		dst.Reasoning.Summary += src.Reasoning.Summary
		return true
	}
	return false
}

func (a *Agent) SetUI(ui code.UI) {
	a.uiMu.Lock()
	a.ui = ui
	a.uiMu.Unlock()
}

func (a *Agent) currentUI() code.UI {
	a.uiMu.RLock()
	defer a.uiMu.RUnlock()
	return a.ui
}

func (a *Agent) RequestPermission(ctx context.Context, p acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
	cancelled := acpsdk.RequestPermissionResponse{
		Outcome: acpsdk.RequestPermissionOutcome{Cancelled: &acpsdk.RequestPermissionOutcomeCancelled{}},
	}
	if len(p.Options) == 0 {
		return cancelled, nil
	}
	selected := func(id acpsdk.PermissionOptionId) acpsdk.RequestPermissionResponse {
		return acpsdk.RequestPermissionResponse{
			Outcome: acpsdk.RequestPermissionOutcome{
				Selected: &acpsdk.RequestPermissionOutcomeSelected{OptionId: id},
			},
		}
	}
	if sess := a.session(string(p.SessionId)); sess != nil {
		sess.mu.Lock()
		unattended := sess.modeID == code.UnattendedModeID
		sess.mu.Unlock()
		if unattended {
			if opt := pickPermissionOption(p.Options, true); opt != nil {
				return selected(opt.OptionId), nil
			}
			return cancelled, nil
		}
	}

	ui := a.currentUI()
	if ui == nil {
		if opt := pickPermissionOption(p.Options, false); opt != nil {
			return selected(opt.OptionId), nil
		}
		return cancelled, nil
	}

	// Preferred: a strict single-choice elicitation so the user can pick any
	// of the offered options (answers, always-allow scopes, selects) instead
	// of a yes/no collapse.
	names := make([]string, len(p.Options))
	descriptions := make([]string, len(p.Options))
	hasDescription := false
	for i, o := range p.Options {
		names[i] = o.Name
		descriptions[i] = permissionOptionDescription(o.Meta)
		hasDescription = hasDescription || descriptions[i] != ""
	}
	if !hasDescription {
		descriptions = nil
	}
	res, err := ui.Elicit(code.WithSessionID(ctx, string(p.SessionId)), tool.ElicitRequest{
		Message: permissionMessage(p),
		Fields: []tool.ElicitField{{
			Name:             "choice",
			Type:             "string",
			Required:         true,
			Enum:             names,
			EnumDescriptions: descriptions,
			Strict:           true,
		}},
	})
	if err == nil {
		switch res.Action {
		case tool.ElicitAccept:
			if choice, ok := res.Content["choice"].(string); ok {
				for i := range p.Options {
					if p.Options[i].Name == choice {
						return selected(p.Options[i].OptionId), nil
					}
				}
			}
			return cancelled, nil
		case tool.ElicitDecline:
			if opt := pickPermissionOption(p.Options, false); opt != nil {
				return selected(opt.OptionId), nil
			}
			return cancelled, nil
		default:
			return cancelled, nil
		}
	}

	ok, err := ui.Confirm(code.WithSessionID(ctx, string(p.SessionId)), permissionMessage(p))
	if err != nil {
		return cancelled, nil
	}
	if ok {
		if opt := pickPermissionOption(p.Options, true); opt != nil {
			return selected(opt.OptionId), nil
		}
		return selected(p.Options[0].OptionId), nil
	}
	if opt := pickPermissionOption(p.Options, false); opt != nil {
		return selected(opt.OptionId), nil
	}
	return cancelled, nil
}

func (a *Agent) UnstableCreateElicitation(ctx context.Context, p acpsdk.UnstableCreateElicitationRequest) (acpsdk.UnstableCreateElicitationResponse, error) {
	cancel := acpsdk.UnstableCreateElicitationResponse{Cancel: &acpsdk.UnstableCreateElicitationCancel{Action: "cancel"}}
	if p.Form == nil {
		return cancel, nil
	}
	// acp-go-sdk v0.13.5 does not expose top-level session scope for form
	// elicitations. Native adapters preserve it as metadata; use that exact
	// scope before falling back to the only active turn.
	sid := elicitationSessionID(p.Form.Meta)
	if sid == "" || a.session(sid) == nil {
		sid = a.activeSessionID()
	}
	if sid != "" {
		ctx = code.WithSessionID(ctx, sid)
	}
	req := tool.ElicitRequest{
		Message: p.Form.Message,
		Fields:  elicitFieldsFromSchema(p.Form.RequestedSchema),
	}
	if sess := a.session(sid); sess != nil {
		sess.mu.Lock()
		unattended := sess.modeID == code.UnattendedModeID
		sess.mu.Unlock()
		if unattended {
			return elicitationResponse(code.UnattendedElicitation(req)), nil
		}
	}

	ui := a.currentUI()
	if ui == nil {
		return acpsdk.UnstableCreateElicitationResponse{Decline: &acpsdk.UnstableCreateElicitationDecline{Action: "decline"}}, nil
	}
	res, err := ui.Elicit(ctx, req)
	if err != nil {
		return cancel, nil
	}
	return elicitationResponse(res), nil
}

func elicitationResponse(res tool.ElicitResult) acpsdk.UnstableCreateElicitationResponse {
	switch res.Action {
	case tool.ElicitAccept:
		content := res.Content
		if content == nil {
			content = map[string]any{}
		}
		return acpsdk.UnstableCreateElicitationResponse{Accept: &acpsdk.UnstableCreateElicitationAccept{Action: "accept", Content: content}}
	case tool.ElicitDecline:
		return acpsdk.UnstableCreateElicitationResponse{Decline: &acpsdk.UnstableCreateElicitationDecline{Action: "decline"}}
	}
	return acpsdk.UnstableCreateElicitationResponse{Cancel: &acpsdk.UnstableCreateElicitationCancel{Action: "cancel"}}
}

func elicitationSessionID(meta map[string]any) string {
	sid, _ := meta["sessionId"].(string)
	return sid
}

func (a *Agent) activeSessionID() string {
	a.mu.Lock()
	ids := make([]string, 0, len(a.sessions))
	states := make([]*sessionState, 0, len(a.sessions))
	for sid, sess := range a.sessions {
		ids = append(ids, sid)
		states = append(states, sess)
	}
	a.mu.Unlock()

	if len(ids) == 1 {
		return ids[0]
	}
	for i, sess := range states {
		sess.mu.Lock()
		inflight := sess.inflight != nil
		sess.mu.Unlock()
		if inflight {
			return ids[i]
		}
	}
	return ""
}

func elicitFieldsFromSchema(schema acpsdk.UnstableElicitationSchema) []tool.ElicitField {
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	required := map[string]bool{}
	for _, r := range schema.Required {
		required[r] = true
	}

	fields := make([]tool.ElicitField, 0, len(names))
	for _, name := range names {
		prop, _ := schema.Properties[name].(map[string]any)
		f := tool.ElicitField{Name: name, Required: required[name]}
		f.Title, _ = prop["title"].(string)
		f.Description, _ = prop["description"].(string)
		f.Default = prop["default"]
		if meta, _ := prop["_meta"].(map[string]any); meta != nil {
			if marker, _ := meta["_askUserQuestionCustomAnswer"].(map[string]any); marker != nil {
				isCustom, _ := marker["isCustomAnswer"].(bool)
				if isCustom {
					f.CustomAnswerFor, _ = marker["questionId"].(string)
				}
			}
		}

		typ, _ := prop["type"].(string)
		options := prop["oneOf"]
		if typ == "array" {
			f.Multiple = true
			typ = "string"
			if items, ok := prop["items"].(map[string]any); ok {
				for _, key := range []string{"anyOf", "oneOf", "enum"} {
					if v, ok := items[key]; ok {
						options = v
						break
					}
				}
			}
		}
		if options == nil {
			options = prop["enum"]
		}
		f.Type = typ

		values, descriptions := elicitEnumOptions(options)
		if len(values) > 0 {
			f.Enum = values
			f.EnumDescriptions = descriptions
			f.Strict = true
		}
		fields = append(fields, f)
	}
	return fields
}

func elicitEnumOptions(v any) (values, descriptions []string) {
	items, ok := v.([]any)
	if !ok {
		return nil, nil
	}
	hasDescription := false
	for _, item := range items {
		switch o := item.(type) {
		case string:
			values = append(values, o)
			descriptions = append(descriptions, "")
		case map[string]any:
			value, _ := o["const"].(string)
			if value == "" {
				value, _ = o["title"].(string)
			}
			if value == "" {
				continue
			}
			desc, _ := o["description"].(string)
			values = append(values, value)
			descriptions = append(descriptions, desc)
			if desc != "" {
				hasDescription = true
			}
		}
	}
	if !hasDescription {
		descriptions = nil
	}
	return values, descriptions
}

func pickPermissionOption(opts []acpsdk.PermissionOption, allow bool) *acpsdk.PermissionOption {
	want := []acpsdk.PermissionOptionKind{acpsdk.PermissionOptionKindRejectOnce, acpsdk.PermissionOptionKindRejectAlways}
	if allow {
		want = []acpsdk.PermissionOptionKind{acpsdk.PermissionOptionKindAllowOnce, acpsdk.PermissionOptionKindAllowAlways}
	}
	for _, k := range want {
		for i := range opts {
			if opts[i].Kind == k {
				return &opts[i]
			}
		}
	}
	return nil
}

func permissionOptionDescription(meta map[string]any) string {
	permission, _ := meta["permission"].(map[string]any)
	if permission == nil {
		return ""
	}
	var changes []any
	switch value := permission["changes"].(type) {
	case []any:
		changes = value
	case []map[string]any:
		changes = make([]any, len(value))
		for i := range value {
			changes[i] = value[i]
		}
	}
	seen := map[string]bool{}
	var descriptions []string
	for _, raw := range changes {
		change, _ := raw.(map[string]any)
		description, _ := change["description"].(string)
		description = strings.TrimSpace(description)
		if description != "" && !seen[description] {
			seen[description] = true
			descriptions = append(descriptions, description)
		}
	}
	return strings.Join(descriptions, "; ")
}

func permissionMessage(p acpsdk.RequestPermissionRequest) string {
	var parts []string
	if p.ToolCall.Title != nil && *p.ToolCall.Title != "" {
		parts = append(parts, *p.ToolCall.Title)
	}
	if m, ok := p.ToolCall.RawInput.(map[string]any); ok {
		if cmd, ok := m["command"].(string); ok && cmd != "" {
			parts = append(parts, "$ "+cmd)
		}
	}
	if t := toolCallContentText(p.ToolCall.Content); t != "" {
		parts = append(parts, t)
	}
	if len(parts) == 0 {
		return "Allow this action?"
	}
	return strings.Join(parts, "\n\n")
}

func (a *Agent) WriteTextFile(_ context.Context, p acpsdk.WriteTextFileRequest) (acpsdk.WriteTextFileResponse, error) {
	abs, err := a.resolvePath(p.Path)
	if err != nil {
		return acpsdk.WriteTextFileResponse{}, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return acpsdk.WriteTextFileResponse{}, err
	}
	return acpsdk.WriteTextFileResponse{}, os.WriteFile(abs, []byte(p.Content), 0o644)
}

func (a *Agent) ReadTextFile(_ context.Context, p acpsdk.ReadTextFileRequest) (acpsdk.ReadTextFileResponse, error) {
	abs, err := a.resolvePath(p.Path)
	if err != nil {
		return acpsdk.ReadTextFileResponse{}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return acpsdk.ReadTextFileResponse{}, err
	}
	content := string(data)
	if p.Line != nil || p.Limit != nil {
		lines := strings.Split(content, "\n")
		start := 0
		if p.Line != nil && *p.Line > 0 {
			start = min(*p.Line-1, len(lines))
		}
		end := len(lines)
		if p.Limit != nil && *p.Limit > 0 && start+*p.Limit < end {
			end = start + *p.Limit
		}
		content = strings.Join(lines[start:end], "\n")
	}
	return acpsdk.ReadTextFileResponse{Content: content}, nil
}

func (a *Agent) resolvePath(p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("path must be absolute: %s", p)
	}
	return filepath.Clean(p), nil
}

func (a *Agent) CreateTerminal(context.Context, acpsdk.CreateTerminalRequest) (acpsdk.CreateTerminalResponse, error) {
	return acpsdk.CreateTerminalResponse{}, errors.New("terminal not supported")
}

// Terminal is not advertised; a zero-valued success would read as real data.
func (a *Agent) KillTerminal(context.Context, acpsdk.KillTerminalRequest) (acpsdk.KillTerminalResponse, error) {
	return acpsdk.KillTerminalResponse{}, acpsdk.NewMethodNotFound("terminal/kill")
}
func (a *Agent) ReleaseTerminal(context.Context, acpsdk.ReleaseTerminalRequest) (acpsdk.ReleaseTerminalResponse, error) {
	return acpsdk.ReleaseTerminalResponse{}, acpsdk.NewMethodNotFound("terminal/release")
}
func (a *Agent) TerminalOutput(context.Context, acpsdk.TerminalOutputRequest) (acpsdk.TerminalOutputResponse, error) {
	return acpsdk.TerminalOutputResponse{}, acpsdk.NewMethodNotFound("terminal/output")
}
func (a *Agent) WaitForTerminalExit(context.Context, acpsdk.WaitForTerminalExitRequest) (acpsdk.WaitForTerminalExitResponse, error) {
	return acpsdk.WaitForTerminalExitResponse{}, acpsdk.NewMethodNotFound("terminal/wait_for_exit")
}

func (a *Agent) refreshConfig(sess *sessionState, options []acpsdk.SessionConfigOption) {
	if options == nil {
		return
	}
	modelID, effortID := "", ""
	modelOptID, effortOptID := "", ""
	var models []model.Model
	var effortOpts []string
	for _, opt := range options {
		if opt.Select == nil {
			continue
		}
		kind := ""
		if opt.Select.Category != nil {
			kind = string(*opt.Select.Category)
		}
		if kind == "" {
			kind = string(opt.Select.Id)
		}
		values := sessionConfigValues(opt.Select.Options)
		switch kind {
		case string(acpsdk.SessionConfigOptionCategoryModel):
			modelOptID = string(opt.Select.Id)
			modelID = string(opt.Select.CurrentValue)
			for _, v := range values {
				description := ""
				if v.Description != nil {
					description = *v.Description
				}
				models = append(models, model.Model{
					ID:          string(v.Value),
					Name:        v.Name,
					Description: description,
				})
			}
		case string(acpsdk.SessionConfigOptionCategoryThoughtLevel), effortConfigID:
			effortOptID = string(opt.Select.Id)
			effortID = string(opt.Select.CurrentValue)
			for _, v := range values {
				effortOpts = append(effortOpts, string(v.Value))
			}
		}
	}
	a.configMu.Lock()
	a.models = models
	a.modelID = modelID
	a.effortID = effortID
	a.effortOpts = effortOpts
	a.configMu.Unlock()

	if sess != nil {
		sess.mu.Lock()
		sess.models = append(sess.models[:0], models...)
		sess.modelID = modelID
		sess.modelOptID = modelOptID
		sess.effortID = effortID
		sess.effortOptID = effortOptID
		sess.effortOpts = append(sess.effortOpts[:0], effortOpts...)
		sess.mu.Unlock()
	}
}

func sessionConfigValues(options acpsdk.SessionConfigSelectOptions) []acpsdk.SessionConfigSelectOption {
	if options.Ungrouped != nil {
		return append([]acpsdk.SessionConfigSelectOption(nil), (*options.Ungrouped)...)
	}
	if options.Grouped == nil {
		return nil
	}
	var result []acpsdk.SessionConfigSelectOption
	for _, group := range *options.Grouped {
		result = append(result, group.Options...)
	}
	return result
}

func blockText(b acpsdk.ContentBlock) string {
	if b.Text != nil {
		return b.Text.Text
	}
	return ""
}

func toolCallContentText(items []acpsdk.ToolCallContent) string {
	var parts []string
	for _, item := range items {
		switch {
		case item.Content != nil:
			if t := blockText(item.Content.Content); t != "" {
				parts = append(parts, t)
			}
		case item.Diff != nil:

			if t := diffBlockText(item.Diff); t != "" {
				parts = append(parts, t)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func toolCallDiffText(items []acpsdk.ToolCallContent) string {
	var parts []string
	for _, item := range items {
		if item.Diff == nil {
			continue
		}
		if text := diffBlockText(item.Diff); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func semanticToolKind(kind acpsdk.ToolKind, items []acpsdk.ToolCallContent) string {
	if kind != "" {
		return string(kind)
	}
	for _, item := range items {
		if item.Diff != nil {
			return string(acpsdk.ToolKindEdit)
		}
	}
	return ""
}

func (a *Agent) workspaceRoot() string {
	if a.workspace == nil {
		return ""
	}
	return a.workspace.RootPath
}

func agentToolLocations(workspaceRoot string, locations []acpsdk.ToolCallLocation) []agent.ToolLocation {
	if len(locations) == 0 {
		return nil
	}
	result := make([]agent.ToolLocation, 0, len(locations))
	for _, location := range locations {
		if location.Path == "" {
			continue
		}
		locationPath := location.Path
		if workspaceRoot != "" && filepath.IsAbs(locationPath) {
			if relative, err := filepath.Rel(workspaceRoot, locationPath); err == nil &&
				relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				locationPath = relative
			}
		}
		mapped := agent.ToolLocation{Path: filepath.ToSlash(locationPath)}
		if location.Line != nil {
			mapped.Line = *location.Line
		}
		result = append(result, mapped)
	}
	return result
}

func diffBlockText(d *acpsdk.ToolCallContentDiff) string {
	old := ""
	if d.OldText != nil {
		old = *d.OldText
	}

	dmp := diffmatchpatch.New()
	c1, c2, lines := dmp.DiffLinesToChars(old, d.NewText)
	diffs := dmp.DiffCharsToLines(dmp.DiffMain(c1, c2, false), lines)

	var b strings.Builder
	if d.Path != "" {
		b.WriteString(d.Path)
		b.WriteByte('\n')
	}
	for _, df := range diffs {
		prefix := " "
		switch df.Type {
		case diffmatchpatch.DiffInsert:
			prefix = "+"
		case diffmatchpatch.DiffDelete:
			prefix = "-"
		}
		for ln := range strings.SplitSeq(strings.TrimSuffix(df.Text, "\n"), "\n") {
			b.WriteString(prefix)
			b.WriteString(ln)
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func rawValueToString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if data, err := json.Marshal(v); err == nil {
		return string(data)
	}
	return fmt.Sprintf("%v", v)
}
