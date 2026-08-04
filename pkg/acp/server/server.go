package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"github.com/adrianliechti/wingman-agent/pkg/acp"
	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	codeagent "github.com/adrianliechti/wingman-agent/pkg/code/agent"
	"github.com/adrianliechti/wingman-agent/pkg/session"
)

func Run(ctx context.Context, in io.Reader, out io.Writer) error {
	cfg, err := agent.DefaultConfig()
	if err != nil {
		return err
	}
	s := &Server{
		config:     cfg,
		sessions:   map[acpsdk.SessionId]*sessionEntry{},
		workspaces: map[string]*workspaceEntry{},
	}
	s.conn = acpsdk.NewAgentSideConnection(s, out, in)
	s.conn.SetLogger(slog.Default())

	select {
	case <-s.conn.Done():
	case <-ctx.Done():
	}

	s.mu.Lock()
	sessionIDs := slices.Collect(maps.Keys(s.sessions))
	s.mu.Unlock()
	for _, id := range sessionIDs {
		_, _ = s.CloseSession(context.Background(), acpsdk.CloseSessionRequest{SessionId: id})
	}
	return nil
}

type Server struct {
	conn   *acpsdk.AgentSideConnection
	config *agent.Config

	mu         sync.Mutex
	sessions   map[acpsdk.SessionId]*sessionEntry
	workspaces map[string]*workspaceEntry

	formElicitation atomic.Bool
}

type sessionEntry struct {
	id        acpsdk.SessionId
	agent     *codeagent.Agent
	workspace *workspaceEntry
	cancel    context.CancelFunc
	closing   bool
}

type workspaceEntry struct {
	ws    *code.Workspace
	agent *codeagent.Agent
	key   string
	refs  int
	ready chan struct{}
}

func (s *Server) Initialize(_ context.Context, params acpsdk.InitializeRequest) (acpsdk.InitializeResponse, error) {
	capabilities := params.ClientCapabilities.Elicitation
	s.formElicitation.Store(capabilities != nil && capabilities.Form != nil)
	return acpsdk.InitializeResponse{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
		AgentInfo: &acpsdk.Implementation{
			Name:    "wingman-agent",
			Title:   acpsdk.Ptr("Wingman Agent"),
			Version: "0.1.0",
		},
		AgentCapabilities: acpsdk.AgentCapabilities{
			LoadSession:        true,
			PromptCapabilities: acpsdk.PromptCapabilities{Image: true},
			SessionCapabilities: acpsdk.SessionCapabilities{
				List:   &acpsdk.SessionListCapabilities{},
				Resume: &acpsdk.SessionResumeCapabilities{},
				Close:  &acpsdk.SessionCloseCapabilities{},
			},
		},
	}, nil
}

func (s *Server) Authenticate(_ context.Context, _ acpsdk.AuthenticateRequest) (acpsdk.AuthenticateResponse, error) {
	return acpsdk.AuthenticateResponse{}, nil
}

func (s *Server) Logout(_ context.Context, _ acpsdk.LogoutRequest) (acpsdk.LogoutResponse, error) {
	return acpsdk.LogoutResponse{}, nil
}

func (s *Server) Elicit(ctx context.Context, req tool.ElicitRequest) (tool.ElicitResult, error) {
	if result, handled := environmentElicitation(req); handled {
		return result, nil
	}
	if s.conn == nil || !s.formElicitation.Load() || code.SessionIDFromContext(ctx) == "" {
		return elicitationFallback(req), nil
	}

	params := acpsdk.NewUnstableCreateElicitationRequestForm(elicitationSchema(req))
	params.Form.Message = req.Message
	if params.Form.Message == "" {
		params.Form.Message = "Additional input is needed."
	}
	response, err := s.conn.UnstableCreateElicitation(ctx, params)
	if err != nil {
		// The client advertised form support, so an RPC failure is not evidence
		// that the user accepted the schema defaults. Treat it as cancellation;
		// only clients without the capability use the compatibility fallback.
		slog.Debug("ACP form elicitation failed; cancelling", "err", err)
		if ctx.Err() != nil {
			return tool.ElicitResult{Action: tool.ElicitCancel}, ctx.Err()
		}
		return tool.ElicitResult{Action: tool.ElicitCancel}, nil
	}
	switch {
	case response.Accept != nil:
		return tool.ElicitResult{Action: tool.ElicitAccept, Content: response.Accept.Content}, nil
	case response.Decline != nil:
		return tool.ElicitResult{Action: tool.ElicitDecline}, nil
	default:
		return tool.ElicitResult{Action: tool.ElicitCancel}, nil
	}
}

func environmentElicitation(req tool.ElicitRequest) (tool.ElicitResult, bool) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("WINGMAN_ELICITATION"))) {
	case "cancel":
		return tool.ElicitResult{Action: tool.ElicitCancel}, true
	case "accept":
		content := make(map[string]any)
		for _, field := range req.Fields {
			switch {
			case field.Default != nil:
				content[field.Name] = field.Default
			case field.Type == "boolean" && !field.Multiple:
				content[field.Name] = true
			case field.Required:
				return tool.ElicitResult{Action: tool.ElicitCancel}, true
			}
		}
		return tool.ElicitResult{Action: tool.ElicitAccept, Content: content}, true
	default:
		return tool.ElicitResult{}, false
	}
}

func elicitationFallback(req tool.ElicitRequest) tool.ElicitResult {
	content := make(map[string]any)
	for _, field := range req.Fields {
		if field.Default != nil {
			content[field.Name] = field.Default
			continue
		}
		if field.Required {
			return tool.ElicitResult{Action: tool.ElicitCancel}
		}
	}
	return tool.ElicitResult{Action: tool.ElicitAccept, Content: content}
}

func elicitationSchema(req tool.ElicitRequest) acpsdk.UnstableElicitationSchema {
	properties := make(map[string]any, len(req.Fields))
	var required []string
	for _, field := range req.Fields {
		property := map[string]any{}
		fieldType := field.Type
		if fieldType == "" {
			fieldType = "string"
		}
		if field.Multiple {
			property["type"] = "array"
			items := map[string]any{"type": fieldType}
			addElicitationEnum(items, "anyOf", field)
			property["items"] = items
		} else {
			property["type"] = fieldType
			addElicitationEnum(property, "oneOf", field)
		}
		if field.Title != "" {
			property["title"] = field.Title
		}
		if field.Description != "" {
			property["description"] = field.Description
		}
		if field.Default != nil {
			property["default"] = field.Default
		}
		if field.CustomAnswerFor != "" {
			property["_meta"] = map[string]any{
				"_askUserQuestionCustomAnswer": map[string]any{
					"questionId":     field.CustomAnswerFor,
					"isCustomAnswer": true,
				},
			}
		}
		properties[field.Name] = property
		if field.Required {
			required = append(required, field.Name)
		}
	}

	return acpsdk.UnstableElicitationSchema{
		Type:       acpsdk.UnstableElicitationSchemaTypeObject,
		Properties: properties,
		Required:   required,
	}
}

func addElicitationEnum(schema map[string]any, describedKey string, field tool.ElicitField) {
	if len(field.Enum) == 0 {
		return
	}
	described := false
	choices := make([]map[string]any, 0, len(field.Enum))
	for i, value := range field.Enum {
		choice := map[string]any{"const": value, "title": value}
		if i < len(field.EnumDescriptions) && field.EnumDescriptions[i] != "" {
			choice["description"] = field.EnumDescriptions[i]
			described = true
		}
		choices = append(choices, choice)
	}
	if described {
		schema[describedKey] = choices
	} else {
		schema["enum"] = slices.Clone(field.Enum)
	}
}

func (s *Server) Confirm(ctx context.Context, message string) (bool, error) {
	if s.conn == nil {
		return false, nil
	}
	sid := code.SessionIDFromContext(ctx)
	if sid == "" {
		return false, nil
	}

	const allow = acpsdk.PermissionOptionId("allow")
	toolCallID := acpsdk.ToolCallId("permission-" + uuid.NewString())
	start := acpsdk.StartToolCall(
		toolCallID,
		message,
		acpsdk.WithStartKind(acpsdk.ToolKindOther),
		acpsdk.WithStartStatus(acpsdk.ToolCallStatusPending),
	)
	if err := s.conn.SessionUpdate(ctx, acpsdk.SessionNotification{
		SessionId: acpsdk.SessionId(sid),
		Update:    start,
	}); err != nil {
		return false, err
	}

	response, err := s.conn.RequestPermission(ctx, acpsdk.RequestPermissionRequest{
		SessionId: acpsdk.SessionId(sid),
		ToolCall: acpsdk.ToolCallUpdate{
			ToolCallId: toolCallID,
			Title:      acpsdk.Ptr(message),
			Kind:       acpsdk.Ptr(acpsdk.ToolKindOther),
			Status:     acpsdk.Ptr(acpsdk.ToolCallStatusPending),
		},
		Options: []acpsdk.PermissionOption{
			{Kind: acpsdk.PermissionOptionKindAllowOnce, Name: "Allow", OptionId: allow},
			{Kind: acpsdk.PermissionOptionKindRejectOnce, Name: "Reject", OptionId: "reject"},
		},
	})
	if err != nil {
		s.finishPermissionToolCall(ctx, acpsdk.SessionId(sid), toolCallID, acpsdk.ToolCallStatusFailed)
		return false, err
	}
	allowed := response.Outcome.Selected != nil && response.Outcome.Selected.OptionId == allow
	status := acpsdk.ToolCallStatusFailed
	if allowed {
		status = acpsdk.ToolCallStatusCompleted
	}
	if err := s.finishPermissionToolCall(ctx, acpsdk.SessionId(sid), toolCallID, status); err != nil {
		return false, err
	}
	return allowed, nil
}

func (s *Server) finishPermissionToolCall(ctx context.Context, sid acpsdk.SessionId, id acpsdk.ToolCallId, status acpsdk.ToolCallStatus) error {
	// Finish an announced lifecycle even if the permission request consumed the
	// caller's deadline. Keep this cleanup bounded in case the client is gone.
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	return s.conn.SessionUpdate(finishCtx, acpsdk.SessionNotification{
		SessionId: sid,
		Update:    acpsdk.UpdateToolCall(id, acpsdk.WithUpdateStatus(status)),
	})
}

func (s *Server) NewSession(ctx context.Context, params acpsdk.NewSessionRequest) (acpsdk.NewSessionResponse, error) {
	cwd, err := normalizeCwd(params.Cwd)
	if err != nil {
		return acpsdk.NewSessionResponse{}, err
	}
	w, err := s.acquireWorkspace(ctx, cwd)
	if err != nil {
		return acpsdk.NewSessionResponse{}, err
	}

	sid, err := w.agent.NewSession(ctx)
	if err != nil {
		s.releaseWorkspace(w)
		return acpsdk.NewSessionResponse{}, err
	}
	acpSid := acpsdk.SessionId(sid)
	s.registerSession(acpSid, w)

	return acpsdk.NewSessionResponse{
		SessionId:     acpSid,
		Modes:         modeState(w.agent, sid),
		ConfigOptions: sessionConfigOptions(w.agent, sid),
	}, nil
}

func (s *Server) acquireWorkspace(ctx context.Context, cwd string) (*workspaceEntry, error) {
	s.mu.Lock()
	if w, ok := s.workspaces[cwd]; ok {
		w.refs++
		s.mu.Unlock()
		return s.awaitWorkspace(ctx, w)
	}
	s.mu.Unlock()

	ws, err := code.NewWorkspace(cwd)
	if err != nil {
		return nil, err
	}
	wa := codeagent.New(ws, s.config, s)

	s.mu.Lock()
	if existing, ok := s.workspaces[cwd]; ok {

		existing.refs++
		s.mu.Unlock()
		_ = wa.Close()
		ws.Close()
		return s.awaitWorkspace(ctx, existing)
	}
	w := &workspaceEntry{ws: ws, agent: wa, key: cwd, refs: 1, ready: make(chan struct{})}
	s.workspaces[cwd] = w
	s.mu.Unlock()

	ws.WarmUp()
	initCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	if err := ws.InitMCP(initCtx); err != nil {
		slog.Warn("workspace mcp init failed", "cwd", cwd, "err", err)
	}
	wa.FetchModels(initCtx)
	cancel()
	close(w.ready)
	return s.awaitWorkspace(ctx, w)
}

func (s *Server) awaitWorkspace(ctx context.Context, w *workspaceEntry) (*workspaceEntry, error) {
	select {
	case <-w.ready:
		return w, nil
	case <-ctx.Done():
		s.releaseWorkspace(w)
		return nil, ctx.Err()
	}
}

func (s *Server) releaseWorkspace(w *workspaceEntry) {
	s.mu.Lock()
	w.refs--
	if w.refs > 0 {
		s.mu.Unlock()
		return
	}
	delete(s.workspaces, w.key)
	s.mu.Unlock()
	_ = w.agent.Close()
	w.ws.Close()
}

func (s *Server) registerSession(id acpsdk.SessionId, w *workspaceEntry) {
	s.mu.Lock()
	_, exists := s.sessions[id]
	if !exists {
		s.sessions[id] = &sessionEntry{id: id, agent: w.agent, workspace: w}
	}
	s.mu.Unlock()

	if exists {
		s.releaseWorkspace(w)
	}
}

func normalizeCwd(cwd string) (string, error) {
	cwd, _, err := acp.NormalizeSessionRoots(cwd, nil)
	return cwd, err
}

func (s *Server) CloseSession(_ context.Context, params acpsdk.CloseSessionRequest) (acpsdk.CloseSessionResponse, error) {
	s.mu.Lock()
	sess := s.sessions[params.SessionId]
	if sess == nil || sess.closing {
		s.mu.Unlock()
		return acpsdk.CloseSessionResponse{}, nil
	}
	cancel := sess.cancel
	if cancel != nil {
		sess.closing = true
	} else {
		delete(s.sessions, params.SessionId)
	}
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	s.releaseWorkspace(sess.workspace)
	return acpsdk.CloseSessionResponse{}, nil
}

func (s *Server) lookupSession(id acpsdk.SessionId) *sessionEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[id]
}

func (s *Server) retainSession(id acpsdk.SessionId, cancel context.CancelFunc) (*sessionEntry, func(), error) {
	s.mu.Lock()
	sess := s.sessions[id]
	if sess == nil {
		s.mu.Unlock()
		return nil, nil, fmt.Errorf("session %s not found", id)
	}
	if sess.closing {
		s.mu.Unlock()
		return nil, nil, fmt.Errorf("session %s is closing", id)
	}
	if sess.cancel != nil {
		s.mu.Unlock()
		return nil, nil, fmt.Errorf("session %s already has a prompt in progress", id)
	}
	sess.workspace.refs++
	sess.cancel = cancel
	s.mu.Unlock()
	return sess, func() {
		s.mu.Lock()
		if s.sessions[id] == sess {
			if sess.closing {
				delete(s.sessions, id)
			} else {
				sess.cancel = nil
			}
		}
		s.mu.Unlock()
	}, nil
}

func (s *Server) Cancel(_ context.Context, params acpsdk.CancelNotification) error {
	s.mu.Lock()
	var cancel context.CancelFunc
	if sess := s.sessions[params.SessionId]; sess != nil {
		cancel = sess.cancel
	}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (s *Server) Prompt(ctx context.Context, params acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sess, unregister, err := s.retainSession(params.SessionId, cancel)
	if err != nil {
		return acpsdk.PromptResponse{}, err
	}
	defer s.releaseWorkspace(sess.workspace)
	defer unregister()

	defer func() {
		if err := sess.agent.Save(string(sess.id)); err != nil {
			slog.Warn("save session failed", "session", sess.id, "err", err)
		}
	}()

	notify := func(u acpsdk.SessionUpdate) {
		_ = s.conn.SessionUpdate(ctx, acpsdk.SessionNotification{
			SessionId: params.SessionId,
			Update:    u,
		})
	}

	stream, err := sess.agent.Send(ctx, string(sess.id), acp.ContentFromBlocks(params.Prompt))
	if err != nil {
		return acpsdk.PromptResponse{}, err
	}
	for msg, err := range stream {
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return promptResponse(sess, acpsdk.StopReasonCancelled, params.MessageId), nil
			}
			notify(acpsdk.UpdateAgentMessageText("error: " + err.Error()))
			return promptResponse(sess, acpsdk.StopReasonEndTurn, params.MessageId), nil
		}
		for _, c := range msg.Content {
			notifyContent(notify, agent.RoleAssistant, c)
		}
	}
	return promptResponse(sess, acpsdk.StopReasonEndTurn, params.MessageId), nil
}

func promptResponse(sess *sessionEntry, reason acpsdk.StopReason, messageID *string) acpsdk.PromptResponse {
	u := sess.agent.Usage(string(sess.id))
	input := tokenCount(u.InputTokens)
	cached := min(tokenCount(u.CachedTokens), input)
	output := tokenCount(u.OutputTokens)
	usage := &acpsdk.Usage{
		InputTokens:  input - cached,
		OutputTokens: output,
		TotalTokens:  addTokenCounts(input, output),
	}
	if cached > 0 {
		usage.CachedReadTokens = &cached
	}
	return acpsdk.PromptResponse{StopReason: reason, Usage: usage, UserMessageId: messageID}
}

func tokenCount(n int64) int {
	if n <= 0 {
		return 0
	}
	maxInt := int64(^uint(0) >> 1)
	if n > maxInt {
		return int(maxInt)
	}
	return int(n)
}

func addTokenCounts(a, b int) int {
	maxInt := int(^uint(0) >> 1)
	if b > maxInt-a {
		return maxInt
	}
	return a + b
}

func (s *Server) ListSessions(_ context.Context, params acpsdk.ListSessionsRequest) (acpsdk.ListSessionsResponse, error) {
	if params.Cwd == nil || *params.Cwd == "" {
		return acpsdk.ListSessionsResponse{Sessions: []acpsdk.SessionInfo{}}, nil
	}
	cwd, err := normalizeCwd(*params.Cwd)
	if err != nil {
		return acpsdk.ListSessionsResponse{}, err
	}

	infos, err := session.List(code.SessionsDir(cwd))
	if err != nil {
		return acpsdk.ListSessionsResponse{}, err
	}
	out := make([]acpsdk.SessionInfo, 0, len(infos))
	for _, si := range infos {
		info := acpsdk.SessionInfo{
			SessionId: acpsdk.SessionId(si.ID),
			Cwd:       cwd,
		}
		if si.Title != "" {
			t := si.Title
			info.Title = &t
		}
		if !si.UpdatedAt.IsZero() {
			u := si.UpdatedAt.UTC().Format(time.RFC3339)
			info.UpdatedAt = &u
		}
		out = append(out, info)
	}
	return acpsdk.ListSessionsResponse{Sessions: out}, nil
}

func (s *Server) ResumeSession(ctx context.Context, params acpsdk.ResumeSessionRequest) (acpsdk.ResumeSessionResponse, error) {
	w, _, err := s.loadAndAttach(ctx, params.Cwd, params.SessionId)
	if err != nil {
		return acpsdk.ResumeSessionResponse{}, err
	}
	if w == nil {
		return acpsdk.ResumeSessionResponse{}, nil
	}
	return acpsdk.ResumeSessionResponse{
		Modes:         modeState(w.agent, string(params.SessionId)),
		ConfigOptions: sessionConfigOptions(w.agent, string(params.SessionId)),
	}, nil
}

func (s *Server) LoadSession(ctx context.Context, params acpsdk.LoadSessionRequest) (acpsdk.LoadSessionResponse, error) {
	w, messages, err := s.loadAndAttach(ctx, params.Cwd, params.SessionId)
	if err != nil {
		return acpsdk.LoadSessionResponse{}, err
	}
	if w == nil {
		return acpsdk.LoadSessionResponse{}, nil
	}
	s.replayMessages(ctx, params.SessionId, messages)
	return acpsdk.LoadSessionResponse{
		Modes:         modeState(w.agent, string(params.SessionId)),
		ConfigOptions: sessionConfigOptions(w.agent, string(params.SessionId)),
	}, nil
}

func (s *Server) loadAndAttach(ctx context.Context, cwdParam string, id acpsdk.SessionId) (*workspaceEntry, []agent.Message, error) {
	cwd, err := normalizeCwd(cwdParam)
	if err != nil {
		return nil, nil, err
	}
	w, err := s.acquireWorkspace(ctx, cwd)
	if err != nil {
		return nil, nil, err
	}
	if err := w.agent.LoadSession(ctx, string(id)); err != nil {
		s.releaseWorkspace(w)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("session %s not found: %w", id, err)
	}
	s.registerSession(id, w)
	return w, w.agent.Messages(string(id)), nil
}

func (s *Server) replayMessages(ctx context.Context, sid acpsdk.SessionId, messages []agent.Message) {
	notify := func(u acpsdk.SessionUpdate) {
		_ = s.conn.SessionUpdate(ctx, acpsdk.SessionNotification{
			SessionId: sid,
			Update:    u,
		})
	}
	for _, m := range messages {
		if m.Hidden {
			continue
		}
		for _, c := range m.Content {
			notifyContent(notify, m.Role, c)
		}
	}
}

func notifyContent(notify func(acpsdk.SessionUpdate), role agent.MessageRole, c agent.Content) {
	switch {
	case c.ToolCall != nil:
		raw := parseRawInput(c.ToolCall.Args)
		opts := []acpsdk.ToolCallStartOpt{
			acpsdk.WithStartKind(mapKind(c.ToolCall.Name)),
			acpsdk.WithStartStatus(acpsdk.ToolCallStatusInProgress),
			acpsdk.WithStartRawInput(raw),
		}
		if locs := toolLocations(c.ToolCall.Name, raw); len(locs) > 0 {
			opts = append(opts, acpsdk.WithStartLocations(locs))
		}
		notify(acpsdk.StartToolCall(
			acpsdk.ToolCallId(c.ToolCall.ID),
			toolTitle(c.ToolCall.Name, raw),
			opts...,
		))
	case c.ToolResult != nil:
		notify(acpsdk.UpdateToolCall(
			acpsdk.ToolCallId(c.ToolResult.ID),
			acpsdk.WithUpdateStatus(acpsdk.ToolCallStatusCompleted),
			acpsdk.WithUpdateContent([]acpsdk.ToolCallContent{
				acpsdk.ToolContent(acpsdk.TextBlock(c.ToolResult.Content)),
			}),
		))
	case c.Reasoning != nil && c.Reasoning.Summary != "":
		notify(acpsdk.UpdateAgentThoughtText(c.Reasoning.Summary))
	case c.Text != "":
		if role == agent.RoleUser {
			notify(acpsdk.UpdateUserMessageText(c.Text))
		} else {
			notify(acpsdk.UpdateAgentMessageText(c.Text))
		}
	}
}

func (s *Server) SetSessionConfigOption(ctx context.Context, params acpsdk.SetSessionConfigOptionRequest) (acpsdk.SetSessionConfigOptionResponse, error) {
	if params.ValueId == nil {
		return acpsdk.SetSessionConfigOptionResponse{}, fmt.Errorf("expected select value")
	}
	p := params.ValueId
	sess := s.lookupSession(p.SessionId)
	if sess == nil {
		return acpsdk.SetSessionConfigOptionResponse{}, fmt.Errorf("session %s not found", p.SessionId)
	}
	switch string(p.ConfigId) {
	case "model":
		if err := sess.agent.SetModel(ctx, string(p.SessionId), string(p.Value)); err != nil {
			return acpsdk.SetSessionConfigOptionResponse{}, err
		}
	case "effort":
		if err := sess.agent.SetEffort(ctx, string(p.SessionId), string(p.Value)); err != nil {
			return acpsdk.SetSessionConfigOptionResponse{}, err
		}
	default:
		return acpsdk.SetSessionConfigOptionResponse{}, fmt.Errorf("unknown config id: %s", p.ConfigId)
	}

	return acpsdk.SetSessionConfigOptionResponse{
		ConfigOptions: sessionConfigOptions(sess.agent, string(p.SessionId)),
	}, nil
}

func (s *Server) SetSessionMode(ctx context.Context, params acpsdk.SetSessionModeRequest) (acpsdk.SetSessionModeResponse, error) {
	sess := s.lookupSession(params.SessionId)
	if sess == nil {
		return acpsdk.SetSessionModeResponse{}, fmt.Errorf("session %s not found", params.SessionId)
	}
	if err := sess.agent.SetMode(ctx, string(params.SessionId), string(params.ModeId)); err != nil {
		return acpsdk.SetSessionModeResponse{}, err
	}
	return acpsdk.SetSessionModeResponse{}, nil
}

func modeState(a *codeagent.Agent, sid string) *acpsdk.SessionModeState {
	modes, current := a.Modes(sid)
	if len(modes) == 0 {
		return nil
	}
	out := make([]acpsdk.SessionMode, 0, len(modes))
	for _, m := range modes {
		mode := acpsdk.SessionMode{Id: acpsdk.SessionModeId(m.ID), Name: m.Name}
		if m.Description != "" {
			desc := m.Description
			mode.Description = &desc
		}
		out = append(out, mode)
	}
	return &acpsdk.SessionModeState{
		AvailableModes: out,
		CurrentModeId:  acpsdk.SessionModeId(current),
	}
}

func parseRawInput(args string) any {
	if args == "" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(args), &v); err == nil {
		return v
	}
	return args
}

func sessionConfigOptions(a *codeagent.Agent, sid string) []acpsdk.SessionConfigOption {
	return []acpsdk.SessionConfigOption{
		modelConfigOption(a, sid),
		effortConfigOption(a, sid),
	}
}

func modelConfigOption(a *codeagent.Agent, sid string) acpsdk.SessionConfigOption {
	available, current := a.Models(sid)
	opts := make(acpsdk.SessionConfigSelectOptionsUngrouped, 0, len(available))
	foundCurrent := false
	for _, m := range available {
		foundCurrent = foundCurrent || m.ID == current
		opts = append(opts, acpsdk.SessionConfigSelectOption{
			Value: acpsdk.SessionConfigValueId(m.ID),
			Name:  m.Name,
		})
	}
	// Custom gateways can return a model that is not in Wingman's built-in
	// catalog. ACP requires a select's current value to be one of its options.
	if current != "" && !foundCurrent {
		opts = append(opts, acpsdk.SessionConfigSelectOption{
			Value: acpsdk.SessionConfigValueId(current),
			Name:  current,
		})
	}
	return acpsdk.SessionConfigOption{
		Select: &acpsdk.SessionConfigOptionSelect{
			Id:           "model",
			Name:         "Model",
			CurrentValue: acpsdk.SessionConfigValueId(current),
			Options:      acpsdk.SessionConfigSelectOptions{Ungrouped: &opts},
		},
	}
}

func effortConfigOption(a *codeagent.Agent, sid string) acpsdk.SessionConfigOption {
	current, values := a.Effort(sid)
	opts := make(acpsdk.SessionConfigSelectOptionsUngrouped, 0, len(values))
	for _, v := range values {
		opts = append(opts, acpsdk.SessionConfigSelectOption{
			Value: acpsdk.SessionConfigValueId(v),
			Name:  titleCase(v),
		})
	}
	return acpsdk.SessionConfigOption{
		Select: &acpsdk.SessionConfigOptionSelect{
			Id:           "effort",
			Name:         "Effort",
			CurrentValue: acpsdk.SessionConfigValueId(current),
			Options:      acpsdk.SessionConfigSelectOptions{Ungrouped: &opts},
		},
	}
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func toolTitle(name string, raw any) string {
	args, _ := raw.(map[string]any)
	str := func(key string) string {
		if args == nil {
			return ""
		}
		v, _ := args[key].(string)
		return v
	}
	intArg := func(key string) int {
		if args == nil {
			return 0
		}
		switch v := args[key].(type) {
		case float64:
			return int(v)
		case int:
			return v
		}
		return 0
	}
	truncate := func(s string, max int) string {
		if len(s) <= max {
			return s
		}
		return s[:max-3] + "..."
	}

	var detail string
	switch name {
	case "read", "write", "edit":

		detail = str("file_path")
		if detail == "" {
			detail = str("path")
		}
	case "grep":
		if p := str("pattern"); p != "" {
			detail = p
			if path := str("path"); path != "" {
				detail = p + " in " + path
			}
		}
	case "glob":
		if p := str("pattern"); p != "" {
			detail = p
			if path := str("path"); path != "" {
				detail = p + " in " + path
			}
		}
	case "lsp":
		if op := str("operation"); op != "" {
			detail = op
			if fp := str("file_path"); fp != "" {
				if line := intArg("line"); line > 0 {
					detail = fmt.Sprintf("%s %s:%d", op, fp, line)
				} else {
					detail = op + " " + fp
				}
			} else if q := str("query"); q != "" {
				detail = op + " " + q
			}
		}
	case "shell":
		if cmd := str("command"); cmd != "" {
			if desc := str("description"); desc != "" {
				detail = desc
			} else {
				detail = truncate(cmd, 60)
			}
		}
	case "elicit":
		detail = truncate(tool.ElicitHint(args), 80)
	case "agent":

		prompt := str("prompt")
		if agentType := str("agent_type"); agentType != "" && prompt != "" {
			detail = truncate(agentType+": "+prompt, 80)
		} else if prompt != "" {
			detail = truncate(prompt, 60)
		}
	}
	if detail == "" {
		return name
	}
	return name + ": " + strings.TrimSpace(detail)
}

func toolLocations(name string, raw any) []acpsdk.ToolCallLocation {
	args, _ := raw.(map[string]any)
	if args == nil {
		return nil
	}
	var path string
	switch name {
	case "read", "write", "edit":
		path, _ = args["file_path"].(string)
		if path == "" {

			path, _ = args["path"].(string)
		}
	case "lsp":
		path, _ = args["file_path"].(string)
	case "grep", "glob":
		path, _ = args["path"].(string)
	}
	if path == "" {
		return nil
	}
	return []acpsdk.ToolCallLocation{{Path: path}}
}

func mapKind(name string) acpsdk.ToolKind {
	switch name {
	case "read", "lsp":
		return acpsdk.ToolKindRead
	case "grep", "glob":
		return acpsdk.ToolKindSearch
	case "write", "edit":
		return acpsdk.ToolKindEdit
	case "shell":
		return acpsdk.ToolKindExecute
	default:
		return acpsdk.ToolKindOther
	}
}
