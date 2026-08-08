package claude

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/coder/acp-go-sdk"

	acpcommon "github.com/adrianliechti/wingman-agent/pkg/acp"

	claudecli "github.com/adrianliechti/wingman-agent/pkg/external/claude"
)

func binPath() string {
	return claudecli.BinPath()
}

type Options struct {
	Model string

	Effort string

	Cwd string
	Env []string

	Path string

	Stderr io.Writer
}

type Agent struct {
	conn *acp.AgentSideConnection

	mu              sync.Mutex
	sessions        map[acp.SessionId]*session
	formElicitation bool
	planUpdates     bool

	defaultModel  string
	defaultEffort string
	defaultCwd    string
	env           []string
	path          string
	stderr        io.Writer

	modelsMu     sync.Mutex
	modelsLoaded bool
	models       []ModelEntry
	commands     []acp.AvailableCommand
}

var _ acp.Agent = (*Agent)(nil)

func New(opts Options) *Agent {
	model := opts.Model
	if model == "" {
		model = "default"
	}
	path := opts.Path
	if path == "" {
		path = binPath()
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	return &Agent{
		sessions:      make(map[acp.SessionId]*session),
		defaultModel:  model,
		defaultEffort: opts.Effort,
		defaultCwd:    opts.Cwd,
		env:           opts.Env,
		path:          path,
		stderr:        stderr,
	}
}

func (a *Agent) SetAgentConnection(conn *acp.AgentSideConnection) { a.conn = conn }

func (a *Agent) Close() error {
	a.mu.Lock()
	sessions := make([]*session, 0, len(a.sessions))
	for _, s := range a.sessions {
		sessions = append(sessions, s)
	}
	a.sessions = make(map[acp.SessionId]*session)
	a.mu.Unlock()
	for _, s := range sessions {
		s.close()
	}
	return nil
}

func (a *Agent) supportsFormElicitation() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.formElicitation
}

func (a *Agent) supportsPlanUpdates() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.planUpdates
}

func (a *Agent) lookup(id acp.SessionId) *session {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessions[id]
}

func (a *Agent) ensureModels(ctx context.Context) {
	a.modelsMu.Lock()
	defer a.modelsMu.Unlock()
	if a.modelsLoaded {
		return
	}
	models, commands, err := a.fetchModels(ctx)
	if err != nil {
		return
	}
	a.models = models
	a.commands = commands
	a.modelsLoaded = true
}

func (a *Agent) sendAvailableCommands(id acp.SessionId) {
	a.modelsMu.Lock()
	cmds := a.commands
	a.modelsMu.Unlock()
	if len(cmds) == 0 {
		return
	}

	go func() {
		_ = a.conn.SessionUpdate(context.Background(), acp.SessionNotification{
			SessionId: id,
			Update: acp.SessionUpdate{AvailableCommandsUpdate: &acp.SessionAvailableCommandsUpdate{
				SessionUpdate:     "available_commands_update",
				AvailableCommands: cmds,
			}},
		})
	}()
}

func (a *Agent) Initialize(_ context.Context, params acp.InitializeRequest) (acp.InitializeResponse, error) {
	a.mu.Lock()
	a.formElicitation = params.ClientCapabilities.Elicitation != nil && params.ClientCapabilities.Elicitation.Form != nil
	a.planUpdates = params.ClientCapabilities.PlanCapabilities != nil
	a.mu.Unlock()

	title := "Claude (ACP)"
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersionNumber,
		AgentInfo: &acp.Implementation{
			Name:    "claude-acp",
			Title:   &title,
			Version: "0.1.0",
		},
		AgentCapabilities: acp.AgentCapabilities{
			LoadSession: true,
			McpCapabilities: acp.McpCapabilities{
				Http: true,
				Sse:  true,
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
				Fork:                  &acp.SessionForkCapabilities{},
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
	if err := acpcommon.ValidateMCPServers(params.McpServers, acp.McpCapabilities{Http: true, Sse: true}); err != nil {
		return acp.NewSessionResponse{}, err
	}
	cwd, additional, err := acpcommon.NormalizeSessionRoots(params.Cwd, params.AdditionalDirectories)
	if err != nil {
		return acp.NewSessionResponse{}, err
	}
	a.ensureModels(ctx)
	id := acp.SessionId(newUUID())
	modelID, effort := normalizeSessionConfig(a.models, a.defaultModel, a.defaultEffort)
	s := a.newSession(id, cwd, modelID, effort, additional)
	s.mcpServers = params.McpServers
	a.mu.Lock()
	a.sessions[id] = s
	a.mu.Unlock()
	a.sendAvailableCommands(id)

	return acp.NewSessionResponse{
		SessionId:     id,
		Modes:         buildSessionModeState(s.mode),
		ConfigOptions: buildConfigOptions(a.models, s.modelID, s.effort),
	}, nil
}

func (a *Agent) Prompt(ctx context.Context, params acp.PromptRequest) (acp.PromptResponse, error) {
	s := a.lookup(params.SessionId)
	if s == nil {
		return acp.PromptResponse{}, fmt.Errorf("session %s not found", params.SessionId)
	}
	stop, usage, err := s.runTurn(ctx, params.Prompt)
	if err != nil {
		return acp.PromptResponse{}, err
	}
	return acp.PromptResponse{StopReason: stop, Usage: usage, UserMessageId: params.MessageId}, nil
}

func (a *Agent) Cancel(_ context.Context, params acp.CancelNotification) error {
	if s := a.lookup(params.SessionId); s != nil {
		s.cancelTurn()
	}
	return nil
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

func (a *Agent) SetSessionConfigOption(_ context.Context, params acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
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
	defer s.mu.Unlock()
	switch string(v.ConfigId) {
	case modelConfigID:
		m := resolveModel(a.models, value)
		if m == nil && value != s.modelID {
			return acp.SetSessionConfigOptionResponse{}, fmt.Errorf("unknown model %q", value)
		}
		if m != nil {
			s.modelID = m.ID
		}
		s.modelOverride = true
		if m != nil && !isValidEffort(m, s.effort) {
			s.effort = "default"
		}
	case effortConfigID:
		m := findModel(a.models, s.modelID)
		if m == nil || !isValidEffort(m, value) {
			return acp.SetSessionConfigOptionResponse{}, fmt.Errorf("effort %q invalid for model %s", value, s.modelID)
		}
		s.effort = value
	default:
		return acp.SetSessionConfigOptionResponse{}, fmt.Errorf("unknown configId %q", v.ConfigId)
	}
	return acp.SetSessionConfigOptionResponse{
		ConfigOptions: buildConfigOptions(a.models, s.modelID, s.effort),
	}, nil
}

func (a *Agent) CloseSession(_ context.Context, params acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	a.mu.Lock()
	s := a.sessions[params.SessionId]
	delete(a.sessions, params.SessionId)
	a.mu.Unlock()
	if s != nil {
		s.close()
	}
	return acp.CloseSessionResponse{}, nil
}

func (a *Agent) UnstableDeleteSession(_ context.Context, params acp.UnstableDeleteSessionRequest) (acp.UnstableDeleteSessionResponse, error) {
	a.mu.Lock()
	s := a.sessions[params.SessionId]
	delete(a.sessions, params.SessionId)
	a.mu.Unlock()
	if s != nil {
		s.close()
	}
	if err := deleteProjectSession(params.SessionId); err != nil {
		return acp.UnstableDeleteSessionResponse{}, err
	}
	return acp.UnstableDeleteSessionResponse{}, nil
}

func (a *Agent) ListSessions(_ context.Context, params acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	cwd := ""
	if params.Cwd != nil {
		cwd = *params.Cwd
	}
	sessions, err := listProjectSessions(cwd)
	if err != nil {
		return acp.ListSessionsResponse{}, err
	}
	return acp.ListSessionsResponse{Sessions: sessions}, nil
}

func (a *Agent) ResumeSession(ctx context.Context, params acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	if err := acpcommon.ValidateMCPServers(params.McpServers, acp.McpCapabilities{Http: true, Sse: true}); err != nil {
		return acp.ResumeSessionResponse{}, err
	}
	cwd, additional, err := acpcommon.NormalizeSessionRoots(params.Cwd, params.AdditionalDirectories)
	if err != nil {
		return acp.ResumeSessionResponse{}, err
	}
	if !sessionExists(cwd, params.SessionId) {
		return acp.ResumeSessionResponse{}, fmt.Errorf("no on-disk session %s for cwd %s", params.SessionId, cwd)
	}
	a.ensureModels(ctx)
	s := a.adoptSession(params.SessionId, cwd, additional, params.McpServers, string(params.SessionId), false)
	a.sendAvailableCommands(params.SessionId)
	return acp.ResumeSessionResponse{
		Modes:         buildSessionModeState(s.mode),
		ConfigOptions: buildConfigOptions(a.models, s.modelID, s.effort),
	}, nil
}

func (a *Agent) LoadSession(ctx context.Context, params acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	if err := acpcommon.ValidateMCPServers(params.McpServers, acp.McpCapabilities{Http: true, Sse: true}); err != nil {
		return acp.LoadSessionResponse{}, err
	}
	cwd, additional, err := acpcommon.NormalizeSessionRoots(params.Cwd, params.AdditionalDirectories)
	if err != nil {
		return acp.LoadSessionResponse{}, err
	}
	if !sessionExists(cwd, params.SessionId) {
		return acp.LoadSessionResponse{}, fmt.Errorf("no on-disk session %s for cwd %s", params.SessionId, cwd)
	}
	a.ensureModels(ctx)
	s := a.adoptSession(params.SessionId, cwd, additional, params.McpServers, string(params.SessionId), false)
	if err := replayHistory(ctx, a.conn, params.SessionId, cwd); err != nil {
		return acp.LoadSessionResponse{}, fmt.Errorf("replay history: %w", err)
	}
	a.sendAvailableCommands(params.SessionId)
	return acp.LoadSessionResponse{
		Modes:         buildSessionModeState(s.mode),
		ConfigOptions: buildConfigOptions(a.models, s.modelID, s.effort),
	}, nil
}

func (a *Agent) UnstableForkSession(_ context.Context, params acp.UnstableForkSessionRequest) (acp.UnstableForkSessionResponse, error) {
	cwd, additional, err := acpcommon.NormalizeSessionRoots(params.Cwd, params.AdditionalDirectories)
	if err != nil {
		return acp.UnstableForkSessionResponse{}, err
	}
	newID := acp.SessionId(newUUID())
	a.adoptSession(newID, cwd, additional, nil, string(params.SessionId), true)

	return acp.UnstableForkSessionResponse{SessionId: newID}, nil
}

func (a *Agent) adoptSession(id acp.SessionId, cwd string, additionalDirs []string, mcpServers []acp.McpServer, resumeFrom string, fork bool) *session {
	modelID, effort := normalizeSessionConfig(a.models, a.defaultModel, a.defaultEffort)
	s := a.newSession(id, cwd, modelID, effort, additionalDirs)
	if resumeFrom != "" && (a.defaultModel == "" || a.defaultModel == "default") {
		path := filepath.Join(projectDirFor(cwd), resumeFrom+".jsonl")
		if live := scanSessionModel(path); live != "" {
			if m := resolveResumedModel(a.models, live); m != nil {
				s.modelID = m.ID
			} else {
				s.modelID = live
			}
			s.modelOverride = false
		}
	}
	s.modelID, s.effort = normalizeSessionConfig(a.models, s.modelID, s.effort)
	s.mcpServers = mcpServers
	s.resumeFrom = resumeFrom
	s.forkOnResume = fork
	a.mu.Lock()
	a.sessions[id] = s
	a.mu.Unlock()
	return s
}

func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {

		return "00000000-0000-4000-8000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	)
}
