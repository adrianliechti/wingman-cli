package pi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	acpcommon "github.com/adrianliechti/wingman-agent/pkg/acp"
)

type Options struct {
	Path string
	Dir  string
	Env  []string
	Args []string

	Stderr io.Writer

	SessionsDir string
}

type Agent struct {
	conn *acp.AgentSideConnection
	opts Options

	mu       sync.Mutex
	sessions map[acp.SessionId]*session
}

var _ acp.Agent = (*Agent)(nil)

func New(opts Options) *Agent {
	return &Agent{
		opts:     opts,
		sessions: map[acp.SessionId]*session{},
	}
}

func (a *Agent) SetAgentConnection(conn *acp.AgentSideConnection) { a.conn = conn }

func (a *Agent) lookup(id acp.SessionId) *session {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessions[id]
}

func (a *Agent) Close() error {
	a.mu.Lock()
	sessions := a.sessions
	a.sessions = map[acp.SessionId]*session{}
	a.mu.Unlock()
	for _, s := range sessions {
		s.close()
	}
	return nil
}

func (a *Agent) Initialize(context.Context, acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersionNumber,
		AgentInfo: &acp.Implementation{
			Name:    "pi-acp",
			Title:   new("Pi (ACP)"),
			Version: "0.1.0",
		},
		AgentCapabilities: acp.AgentCapabilities{
			LoadSession: a.opts.SessionsDir != "",
			PromptCapabilities: acp.PromptCapabilities{
				Image:           true,
				EmbeddedContext: true,
			},
			SessionCapabilities: acp.SessionCapabilities{
				Close:  &acp.SessionCloseCapabilities{},
				Fork:   a.forkCapability(),
				List:   a.listCapability(),
				Delete: a.deleteCapability(),
			},
		},
	}, nil
}

func (a *Agent) forkCapability() *acp.SessionForkCapabilities {
	if a.opts.SessionsDir == "" {
		return nil
	}
	return &acp.SessionForkCapabilities{}
}

func (a *Agent) listCapability() *acp.SessionListCapabilities {
	if a.opts.SessionsDir == "" {
		return nil
	}
	return &acp.SessionListCapabilities{}
}

func (a *Agent) deleteCapability() *acp.SessionDeleteCapabilities {
	if a.opts.SessionsDir == "" {
		return nil
	}
	return &acp.SessionDeleteCapabilities{}
}

// UnstableDeleteSession is idempotent per the ACP session/delete semantics:
// deleting a session that does not exist (or is already gone) succeeds.
func (a *Agent) UnstableDeleteSession(_ context.Context, params acp.UnstableDeleteSessionRequest) (acp.UnstableDeleteSessionResponse, error) {
	a.disposeSession(params.SessionId)
	if a.opts.SessionsDir != "" {
		if file, ok := findSessionFile(a.opts.SessionsDir, string(params.SessionId)); ok {
			if err := os.Remove(file.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return acp.UnstableDeleteSessionResponse{}, err
			}
		}
	}
	return acp.UnstableDeleteSessionResponse{}, nil
}

func (a *Agent) Authenticate(context.Context, acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}

func (a *Agent) Logout(context.Context, acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, nil
}

func (a *Agent) NewSession(ctx context.Context, params acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	cwd, _, err := acpcommon.NormalizeSessionRoots(params.Cwd, nil)
	if err != nil {
		return acp.NewSessionResponse{}, err
	}

	proc, bridge, err := a.spawnSessionProcess(ctx, cwd, nil, params.McpServers)
	if err != nil {
		return acp.NewSessionResponse{}, err
	}

	modelsData, err := proc.getAvailableModels(ctx)
	if err != nil {
		proc.dispose()
		bridge.cleanup()
		return acp.NewSessionResponse{}, err
	}
	models := parseAvailableModels(modelsData)
	if len(models) == 0 {
		proc.dispose()
		bridge.cleanup()
		return acp.NewSessionResponse{}, acp.NewAuthRequired(nil)
	}

	stateData, err := proc.getState(ctx)
	if err != nil {
		proc.dispose()
		bridge.cleanup()
		return acp.NewSessionResponse{}, err
	}
	state := parseState(stateData)

	id := acp.SessionId(state.SessionID)
	if id == "" {
		id = acp.SessionId(uuid.NewString())
	}

	s := newSession(id, cwd, proc)
	s.cleanup = bridge.cleanup
	s.models = models
	if err := s.refreshConfiguration(ctx); err != nil {
		s.close()
		return acp.NewSessionResponse{}, err
	}

	a.mu.Lock()
	a.sessions[id] = s
	a.mu.Unlock()

	return acp.NewSessionResponse{
		SessionId:     id,
		ConfigOptions: s.configOptions(),
	}, nil
}

func (a *Agent) Prompt(ctx context.Context, params acp.PromptRequest) (acp.PromptResponse, error) {
	s := a.lookup(params.SessionId)
	if s == nil {
		return acp.PromptResponse{}, fmt.Errorf("session %s not found", params.SessionId)
	}
	stop, err := s.runTurn(ctx, a.conn, params.Prompt)
	if err != nil {
		return acp.PromptResponse{}, err
	}
	return acp.PromptResponse{StopReason: stop, UserMessageId: params.MessageId}, nil
}

func (a *Agent) Cancel(_ context.Context, params acp.CancelNotification) error {
	if s := a.lookup(params.SessionId); s != nil {
		s.cancel()
	}
	return nil
}

func (a *Agent) SetSessionMode(context.Context, acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	return acp.SetSessionModeResponse{}, nil
}

func (a *Agent) SetSessionConfigOption(ctx context.Context, params acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	if params.ValueId == nil {
		return acp.SetSessionConfigOptionResponse{}, fmt.Errorf("only value-id config options supported")
	}
	v := params.ValueId
	s := a.lookup(v.SessionId)
	if s == nil {
		return acp.SetSessionConfigOptionResponse{}, fmt.Errorf("session %s not found", v.SessionId)
	}
	value := string(v.Value)

	switch string(v.ConfigId) {
	case modelConfigID:
		provider, modelID, ok := strings.Cut(value, "/")
		if !ok {
			return acp.SetSessionConfigOptionResponse{}, fmt.Errorf("invalid model id %q", value)
		}
		if err := s.proc.setModel(ctx, provider, modelID); err != nil {
			return acp.SetSessionConfigOptionResponse{}, err
		}
		if err := s.refreshConfiguration(ctx); err != nil {
			return acp.SetSessionConfigOptionResponse{}, err
		}

	case effortConfigID:
		s.mu.Lock()
		valid := isThinkingLevel(s.thinkingLevels, value)
		s.mu.Unlock()
		if !valid {
			return acp.SetSessionConfigOptionResponse{}, fmt.Errorf("unknown thinking level %q", value)
		}
		if err := s.proc.setThinkingLevel(ctx, value); err != nil {
			return acp.SetSessionConfigOptionResponse{}, err
		}
		if err := s.refreshConfiguration(ctx); err != nil {
			return acp.SetSessionConfigOptionResponse{}, err
		}

	default:
		return acp.SetSessionConfigOptionResponse{}, fmt.Errorf("unknown configId %q", v.ConfigId)
	}

	return acp.SetSessionConfigOptionResponse{ConfigOptions: s.configOptions()}, nil
}

func (a *Agent) CloseSession(_ context.Context, params acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	a.disposeSession(params.SessionId)
	return acp.CloseSessionResponse{}, nil
}

func (a *Agent) disposeSession(id acp.SessionId) {
	a.mu.Lock()
	s := a.sessions[id]
	delete(a.sessions, id)
	a.mu.Unlock()
	if s != nil {
		s.close()
	}
}

const sessionPageSize = 50

func (a *Agent) ListSessions(_ context.Context, params acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	if a.opts.SessionsDir == "" {
		return acp.ListSessionsResponse{}, nil
	}

	all := listSessionFiles(a.opts.SessionsDir)

	if params.Cwd != nil && *params.Cwd != "" {
		// pi records its own symlink-resolved cwd (e.g. /private/var vs
		// /var on macOS), so compare canonical forms.
		want := canonicalPath(*params.Cwd)
		filtered := all[:0]
		for _, s := range all {
			if canonicalPath(s.Cwd) == want {
				filtered = append(filtered, s)
			}
		}
		all = filtered
	}

	offset := 0
	if params.Cursor != nil {
		if n, err := strconv.Atoi(*params.Cursor); err == nil && n > 0 {
			offset = n
		}
	}
	if offset > len(all) {
		offset = len(all)
	}
	end := min(offset+sessionPageSize, len(all))

	sessions := make([]acp.SessionInfo, 0, end-offset)
	for _, s := range all[offset:end] {
		info := acp.SessionInfo{SessionId: acp.SessionId(s.ID), Cwd: s.Cwd}
		if s.Title != "" {
			title := s.Title
			info.Title = &title
		}
		if s.UpdatedAt != "" {
			updated := s.UpdatedAt
			info.UpdatedAt = &updated
		}
		sessions = append(sessions, info)
	}

	var nextCursor *string
	if end < len(all) {
		c := strconv.Itoa(end)
		nextCursor = &c
	}

	return acp.ListSessionsResponse{Sessions: sessions, NextCursor: nextCursor}, nil
}

func (a *Agent) ResumeSession(context.Context, acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, errors.ErrUnsupported
}

func (a *Agent) LoadSession(ctx context.Context, params acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	if a.opts.SessionsDir == "" {
		return acp.LoadSessionResponse{}, errors.ErrUnsupported
	}
	file, ok := findSessionFile(a.opts.SessionsDir, string(params.SessionId))
	if !ok {
		return acp.LoadSessionResponse{}, fmt.Errorf("unknown session %s", params.SessionId)
	}

	cwd, _, err := acpcommon.NormalizeSessionRoots(params.Cwd, nil)
	if err != nil {
		return acp.LoadSessionResponse{}, err
	}

	a.disposeSession(params.SessionId)

	proc, bridge, err := a.spawnSessionProcess(ctx, cwd, []string{"--session", file.Path}, params.McpServers)
	if err != nil {
		return acp.LoadSessionResponse{}, err
	}

	modelsData, err := proc.getAvailableModels(ctx)
	if err != nil {
		proc.dispose()
		bridge.cleanup()
		return acp.LoadSessionResponse{}, err
	}
	models := parseAvailableModels(modelsData)
	if len(models) == 0 {
		proc.dispose()
		bridge.cleanup()
		return acp.LoadSessionResponse{}, acp.NewAuthRequired(nil)
	}

	s := newSession(params.SessionId, cwd, proc)
	s.cleanup = bridge.cleanup
	s.models = models
	if err := s.refreshConfiguration(ctx); err != nil {
		s.close()
		return acp.LoadSessionResponse{}, err
	}

	a.mu.Lock()
	a.sessions[params.SessionId] = s
	a.mu.Unlock()

	send := func(u acp.SessionUpdate) {
		_ = a.conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: s.id, Update: u})
	}
	if data, err := proc.getMessages(ctx); err == nil {
		replayMessages(send, data)
	}

	return acp.LoadSessionResponse{ConfigOptions: s.configOptions()}, nil
}

func (a *Agent) spawnSessionProcess(ctx context.Context, cwd string, extraArgs []string, servers []acp.McpServer) (*process, *preparedMCPBridge, error) {
	bridge, err := prepareMCPBridge(cwd, a.opts.Env, servers)
	if err != nil {
		return nil, nil, err
	}

	args := append([]string(nil), a.opts.Args...)
	args = append(args, extraArgs...)
	args = append(args, bridge.args...)
	proc, err := spawn(spawnOptions{Path: a.opts.Path, Dir: cwd, Env: bridge.env, Args: args, Stderr: a.opts.Stderr})
	if err != nil {
		bridge.cleanup()
		return nil, nil, err
	}
	if err := bridge.waitReady(ctx, proc); err != nil {
		proc.dispose()
		bridge.cleanup()
		return nil, nil, err
	}
	return proc, bridge, nil
}

func (a *Agent) UnstableForkSession(ctx context.Context, params acp.UnstableForkSessionRequest) (acp.UnstableForkSessionResponse, error) {
	if a.opts.SessionsDir == "" {
		return acp.UnstableForkSessionResponse{}, errors.ErrUnsupported
	}
	file, ok := findSessionFile(a.opts.SessionsDir, string(params.SessionId))
	if !ok {
		return acp.UnstableForkSessionResponse{}, fmt.Errorf("unknown session %s", params.SessionId)
	}
	cwd, _, err := acpcommon.NormalizeSessionRoots(params.Cwd, nil)
	if err != nil {
		return acp.UnstableForkSessionResponse{}, err
	}
	servers, err := stableMCPServers(params.McpServers)
	if err != nil {
		return acp.UnstableForkSessionResponse{}, err
	}

	proc, bridge, err := a.spawnSessionProcess(ctx, cwd, []string{"--session", file.Path}, servers)
	if err != nil {
		return acp.UnstableForkSessionResponse{}, err
	}
	fail := func(err error) (acp.UnstableForkSessionResponse, error) {
		proc.dispose()
		bridge.cleanup()
		return acp.UnstableForkSessionResponse{}, err
	}

	if err := bridge.resetReady(); err != nil {
		return fail(err)
	}
	cloneData, err := proc.clone(ctx)
	if err != nil {
		return fail(err)
	}
	if err := bridge.waitReady(ctx, proc); err != nil {
		return fail(err)
	}
	var cloneResult struct {
		Cancelled bool `json:"cancelled"`
	}
	if json.Unmarshal(cloneData, &cloneResult) != nil {
		return fail(errors.New("pi clone returned an invalid response"))
	}
	if cloneResult.Cancelled {
		return fail(errors.New("pi clone was cancelled"))
	}

	modelsData, err := proc.getAvailableModels(ctx)
	if err != nil {
		return fail(err)
	}
	models := parseAvailableModels(modelsData)
	if len(models) == 0 {
		return fail(acp.NewAuthRequired(nil))
	}
	stateData, err := proc.getState(ctx)
	if err != nil {
		return fail(err)
	}
	state := parseState(stateData)
	id := acp.SessionId(state.SessionID)
	if id == "" || id == params.SessionId {
		return fail(fmt.Errorf("pi clone returned invalid session id %q", id))
	}

	s := newSession(id, cwd, proc)
	s.cleanup = bridge.cleanup
	s.models = models
	if err := s.refreshConfiguration(ctx); err != nil {
		return fail(err)
	}

	a.mu.Lock()
	a.sessions[id] = s
	a.mu.Unlock()
	return acp.UnstableForkSessionResponse{SessionId: id}, nil
}

func stableMCPServers(servers []acp.UnstableMcpServer) ([]acp.McpServer, error) {
	out := make([]acp.McpServer, 0, len(servers))
	for i, server := range servers {
		count := 0
		for _, present := range []bool{server.Stdio != nil, server.Http != nil, server.Sse != nil, server.Acp != nil} {
			if present {
				count++
			}
		}
		if count != 1 {
			return nil, fmt.Errorf("MCP server %d must specify exactly one transport", i+1)
		}
		if server.Stdio == nil {
			return nil, fmt.Errorf("MCP server %d: only stdio transport is supported", i+1)
		}
		out = append(out, acp.McpServer{Stdio: server.Stdio})
	}
	if err := acpcommon.ValidateMCPServers(out, acp.McpCapabilities{}); err != nil {
		return nil, err
	}
	return out, nil
}
