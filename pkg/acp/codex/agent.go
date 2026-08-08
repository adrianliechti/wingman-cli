package codex

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"

	acpcommon "github.com/adrianliechti/wingman-agent/pkg/acp"
)

type Agent struct {
	conn  *acp.AgentSideConnection
	codex *codexClient

	cmd       *exec.Cmd
	stdin     io.WriteCloser
	closeOnce sync.Once
	closed    chan struct{}

	mu       sync.Mutex
	sessions map[acp.SessionId]*session

	defaultModel  string
	defaultEffort string

	models []modelEntry

	clientCapabilities acp.ClientCapabilities
}

var _ acp.Agent = (*Agent)(nil)

func newAgent(codex *codexClient, model, effort string) *Agent {
	return &Agent{
		codex:         codex,
		sessions:      make(map[acp.SessionId]*session),
		closed:        make(chan struct{}),
		defaultModel:  model,
		defaultEffort: effort,
	}
}

func (a *Agent) SetAgentConnection(conn *acp.AgentSideConnection) { a.conn = conn }

func (a *Agent) lookup(id acp.SessionId) *session {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessions[id]
}

func (a *Agent) loadModels(ctx context.Context) {
	var all []codexModel
	var cursor *string
	for {
		resp, err := a.codex.modelList(ctx, modelListParams{Cursor: cursor})
		if err != nil {
			return
		}
		all = append(all, resp.Data...)
		if resp.NextCursor == nil || *resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor
	}
	a.models = modelsFromCodex(all)
}

// resumeModelProvider returns the currently configured provider so resumed
// threads do not silently switch to the provider persisted in their rollout.
func (a *Agent) resumeModelProvider(ctx context.Context) string {
	resp, err := a.codex.configRead(ctx, configReadParams{})
	if err != nil {
		return ""
	}
	if mp, ok := resp.Config["model_provider"].(string); ok {
		return mp
	}
	return ""
}

func (a *Agent) Initialize(ctx context.Context, req acp.InitializeRequest) (acp.InitializeResponse, error) {
	a.clientCapabilities = req.ClientCapabilities
	if err := a.codex.initialize(ctx, initializeParams{
		ClientInfo: clientInfo{Name: "codex-acp", Title: "Codex (ACP)", Version: "0.1.0"},
	}); err != nil {
		return acp.InitializeResponse{}, fmt.Errorf("codex initialize: %w", err)
	}
	a.loadModels(ctx)

	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersionNumber,
		AgentInfo: &acp.Implementation{
			Name:    "codex-acp",
			Title:   new("Codex (ACP)"),
			Version: "0.1.0",
		},
		AgentCapabilities: acp.AgentCapabilities{
			LoadSession: true,
			McpCapabilities: acp.McpCapabilities{
				Http: true,
			},
			PromptCapabilities: acp.PromptCapabilities{
				Image:           true,
				EmbeddedContext: true,
			},
			SessionCapabilities: acp.SessionCapabilities{
				AdditionalDirectories: &acp.SessionAdditionalDirectoriesCapabilities{},
				Close:                 &acp.SessionCloseCapabilities{},
				List:                  &acp.SessionListCapabilities{},
				Resume:                &acp.SessionResumeCapabilities{},
				Delete:                &acp.SessionDeleteCapabilities{},
			},
		},
	}, nil
}

func (a *Agent) Authenticate(context.Context, acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}

func (a *Agent) Logout(context.Context, acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, nil
}

func (a *Agent) NewSession(ctx context.Context, params acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	if err := acpcommon.ValidateMCPServers(params.McpServers, acp.McpCapabilities{Http: true}); err != nil {
		return acp.NewSessionResponse{}, err
	}
	cwd, additional, err := acpcommon.NormalizeSessionRoots(params.Cwd, params.AdditionalDirectories)
	if err != nil {
		return acp.NewSessionResponse{}, err
	}
	startParams := threadStartParams{Cwd: cwd, Config: sessionConfig(cwd, additional, params.McpServers)}
	if a.defaultModel != "" && a.defaultModel != "default" {
		startParams.Model = a.defaultModel
	}

	resp, err := a.codex.threadStart(ctx, startParams)
	if err != nil {
		return acp.NewSessionResponse{}, fmt.Errorf("thread/start: %w", err)
	}
	if resp.Thread.ID == "" {
		return acp.NewSessionResponse{}, fmt.Errorf("codex returned empty thread id")
	}

	s := a.registerSession(acp.SessionId(resp.Thread.ID), resp.Model, derefEffort(resp.ReasoningEffort), additional)
	a.sendAvailableCommands(ctx, s.id)
	return acp.NewSessionResponse{
		SessionId:     s.id,
		Modes:         buildSessionModeState(s.mode),
		ConfigOptions: buildConfigOptions(a.models, s.modelID, s.effort, s.collaborationMode),
	}, nil
}

func (a *Agent) registerSession(id acp.SessionId, model, effort string, additionalDirectories []string) *session {
	if model == "" {
		model = a.defaultModel
	}
	if effort == "" {
		effort = a.defaultEffort
	}
	model, effort = normalizeSessionConfig(a.models, model, effort)
	s := newSession(id, model, effort, additionalDirectories)
	a.mu.Lock()
	a.sessions[id] = s
	a.mu.Unlock()
	return s
}

func (a *Agent) sendAvailableCommands(ctx context.Context, id acp.SessionId) {
	if a.conn == nil {
		return
	}
	_ = a.conn.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: id,
		Update: acp.SessionUpdate{AvailableCommandsUpdate: &acp.SessionAvailableCommandsUpdate{
			SessionUpdate: "available_commands_update",
			AvailableCommands: []acp.AvailableCommand{
				{Name: "plan", Description: "Toggle plan mode"},
			},
		}},
	})
}

func derefEffort(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func sessionConfig(cwd string, additionalDirectories []string, servers []acp.McpServer) map[string]any {
	cfg := map[string]any{}
	projects := map[string]any{}
	for _, root := range append([]string{cwd}, additionalDirectories...) {
		if root != "" {
			projects[root] = map[string]any{"trust_level": "trusted"}
		}
	}
	if len(projects) > 0 {
		cfg["projects"] = projects
	}
	if len(additionalDirectories) > 0 {
		cfg["sandbox_workspace_write"] = map[string]any{"writable_roots": additionalDirectories}
	}
	if mcp := mcpServersConfig(servers); len(mcp) > 0 {
		cfg["mcp_servers"] = mcp
	}
	if len(cfg) == 0 {
		return nil
	}
	return cfg
}

func mcpServersConfig(servers []acp.McpServer) map[string]any {
	out := map[string]any{}
	for _, s := range servers {
		switch {
		case s.Stdio != nil:
			env := map[string]string{}
			for _, e := range s.Stdio.Env {
				env[e.Name] = e.Value
			}
			out[s.Stdio.Name] = map[string]any{"command": s.Stdio.Command, "args": s.Stdio.Args, "env": env}
		case s.Http != nil:
			headers := map[string]string{}
			for _, h := range s.Http.Headers {
				headers[h.Name] = h.Value
			}
			out[s.Http.Name] = map[string]any{"url": s.Http.Url, "http_headers": headers}
		}
	}
	return out
}

func (a *Agent) Prompt(ctx context.Context, params acp.PromptRequest) (acp.PromptResponse, error) {
	s := a.lookup(params.SessionId)
	if s == nil {
		return acp.PromptResponse{}, fmt.Errorf("session %s not found", params.SessionId)
	}
	if isPlanCommand(params.Prompt) {
		if err := a.togglePlanMode(ctx, s); err != nil {
			return acp.PromptResponse{}, err
		}
		return acp.PromptResponse{
			StopReason:    acp.StopReasonEndTurn,
			UserMessageId: params.MessageId,
		}, nil
	}
	stop, usage, err := s.runTurn(ctx, a.conn, a.codex, a.clientCapabilities, a.models, params.Prompt)
	if err != nil {
		return acp.PromptResponse{}, err
	}
	return acp.PromptResponse{StopReason: stop, Usage: usage, UserMessageId: params.MessageId}, nil
}

func isPlanCommand(prompt []acp.ContentBlock) bool {
	return len(prompt) == 1 && prompt[0].Text != nil && strings.TrimSpace(prompt[0].Text.Text) == "/plan"
}

func (a *Agent) togglePlanMode(ctx context.Context, s *session) error {
	s.mu.Lock()
	modelID := s.modelID
	effort := s.effort
	next := planCollaborationMode
	if s.collaborationMode == planCollaborationMode {
		next = defaultCollaborationMode
	}
	s.mu.Unlock()

	if err := a.codex.threadSettingsUpdate(ctx, newThreadSettingsUpdate(string(s.id), modelID, effort, next)); err != nil {
		return fmt.Errorf("thread/settings/update: %w", err)
	}
	s.mu.Lock()
	s.collaborationMode = next
	s.mu.Unlock()
	if a.conn != nil {
		_ = a.conn.SessionUpdate(ctx, acp.SessionNotification{
			SessionId: s.id,
			Update: acp.SessionUpdate{ConfigOptionUpdate: &acp.SessionConfigOptionUpdate{
				SessionUpdate: "config_option_update",
				ConfigOptions: buildConfigOptions(a.models, modelID, effort, next),
			}},
		})
		label := "Plan mode enabled."
		if next == defaultCollaborationMode {
			label = "Plan mode disabled."
		}
		_ = a.conn.SessionUpdate(ctx, acp.SessionNotification{
			SessionId: s.id,
			Update:    acp.UpdateAgentMessageText(label),
		})
	}
	return nil
}

func (a *Agent) Steer(ctx context.Context, sessionID acp.SessionId, prompt []acp.ContentBlock, messageID string) error {
	s := a.lookup(sessionID)
	if s == nil {
		return fmt.Errorf("session %s not found", sessionID)
	}
	return s.steer(ctx, a.codex, prompt, messageID)
}

func (a *Agent) Cancel(ctx context.Context, params acp.CancelNotification) error {
	if s := a.lookup(params.SessionId); s != nil {
		a.interruptSession(ctx, s)
	}
	return nil
}

func (a *Agent) interruptSession(ctx context.Context, s *session) {
	interruptCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	s.interrupt(interruptCtx, a.codex)
}

func (a *Agent) SetSessionMode(_ context.Context, params acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	s := a.lookup(params.SessionId)
	if s == nil {
		return acp.SetSessionModeResponse{}, fmt.Errorf("session %s not found", params.SessionId)
	}
	id := string(params.ModeId)
	if findMode(id) == nil {
		return acp.SetSessionModeResponse{}, fmt.Errorf("unknown mode %q", id)
	}
	s.mu.Lock()
	s.mode = id
	s.mu.Unlock()
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

	s.mu.Lock()
	modelID := s.modelID
	effort := s.effort
	collaborationMode := s.collaborationMode
	s.mu.Unlock()

	switch string(v.ConfigId) {
	case modelConfigID:
		m := findModel(a.models, value)
		if m == nil {
			return acp.SetSessionConfigOptionResponse{}, fmt.Errorf("unknown model %q", value)
		}
		modelID = value
		if !isValidEffort(m, effort) {
			effort = "default"
		}
	case effortConfigID:
		m := findModel(a.models, modelID)
		if m == nil || !isValidEffort(m, value) {
			return acp.SetSessionConfigOptionResponse{}, fmt.Errorf("effort %q invalid for model %s", value, modelID)
		}
		effort = value
	case collaborationModeConfigID:
		if !isValidCollaborationMode(value) {
			return acp.SetSessionConfigOptionResponse{}, fmt.Errorf("unknown collaboration mode %q", value)
		}
		collaborationMode = value
	default:
		return acp.SetSessionConfigOptionResponse{}, fmt.Errorf("unknown configId %q", v.ConfigId)
	}

	if collaborationMode == planCollaborationMode || string(v.ConfigId) == collaborationModeConfigID {
		if err := a.codex.threadSettingsUpdate(ctx, newThreadSettingsUpdate(string(s.id), modelID, effort, collaborationMode)); err != nil {
			return acp.SetSessionConfigOptionResponse{}, fmt.Errorf("thread/settings/update: %w", err)
		}
	}
	s.mu.Lock()
	s.modelID = modelID
	s.effort = effort
	s.collaborationMode = collaborationMode
	s.mu.Unlock()
	return acp.SetSessionConfigOptionResponse{
		ConfigOptions: buildConfigOptions(a.models, modelID, effort, collaborationMode),
	}, nil
}

func newThreadSettingsUpdate(threadID, modelID, effort, collaborationMode string) threadSettingsUpdateParams {
	var reasoningEffort *string
	if effort != "" && effort != "default" {
		reasoningEffort = &effort
	}
	return threadSettingsUpdateParams{
		ThreadID: threadID,
		CollaborationMode: codexCollaborationMode{
			Mode: collaborationMode,
			Settings: collaborationModeSettings{
				Model:           modelID,
				ReasoningEffort: reasoningEffort,
			},
		},
	}
}

func (a *Agent) CloseSession(ctx context.Context, params acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	a.mu.Lock()
	s := a.sessions[params.SessionId]
	delete(a.sessions, params.SessionId)
	a.mu.Unlock()
	if s != nil {
		a.interruptSession(ctx, s)
	}
	_ = a.codex.threadUnsubscribe(ctx, threadUnsubscribeParams{ThreadID: string(params.SessionId)})
	return acp.CloseSessionResponse{}, nil
}

func (a *Agent) UnstableDeleteSession(ctx context.Context, params acp.UnstableDeleteSessionRequest) (acp.UnstableDeleteSessionResponse, error) {
	a.mu.Lock()
	s := a.sessions[params.SessionId]
	delete(a.sessions, params.SessionId)
	a.mu.Unlock()
	if s != nil {
		a.interruptSession(ctx, s)
	}
	_ = a.codex.threadUnsubscribe(ctx, threadUnsubscribeParams{ThreadID: string(params.SessionId)})
	if err := a.codex.threadArchive(ctx, threadArchiveParams{ThreadID: string(params.SessionId)}); err != nil {
		// ACP session/delete is idempotent: deleting an unknown or
		// already-archived thread succeeds.
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "no rollout found") || strings.Contains(msg, "not found") {
			return acp.UnstableDeleteSessionResponse{}, nil
		}
		return acp.UnstableDeleteSessionResponse{}, fmt.Errorf("thread/archive: %w", err)
	}
	return acp.UnstableDeleteSessionResponse{}, nil
}

func (a *Agent) ListSessions(ctx context.Context, params acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	lp := threadListParams{
		Cursor: params.Cursor,
		SourceKinds: []string{
			"cli", "vscode", "exec", "appServer",
			"subAgent", "subAgentReview", "subAgentCompact",
			"subAgentThreadSpawn", "subAgentOther", "unknown",
		},
	}
	if params.Cwd != nil && *params.Cwd != "" {
		lp.Cwd = *params.Cwd
	}
	resp, err := a.codex.threadList(ctx, lp)
	if err != nil {
		return acp.ListSessionsResponse{}, fmt.Errorf("thread/list: %w", err)
	}

	sessions := make([]acp.SessionInfo, 0, len(resp.Data))
	for _, t := range resp.Data {
		sessions = append(sessions, acp.SessionInfo{
			SessionId: acp.SessionId(t.ID),
			Cwd:       t.Cwd,
			Title:     sessionTitle(t),
			UpdatedAt: sessionUpdatedAt(t.UpdatedAt),
		})
	}
	return acp.ListSessionsResponse{Sessions: sessions, NextCursor: resp.NextCursor}, nil
}

func sessionTitle(t threadSummary) *string {
	if t.Name != nil && *t.Name != "" {
		return t.Name
	}
	if t.Preview != "" {
		p := t.Preview
		return &p
	}
	return nil
}

func sessionUpdatedAt(unix int64) *string {
	if unix == 0 {
		return nil
	}
	s := time.Unix(unix, 0).UTC().Format(time.RFC3339)
	return &s
}

func (a *Agent) ResumeSession(ctx context.Context, params acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	if err := acpcommon.ValidateMCPServers(params.McpServers, acp.McpCapabilities{Http: true}); err != nil {
		return acp.ResumeSessionResponse{}, err
	}
	cwd, additional, err := acpcommon.NormalizeSessionRoots(params.Cwd, params.AdditionalDirectories)
	if err != nil {
		return acp.ResumeSessionResponse{}, err
	}
	resp, err := a.codex.threadResume(ctx, threadResumeParams{
		ThreadID:      string(params.SessionId),
		Cwd:           cwd,
		ModelProvider: a.resumeModelProvider(ctx),
		Config:        sessionConfig(cwd, additional, params.McpServers),
	})
	if err != nil {
		return acp.ResumeSessionResponse{}, fmt.Errorf("thread/resume: %w", err)
	}
	s := a.registerSession(params.SessionId, resp.Model, derefEffort(resp.ReasoningEffort), additional)
	a.sendAvailableCommands(ctx, s.id)
	return acp.ResumeSessionResponse{
		Modes:         buildSessionModeState(s.mode),
		ConfigOptions: buildConfigOptions(a.models, s.modelID, s.effort, s.collaborationMode),
	}, nil
}

func (a *Agent) LoadSession(ctx context.Context, params acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	if err := acpcommon.ValidateMCPServers(params.McpServers, acp.McpCapabilities{Http: true}); err != nil {
		return acp.LoadSessionResponse{}, err
	}
	cwd, additional, err := acpcommon.NormalizeSessionRoots(params.Cwd, params.AdditionalDirectories)
	if err != nil {
		return acp.LoadSessionResponse{}, err
	}
	resp, err := a.codex.threadResume(ctx, threadResumeParams{
		ThreadID:      string(params.SessionId),
		Cwd:           cwd,
		ModelProvider: a.resumeModelProvider(ctx),
		Config:        sessionConfig(cwd, additional, params.McpServers),
	})
	if err != nil {
		return acp.LoadSessionResponse{}, fmt.Errorf("thread/resume: %w", err)
	}
	s := a.registerSession(params.SessionId, resp.Model, derefEffort(resp.ReasoningEffort), additional)
	a.sendAvailableCommands(ctx, s.id)

	thread := resp.Thread
	if read, err := a.codex.threadRead(ctx, threadReadParams{ThreadID: string(params.SessionId), IncludeTurns: true}); err == nil {
		thread = read.Thread
	}

	outputs := rolloutCommandOutputs(string(params.SessionId), threadPath(thread))
	streamThreadHistory(ctx, a.conn, s.id, thread.Turns, outputs, a.clientCapabilities.PlanCapabilities != nil)
	return acp.LoadSessionResponse{
		Modes:         buildSessionModeState(s.mode),
		ConfigOptions: buildConfigOptions(a.models, s.modelID, s.effort, s.collaborationMode),
	}, nil
}

func threadPath(t threadInfo) string {
	if t.Path != nil {
		return *t.Path
	}
	return ""
}
