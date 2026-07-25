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
	cleanup    func() error
	steer      func(context.Context, acpsdk.SessionId, []acpsdk.ContentBlock, string) error

	caps acpsdk.AgentCapabilities

	uiMu sync.RWMutex
	ui   code.UI

	configMu   sync.RWMutex
	models     []model.Model
	modelID    string
	effortID   string
	effortOpts []string

	mu       sync.Mutex
	sessions map[string]*sessionState
}

type sessionState struct {
	parent *Agent
	id     acpsdk.SessionId

	mu       sync.Mutex
	messages []agent.Message
	usage    agent.Usage
	inflight *turn
	loaded   bool

	toolCallsMu sync.Mutex
	toolCalls   map[string]toolCall

	modes  []code.Mode
	modeID string

	modelID  string
	effortID string
}

type toolCall struct {
	name string
	args string
}

type turn struct {
	events chan event
	done   chan struct{}
	cancel context.CancelFunc

	mu                sync.Mutex
	emitted           []agent.Message
	ignoreUserUpdates bool
}

func (t *turn) messages() []agent.Message {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]agent.Message(nil), t.emitted...)
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
	msg  agent.Message
	err  error
	done bool
}

const (
	modelConfigID  = "model"
	effortConfigID = "effort"
	initTimeout    = 30 * time.Second
)

func New(ws *code.Workspace, def code.AgentDef) (*Agent, error) {
	if def.Command == "" {
		return nil, fmt.Errorf("agent %q: empty command", def.Name)
	}

	cwd := ws.RootPath
	cmd := exec.Command(def.Command, def.Args...)
	cmd.Dir = cwd
	cmd.Env = os.Environ()
	for k, v := range def.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stderr = os.Stderr

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
	}
	a.conn = acpsdk.NewClientSideConnection(a, stdin, stdout)

	a.conn.SetLogger(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	})))

	initCtx, cancel := context.WithTimeout(context.Background(), initTimeout)
	defer cancel()
	resp, err := a.conn.Initialize(initCtx, acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
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
		a.shutdown()
		return nil, fmt.Errorf("agent %q: initialize: %w", def.Name, err)
	}
	a.caps = resp.AgentCapabilities
	return a, nil
}

func NewInProcess(
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

	a.conn = acpsdk.NewClientSideConnection(a, clientW, clientR)
	a.conn.SetLogger(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	})))

	initCtx, cancel := context.WithTimeout(context.Background(), initTimeout)
	defer cancel()
	resp, err := a.conn.Initialize(initCtx, acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
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
		a.shutdown()
		return nil, fmt.Errorf("agent %q: initialize: %w", name, err)
	}
	a.caps = resp.AgentCapabilities
	return a, nil
}

func (a *Agent) Name() string               { return a.def.Name }
func (a *Agent) Workspace() *code.Workspace { return a.workspace }

func (a *Agent) Models(sessionID string) ([]model.Model, string) {
	override := ""
	if sess := a.session(sessionID); sess != nil {
		sess.mu.Lock()
		override = sess.modelID
		sess.mu.Unlock()
	}
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	out := make([]model.Model, len(a.models))
	copy(out, a.models)
	if override != "" {
		return out, override
	}
	return out, a.modelID
}

func (a *Agent) SetModel(ctx context.Context, sessionID, id string) error {
	return a.setConfig(ctx, sessionID, modelConfigID, id, &a.modelID)
}

func (a *Agent) Effort(sessionID string) (string, []string) {
	override := ""
	if sess := a.session(sessionID); sess != nil {
		sess.mu.Lock()
		override = sess.effortID
		sess.mu.Unlock()
	}
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	opts := make([]string, len(a.effortOpts))
	copy(opts, a.effortOpts)
	if override != "" {
		return override, opts
	}
	return a.effortID, opts
}

func (a *Agent) SetEffort(ctx context.Context, sessionID, value string) error {
	return a.setConfig(ctx, sessionID, effortConfigID, value, &a.effortID)
}

func (a *Agent) setConfig(ctx context.Context, sessionID, configID, value string, defaultField *string) error {
	sess := a.session(sessionID)
	if sess == nil {
		a.configMu.Lock()
		*defaultField = value
		a.configMu.Unlock()
		return nil
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

func (a *Agent) Modes(sessionID string) ([]code.Mode, string) {
	sess := a.session(sessionID)
	if sess == nil {
		return nil, ""
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	out := make([]code.Mode, len(sess.modes))
	copy(out, sess.modes)
	return out, sess.modeID
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
	resp, err := a.conn.ListSessions(ctx, acpsdk.ListSessionsRequest{Cwd: &cwd})
	if err != nil {
		return nil, err
	}
	out := make([]code.SessionInfo, 0, len(resp.Sessions))
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
		out = append(out, info)
	}
	return out, nil
}

func (a *Agent) NewSession(ctx context.Context) (string, error) {
	resp, err := a.conn.NewSession(ctx, acpsdk.NewSessionRequest{
		Cwd:        a.workspace.RootPath,
		McpServers: []acpsdk.McpServer{},
	})
	if err != nil {
		return "", err
	}

	id := string(resp.SessionId)
	sess := &sessionState{
		parent:    a,
		id:        resp.SessionId,
		toolCalls: map[string]toolCall{},
	}
	a.refreshConfig(sess, resp.ConfigOptions)
	sess.applyModes(resp.Modes)
	sess.loaded = true
	a.mu.Lock()
	a.sessions[id] = sess
	a.mu.Unlock()
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
	if !a.caps.LoadSession {
		return func(yield func([]agent.Message, error) bool) {
			yield(nil, errors.ErrUnsupported)
		}
	}

	a.mu.Lock()
	sess, exists := a.sessions[id]
	if !exists {
		sess = &sessionState{
			parent:    a,
			id:        acpsdk.SessionId(id),
			toolCalls: map[string]toolCall{},
		}
		a.sessions[id] = sess
	}
	a.mu.Unlock()

	sess.mu.Lock()
	if sess.loaded {
		snap := append([]agent.Message(nil), sess.messages...)
		sess.mu.Unlock()
		return func(yield func([]agent.Message, error) bool) {
			yield(snap, nil)
		}
	}
	sess.mu.Unlock()

	return func(yield func([]agent.Message, error) bool) {
		loadCtx, cancel := context.WithCancel(ctx)
		t := &turn{
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
	a.mu.Lock()
	delete(a.sessions, id)
	a.mu.Unlock()
	_, err := a.conn.UnstableDeleteSession(ctx, acpsdk.UnstableDeleteSessionRequest{
		SessionId: acpsdk.SessionId(id),
	})
	return err
}

func (a *Agent) Messages(id string) []agent.Message {
	sess := a.session(id)
	if sess == nil {
		return nil
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	out := make([]agent.Message, len(sess.messages))
	copy(out, sess.messages)
	return out
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

		if err == nil && resp.Usage != nil {
			sess.mu.Lock()
			sess.usage = agent.Usage{
				InputTokens:  int64(resp.Usage.InputTokens),
				OutputTokens: int64(resp.Usage.OutputTokens),
			}
			if resp.Usage.CachedReadTokens != nil {
				sess.usage.CachedTokens = int64(*resp.Usage.CachedReadTokens)
			}
			sess.mu.Unlock()
		}
		select {
		case t.events <- event{done: true, err: err}:
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
			sess.toolCallsMu.Lock()
			clear(sess.toolCalls)
			sess.toolCallsMu.Unlock()

			sess.finalizeTurn(t)
		}()
		for {
			select {
			case <-ctx.Done():
				yield(agent.Message{}, ctx.Err())
				return
			case ev := <-t.events:
				if ev.done {
					completed = ev.err == nil ||
						(!errors.Is(ev.err, context.Canceled) && !errors.Is(ev.err, context.DeadlineExceeded))
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
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			for _, sess := range sessions {
				_, _ = a.conn.CloseSession(ctx, acpsdk.CloseSessionRequest{SessionId: sess.id})
			}
			cancel()
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
	if n.Update.ConfigOptionUpdate != nil {
		a.refreshConfig(a.session(string(n.SessionId)), n.Update.ConfigOptionUpdate.ConfigOptions)
		return nil
	}

	if n.Update.CurrentModeUpdate != nil {
		if sess := a.session(string(n.SessionId)); sess != nil {
			sess.mu.Lock()
			sess.modeID = string(n.Update.CurrentModeUpdate.CurrentModeId)
			sess.mu.Unlock()
		}
		return nil
	}

	sess := a.session(string(n.SessionId))
	if sess == nil {
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

func (a *Agent) translateUpdate(sess *sessionState, t *turn, u acpsdk.SessionUpdate) (agent.Message, bool) {
	emit := func(role agent.MessageRole, c agent.Content) agent.Message {
		t.mu.Lock()
		if n := len(t.emitted); n > 0 && t.emitted[n-1].Role == role {
			t.emitted[n-1].Content = append(t.emitted[n-1].Content, c)
		} else {
			t.emitted = append(t.emitted, agent.Message{Role: role, Content: []agent.Content{c}})
		}
		t.mu.Unlock()
		return agent.Message{Role: role, Content: []agent.Content{c}}
	}

	switch {
	case u.UserMessageChunk != nil:
		if t.ignoreUserUpdates {
			return agent.Message{}, false
		}
		text := blockText(u.UserMessageChunk.Content)
		if text == "" {
			return agent.Message{}, false
		}
		return emit(agent.RoleUser, agent.Content{Text: text}), true

	case u.AgentMessageChunk != nil:
		text := blockText(u.AgentMessageChunk.Content)
		if text == "" {
			return agent.Message{}, false
		}
		return emit(agent.RoleAssistant, agent.Content{Text: text}), true

	case u.AgentThoughtChunk != nil:
		text := blockText(u.AgentThoughtChunk.Content)
		if text == "" {
			return agent.Message{}, false
		}
		id := ""
		if u.AgentThoughtChunk.MessageId != nil {
			id = *u.AgentThoughtChunk.MessageId
		}
		return emit(agent.RoleAssistant, agent.Content{Reasoning: &agent.Reasoning{ID: id, Summary: text}}), true

	case u.ToolCall != nil:
		tc := u.ToolCall
		args := rawValueToString(tc.RawInput)

		name := tc.Title
		if name == "" {
			name = string(tc.Kind)
		}
		sess.toolCallsMu.Lock()
		sess.toolCalls[string(tc.ToolCallId)] = toolCall{name: name, args: args}
		sess.toolCallsMu.Unlock()
		return emit(agent.RoleAssistant, agent.Content{ToolCall: &agent.ToolCall{
			ID:   string(tc.ToolCallId),
			Name: name,
			Args: args,
		}}), true

	case u.ToolCallUpdate != nil:
		tu := u.ToolCallUpdate
		if tu.Status == nil {
			return agent.Message{}, false
		}
		status := *tu.Status
		if status != acpsdk.ToolCallStatusCompleted && status != acpsdk.ToolCallStatusFailed {
			return agent.Message{}, false
		}
		sess.toolCallsMu.Lock()
		prior := sess.toolCalls[string(tu.ToolCallId)]
		delete(sess.toolCalls, string(tu.ToolCallId))
		sess.toolCallsMu.Unlock()
		body := toolCallContentText(tu.Content)
		if body == "" && tu.RawOutput != nil {
			body = rawValueToString(tu.RawOutput)
		}
		if body == "" && status == acpsdk.ToolCallStatusFailed {
			body = "tool call failed"
		}
		return emit(agent.RoleAssistant, agent.Content{ToolResult: &agent.ToolResult{
			ID:      string(tu.ToolCallId),
			Name:    prior.name,
			Args:    prior.args,
			Content: body,
		}}), true

	case u.UsageUpdate != nil:
		sess.mu.Lock()
		sess.usage.InputTokens = int64(u.UsageUpdate.Used)
		sess.mu.Unlock()
	}
	return agent.Message{}, false
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

	ui := a.currentUI()
	if ui == nil {
		return selected(p.Options[0].OptionId), nil
	}

	// Preferred: a strict single-choice elicitation so the user can pick any
	// of the offered options (answers, always-allow scopes, selects) instead
	// of a yes/no collapse.
	names := make([]string, len(p.Options))
	for i, o := range p.Options {
		names[i] = o.Name
	}
	res, err := ui.Elicit(code.WithSessionID(ctx, string(p.SessionId)), tool.ElicitRequest{
		Message: permissionMessage(p),
		Fields: []tool.ElicitField{{
			Name:     "choice",
			Type:     "string",
			Required: true,
			Enum:     names,
			Strict:   true,
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
	ui := a.currentUI()
	if ui == nil {
		return acpsdk.UnstableCreateElicitationResponse{Decline: &acpsdk.UnstableCreateElicitationDecline{Action: "decline"}}, nil
	}

	// The SDK's form elicitation carries no session id, but the web UI
	// routes prompts per session — attribute it to the session with the
	// running turn.
	if sid := a.activeSessionID(); sid != "" {
		ctx = code.WithSessionID(ctx, sid)
	}
	res, err := ui.Elicit(ctx, tool.ElicitRequest{
		Message: p.Form.Message,
		Fields:  elicitFieldsFromSchema(p.Form.RequestedSchema),
	})
	if err != nil {
		return cancel, nil
	}
	switch res.Action {
	case tool.ElicitAccept:
		content := res.Content
		if content == nil {
			content = map[string]any{}
		}
		return acpsdk.UnstableCreateElicitationResponse{Accept: &acpsdk.UnstableCreateElicitationAccept{Action: "accept", Content: content}}, nil
	case tool.ElicitDecline:
		return acpsdk.UnstableCreateElicitationResponse{Decline: &acpsdk.UnstableCreateElicitationDecline{Action: "decline"}}, nil
	}
	return cancel, nil
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
	_ = os.MkdirAll(filepath.Dir(abs), 0o755)
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
			start = *p.Line - 1
			if start > len(lines) {
				start = len(lines)
			}
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
	clean := filepath.Clean(p)
	root := filepath.Clean(a.workspace.RootPath)
	rel, err := filepath.Rel(root, clean)
	if err != nil {
		return "", fmt.Errorf("path outside workspace: %s", p)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path outside workspace: %s", p)
	}
	return clean, nil
}

func (a *Agent) CreateTerminal(context.Context, acpsdk.CreateTerminalRequest) (acpsdk.CreateTerminalResponse, error) {
	return acpsdk.CreateTerminalResponse{}, errors.New("terminal not supported")
}
func (a *Agent) KillTerminal(context.Context, acpsdk.KillTerminalRequest) (acpsdk.KillTerminalResponse, error) {
	return acpsdk.KillTerminalResponse{}, nil
}
func (a *Agent) ReleaseTerminal(context.Context, acpsdk.ReleaseTerminalRequest) (acpsdk.ReleaseTerminalResponse, error) {
	return acpsdk.ReleaseTerminalResponse{}, nil
}
func (a *Agent) TerminalOutput(context.Context, acpsdk.TerminalOutputRequest) (acpsdk.TerminalOutputResponse, error) {
	return acpsdk.TerminalOutputResponse{Output: ""}, nil
}
func (a *Agent) WaitForTerminalExit(context.Context, acpsdk.WaitForTerminalExitRequest) (acpsdk.WaitForTerminalExitResponse, error) {
	return acpsdk.WaitForTerminalExitResponse{}, nil
}

func (a *Agent) refreshConfig(sess *sessionState, options []acpsdk.SessionConfigOption) {
	if options == nil {
		return
	}
	modelID, effortID := "", ""
	a.configMu.Lock()
	a.effortID = ""
	a.effortOpts = nil
	for _, opt := range options {
		if opt.Select == nil {
			continue
		}
		switch string(opt.Select.Id) {
		case modelConfigID:
			a.models = a.models[:0]
			if u := opt.Select.Options.Ungrouped; u != nil {
				for _, v := range *u {
					a.models = append(a.models, model.Model{ID: string(v.Value), Name: v.Name})
				}
			}
			a.modelID = string(opt.Select.CurrentValue)
			modelID = a.modelID
		case effortConfigID:
			a.effortID = string(opt.Select.CurrentValue)
			if u := opt.Select.Options.Ungrouped; u != nil {
				for _, v := range *u {
					a.effortOpts = append(a.effortOpts, string(v.Value))
				}
			}
			effortID = a.effortID
		}
	}
	a.configMu.Unlock()

	if sess != nil {
		sess.mu.Lock()
		if modelID != "" {
			sess.modelID = modelID
		}
		if effortID != "" {
			sess.effortID = effortID
		}
		sess.mu.Unlock()
	}
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
		for _, ln := range strings.Split(strings.TrimSuffix(df.Text, "\n"), "\n") {
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
