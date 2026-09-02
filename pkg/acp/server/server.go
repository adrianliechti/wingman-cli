package server

import (
	"context"
	"crypto/sha256"
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
	"github.com/adrianliechti/wingman-agent/pkg/mcp"
	"github.com/adrianliechti/wingman-agent/pkg/session"
)

func Run(ctx context.Context, in io.Reader, out io.Writer) error {
	cfg, err := agent.DefaultConfig()
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = cfg.Telemetry.Shutdown(shutdownCtx)
		cancel()
	}()
	s := &Server{
		config:      cfg,
		sessions:    map[acpsdk.SessionId]*sessionEntry{},
		sessionDirs: map[acpsdk.SessionId]string{},
		workspaces:  map[string]*workspaceEntry{},
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

var serverMCPCapabilities = acpsdk.McpCapabilities{Http: true, Sse: true}

type Server struct {
	conn   *acpsdk.AgentSideConnection
	config *agent.Config

	mu          sync.Mutex
	sessions    map[acpsdk.SessionId]*sessionEntry
	sessionDirs map[acpsdk.SessionId]string
	workspaces  map[string]*workspaceEntry

	formElicitation atomic.Bool
}

type sessionEntry struct {
	id         acpsdk.SessionId
	agent      *codeagent.Agent
	workspace  *workspaceEntry
	cancel     context.CancelFunc
	promptDone chan struct{}
	closing    bool
	lastTitle  string
}

type workspaceEntry struct {
	ws         *code.Workspace
	agent      *codeagent.Agent
	key        string
	refs       int
	ready      chan struct{}
	err        error
	toolUpdate *code.ManagedToolsUpdate
}

func (s *Server) Initialize(_ context.Context, params acpsdk.InitializeRequest) (acpsdk.InitializeResponse, error) {
	capabilities := params.ClientCapabilities.Elicitation
	s.formElicitation.Store(capabilities != nil && capabilities.Form != nil)
	return acpsdk.InitializeResponse{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
		AgentInfo: &acpsdk.Implementation{
			Name:    "wingman-agent",
			Title:   new("Wingman Agent"),
			Version: "0.1.0",
		},
		AgentCapabilities: acpsdk.AgentCapabilities{
			LoadSession:     true,
			McpCapabilities: serverMCPCapabilities,
			PromptCapabilities: acpsdk.PromptCapabilities{
				Image:           true,
				EmbeddedContext: true,
			},
			SessionCapabilities: acpsdk.SessionCapabilities{
				List:   &acpsdk.SessionListCapabilities{},
				Resume: &acpsdk.SessionResumeCapabilities{},
				Close:  &acpsdk.SessionCloseCapabilities{},
				Delete: &acpsdk.SessionDeleteCapabilities{},
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
			Title:      new(message),
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
	w, err := s.acquireWorkspace(ctx, cwd, params.McpServers)
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

func (s *Server) acquireWorkspace(ctx context.Context, cwd string, requestedServers []acpsdk.McpServer) (*workspaceEntry, error) {
	requested, err := requestedMCPConfig(requestedServers)
	if err != nil {
		return nil, err
	}
	key := workspaceKey(cwd, requested)

	s.mu.Lock()
	if w, ok := s.workspaces[key]; ok {
		w.refs++
		s.mu.Unlock()
		return s.awaitWorkspace(ctx, w)
	}
	s.mu.Unlock()

	ws, err := code.NewWorkspace(cwd)
	if err != nil {
		return nil, err
	}
	if len(requested) > 0 {
		if ws.MCP == nil {
			ws.MCP = mcp.NewManager(&mcp.Config{Servers: map[string]mcp.ServerConfig{}})
			ws.MCP.Dir = cwd
		}
		maps.Copy(ws.MCP.Servers, requested)
	}
	wa := codeagent.New(ws, s.config, s)

	s.mu.Lock()
	if existing, ok := s.workspaces[key]; ok {

		existing.refs++
		s.mu.Unlock()
		_ = wa.Close()
		ws.Close()
		return s.awaitWorkspace(ctx, existing)
	}
	w := &workspaceEntry{ws: ws, agent: wa, key: key, refs: 1, ready: make(chan struct{})}
	s.workspaces[key] = w
	s.mu.Unlock()

	ws.WarmUp()
	w.toolUpdate = ws.StartManagedToolsUpdate(context.Background(), code.ManagedLSPTools)
	go func() {
		if _, err := w.toolUpdate.Wait(); err != nil {
			slog.Warn("managed tools update failed", "cwd", cwd, "err", err)
		}
	}()
	mcpCtx, cancelMCP := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	if err := ws.InitMCP(mcpCtx); err != nil {
		// A local stdio server that won't start is a misconfiguration worth
		// failing on; a remote one is usually transient and must not take the
		// whole session down with it.
		var missing []string
		for _, name := range missingRequestedMCPServers(requested, ws.MCP.Sessions()) {
			if requested[name].Transport == "" {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			w.err = fmt.Errorf("initialize requested MCP servers %s: %w", strings.Join(missing, ", "), err)
		} else {
			slog.Warn("workspace mcp init failed", "cwd", cwd, "err", err)
		}
	}
	cancelMCP()
	if w.err == nil {
		modelCtx, cancelModels := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		wa.FetchModels(modelCtx)
		cancelModels()
	}
	close(w.ready)
	return s.awaitWorkspace(ctx, w)
}

func requestedMCPConfig(servers []acpsdk.McpServer) (map[string]mcp.ServerConfig, error) {
	if err := acp.ValidateMCPServers(servers, serverMCPCapabilities); err != nil {
		return nil, err
	}
	result := make(map[string]mcp.ServerConfig, len(servers))
	for _, server := range servers {
		switch {
		case server.Stdio != nil:
			stdio := server.Stdio
			env := make(map[string]string, len(stdio.Env))
			for _, item := range stdio.Env {
				env[item.Name] = item.Value
			}
			result[stdio.Name] = mcp.ServerConfig{
				Command: stdio.Command,
				Args:    append([]string(nil), stdio.Args...),
				Env:     env,
			}
		case server.Http != nil:
			result[server.Http.Name] = mcp.ServerConfig{
				Transport: "streamable-http",
				URL:       server.Http.Url,
				Headers:   headerMap(server.Http.Headers),
			}
		case server.Sse != nil:
			result[server.Sse.Name] = mcp.ServerConfig{
				Transport: "sse",
				URL:       server.Sse.Url,
				Headers:   headerMap(server.Sse.Headers),
			}
		}
	}
	return result, nil
}

func headerMap(headers []acpsdk.HttpHeader) map[string]string {
	m := make(map[string]string, len(headers))
	for _, h := range headers {
		m[h.Name] = h.Value
	}
	return m
}

func workspaceKey(cwd string, requested map[string]mcp.ServerConfig) string {
	if len(requested) == 0 {
		return cwd
	}
	data, _ := json.Marshal(requested)
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%s\x00%x", cwd, sum)
}

func (s *Server) awaitWorkspace(ctx context.Context, w *workspaceEntry) (*workspaceEntry, error) {
	select {
	case <-w.ready:
		if w.err != nil {
			s.releaseWorkspace(w)
			return nil, w.err
		}
		return w, nil
	case <-ctx.Done():
		s.releaseWorkspace(w)
		return nil, ctx.Err()
	}
}

func missingRequestedMCPServers[T any](requested map[string]mcp.ServerConfig, connected map[string]T) []string {
	var missing []string
	for _, name := range slices.Sorted(maps.Keys(requested)) {
		if _, ok := connected[name]; !ok {
			missing = append(missing, name)
		}
	}
	return missing
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
	if w.toolUpdate != nil {
		w.toolUpdate.Cancel()
	}
	_ = w.agent.Close()
	w.ws.Close()
}

func (s *Server) registerSession(id acpsdk.SessionId, w *workspaceEntry) {
	s.mu.Lock()
	if s.sessionDirs == nil {
		s.sessionDirs = map[acpsdk.SessionId]string{}
	}
	s.sessionDirs[id] = w.agent.SessionsDir()
	_, exists := s.sessions[id]
	if !exists {
		s.sessions[id] = &sessionEntry{id: id, agent: w.agent, workspace: w}
	}
	s.mu.Unlock()

	if exists {
		s.releaseWorkspace(w)
	}
}

func (s *Server) replaceLoadedSession(id acpsdk.SessionId, w *workspaceEntry) error {
	s.mu.Lock()
	old := s.sessions[id]
	if old != nil && (old.closing || old.cancel != nil) {
		s.mu.Unlock()
		return fmt.Errorf("session %s has a prompt in progress or is closing", id)
	}
	if s.sessionDirs == nil {
		s.sessionDirs = map[acpsdk.SessionId]string{}
	}
	s.sessionDirs[id] = w.agent.SessionsDir()
	s.sessions[id] = &sessionEntry{id: id, agent: w.agent, workspace: w}
	s.mu.Unlock()

	if old != nil {
		s.releaseWorkspace(old.workspace)
	}
	return nil
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

// UnstableDeleteSession implements the stable session/delete wire method. The
// v0.13.5 Go SDK still uses the Unstable prefix for its generated Go symbols.
func (s *Server) UnstableDeleteSession(ctx context.Context, params acpsdk.UnstableDeleteSessionRequest) (acpsdk.UnstableDeleteSessionResponse, error) {
	s.mu.Lock()
	sess := s.sessions[params.SessionId]
	dir := s.sessionDirs[params.SessionId]
	removeWorkspaceRef := false
	var cancel context.CancelFunc
	if sess != nil && !sess.closing {
		sess.closing = true
		removeWorkspaceRef = true
		cancel = sess.cancel
		if cancel == nil {
			delete(s.sessions, params.SessionId)
		}
	}
	s.mu.Unlock()

	var err error
	if sess != nil {
		// Removing the code-agent session first prevents an active prompt's
		// deferred Save from recreating the session after deletion.
		err = sess.agent.DeleteSession(ctx, string(params.SessionId))
	} else if dir != "" {
		err = session.Delete(dir, string(params.SessionId))
	}
	if cancel != nil {
		cancel()
	}
	if removeWorkspaceRef {
		s.releaseWorkspace(sess.workspace)
	}
	if err != nil && !os.IsNotExist(err) {
		return acpsdk.UnstableDeleteSessionResponse{}, err
	}

	s.mu.Lock()
	delete(s.sessionDirs, params.SessionId)
	s.mu.Unlock()
	return acpsdk.UnstableDeleteSessionResponse{}, nil
}

func (s *Server) lookupSession(id acpsdk.SessionId) *sessionEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[id]
}

func (s *Server) retainSession(ctx context.Context, id acpsdk.SessionId, cancel context.CancelFunc) (*sessionEntry, func(), error) {
	for {
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
			done := sess.promptDone
			s.mu.Unlock()
			if done == nil {
				return nil, nil, fmt.Errorf("session %s already has a prompt in progress", id)
			}
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			}
		}

		done := make(chan struct{})
		sess.workspace.refs++
		sess.cancel = cancel
		sess.promptDone = done
		s.mu.Unlock()
		return sess, func() {
			closeDone := false
			s.mu.Lock()
			if sess.promptDone == done {
				sess.cancel = nil
				sess.promptDone = nil
				closeDone = true
				if s.sessions[id] == sess && sess.closing {
					delete(s.sessions, id)
				}
			}
			s.mu.Unlock()
			if closeDone {
				close(done)
			}
		}, nil
	}
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

// pushSessionInfo reports the title the agent derived for this session, so the
// client can replace its placeholder thread name.
func (s *Server) pushSessionInfo(ctx context.Context, sess *sessionEntry) {
	if s.conn == nil || sess.workspace == nil || sess.workspace.ws == nil {
		return
	}
	info, err := session.Load(code.SessionsDir(sess.workspace.ws.RootPath), string(sess.id))
	if err != nil || info.Title == "" {
		return
	}
	if sess.lastTitle == info.Title {
		return
	}
	sess.lastTitle = info.Title

	title := info.Title
	update := acpsdk.SessionSessionInfoUpdate{SessionUpdate: "session_info_update", Title: &title}
	if !info.UpdatedAt.IsZero() {
		u := info.UpdatedAt.UTC().Format(time.RFC3339)
		update.UpdatedAt = &u
	}
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_ = s.conn.SessionUpdate(sendCtx, acpsdk.SessionNotification{
		SessionId: sess.id,
		Update:    acpsdk.SessionUpdate{SessionInfoUpdate: &update},
	})
}

func (s *Server) Prompt(ctx context.Context, params acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sess, unregister, err := s.retainSession(ctx, params.SessionId, cancel)
	if err != nil {
		return acpsdk.PromptResponse{}, err
	}
	defer s.releaseWorkspace(sess.workspace)
	defer unregister()

	defer func() {
		if err := sess.agent.Save(string(sess.id)); err != nil {
			slog.Warn("save session failed", "session", sess.id, "err", err)
			return
		}
		s.pushSessionInfo(ctx, sess)
	}()

	notify := func(u acpsdk.SessionUpdate) {
		sendCtx := ctx
		// Finish tool calls already announced in_progress; the SDK won't write on a cancelled ctx.
		if ctx.Err() != nil {
			detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			sendCtx = detached
		}
		_ = s.conn.SessionUpdate(sendCtx, acpsdk.SessionNotification{
			SessionId: params.SessionId,
			Update:    u,
		})
	}

	// ACP agent_message_chunk updates cannot be retracted. Deliberately leave
	// Reset unsupported so a recoverable error after visible output terminates
	// the attempt instead of duplicating it with an invisible retry.
	stream, err := sess.agent.Send(ctx, string(sess.id), acp.ContentFromBlocks(params.Prompt))
	if err != nil {
		return acpsdk.PromptResponse{}, err
	}
	for msg, err := range stream {
		if err != nil {
			reason, streamErr := classifyPromptStreamError(err)
			if streamErr != nil {
				return acpsdk.PromptResponse{}, streamErr
			}
			return promptResponse(sess, reason, params.MessageId), nil
		}
		for _, c := range msg.Content {
			notifyContent(notify, agent.RoleAssistant, c)
		}
	}
	return promptResponse(sess, acpsdk.StopReasonEndTurn, params.MessageId), nil
}

func classifyPromptStreamError(err error) (acpsdk.StopReason, error) {
	if errors.Is(err, context.Canceled) {
		return acpsdk.StopReasonCancelled, nil
	}
	return "", err
}

func promptResponse(sess *sessionEntry, reason acpsdk.StopReason, messageID *string) acpsdk.PromptResponse {
	u := sess.agent.Usage(string(sess.id))
	input := tokenCount(u.InputTokens)
	cacheRead := min(tokenCount(u.CacheReadInputTokens), input)
	cacheWrite := min(tokenCount(u.CacheCreationInputTokens), input-cacheRead)
	output := tokenCount(u.OutputTokens)
	reasoning := min(tokenCount(u.ReasoningTokens), output)
	usage := &acpsdk.Usage{
		InputTokens:  input - cacheRead - cacheWrite,
		OutputTokens: output - reasoning,
		TotalTokens:  addTokenCounts(input, output),
	}
	if cacheRead > 0 {
		usage.CachedReadTokens = &cacheRead
	}
	if cacheWrite > 0 {
		usage.CachedWriteTokens = &cacheWrite
	}
	if reasoning > 0 {
		usage.ThoughtTokens = &reasoning
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
	s.mu.Lock()
	if s.sessionDirs == nil {
		s.sessionDirs = map[acpsdk.SessionId]string{}
	}
	for _, info := range out {
		s.sessionDirs[info.SessionId] = code.SessionsDir(cwd)
	}
	s.mu.Unlock()
	return acpsdk.ListSessionsResponse{Sessions: out}, nil
}

func (s *Server) ResumeSession(ctx context.Context, params acpsdk.ResumeSessionRequest) (acpsdk.ResumeSessionResponse, error) {
	w, _, err := s.loadAndAttach(ctx, params.Cwd, params.SessionId, params.McpServers)
	if err != nil {
		return acpsdk.ResumeSessionResponse{}, err
	}
	return acpsdk.ResumeSessionResponse{
		Modes:         modeState(w.agent, string(params.SessionId)),
		ConfigOptions: sessionConfigOptions(w.agent, string(params.SessionId)),
	}, nil
}

func (s *Server) LoadSession(ctx context.Context, params acpsdk.LoadSessionRequest) (acpsdk.LoadSessionResponse, error) {
	w, messages, err := s.loadAndAttach(ctx, params.Cwd, params.SessionId, params.McpServers)
	if err != nil {
		return acpsdk.LoadSessionResponse{}, err
	}
	s.replayMessages(ctx, params.SessionId, messages)
	return acpsdk.LoadSessionResponse{
		Modes:         modeState(w.agent, string(params.SessionId)),
		ConfigOptions: sessionConfigOptions(w.agent, string(params.SessionId)),
	}, nil
}

func (s *Server) loadAndAttach(ctx context.Context, cwdParam string, id acpsdk.SessionId, servers []acpsdk.McpServer) (*workspaceEntry, []agent.Message, error) {
	cwd, err := normalizeCwd(cwdParam)
	if err != nil {
		return nil, nil, err
	}
	w, err := s.acquireWorkspace(ctx, cwd, servers)
	if err != nil {
		return nil, nil, err
	}
	if err := w.agent.LoadSession(ctx, string(id)); err != nil {
		s.releaseWorkspace(w)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, &acpsdk.RequestError{
				Code:    -32002,
				Message: "Resource not found",
				Data:    map[string]any{"sessionId": string(id)},
			}
		}
		return nil, nil, fmt.Errorf("session %s not found: %w", id, err)
	}
	if err := s.replaceLoadedSession(id, w); err != nil {
		s.releaseWorkspace(w)
		return nil, nil, err
	}
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
		if c.ToolCall.Partial {
			return
		}
		presentation := c.ToolCall.Presentation
		if presentation == nil {
			presentation = agent.NewToolPresentation(
				c.ToolCall.Name, c.ToolCall.Kind, c.ToolCall.Args, c.ToolCall.Locations,
			)
		}
		title := presentation.Title
		if title == "" {
			title = c.ToolCall.Name
		}
		if title == "" {
			title = "Tool call"
		}
		kind := acpsdk.ToolKind(presentation.Kind)
		if kind == "" {
			kind = acpsdk.ToolKindOther
		}
		rawInput := parseRawInput(presentation.Args)
		locations := sdkToolLocations(presentation.Locations)
		opts := []acpsdk.ToolCallStartOpt{
			acpsdk.WithStartKind(kind),
			acpsdk.WithStartStatus(acpsdk.ToolCallStatusInProgress),
		}
		if len(locations) > 0 {
			opts = append(opts, acpsdk.WithStartLocations(locations))
		}
		// Locations are rendered as file chips. Apply the compact display input
		// afterwards so the SDK cannot mirror a lone location back into args.
		if rawInput != nil || len(locations) > 0 {
			opts = append(opts, acpsdk.WithStartRawInput(rawInput))
		}
		notify(acpsdk.StartToolCall(
			acpsdk.ToolCallId(c.ToolCall.ID),
			title,
			opts...,
		))
		return
	case c.ToolResult != nil:
		status := acpsdk.ToolCallStatusCompleted
		if c.ToolResult.IsError {
			status = acpsdk.ToolCallStatusFailed
		}
		notify(acpsdk.UpdateToolCall(
			acpsdk.ToolCallId(c.ToolResult.ID),
			acpsdk.WithUpdateStatus(status),
			acpsdk.WithUpdateContent([]acpsdk.ToolCallContent{
				acpsdk.ToolContent(acpsdk.TextBlock(c.ToolResult.Content)),
			}),
		))
		return
	}

	if c.Reasoning != nil && c.Reasoning.Summary != "" {
		notify(acpsdk.UpdateAgentThoughtText(c.Reasoning.Summary))
	}
	for _, block := range acp.ContentToBlocks([]agent.Content{c}) {
		if role == agent.RoleUser {
			notify(acpsdk.UpdateUserMessage(block))
		} else {
			notify(acpsdk.UpdateAgentMessage(block))
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

func sdkToolLocations(locations []agent.ToolLocation) []acpsdk.ToolCallLocation {
	result := make([]acpsdk.ToolCallLocation, 0, len(locations))
	for _, location := range locations {
		if location.Path == "" {
			continue
		}
		mapped := acpsdk.ToolCallLocation{Path: location.Path}
		if location.Line > 0 {
			mapped.Line = new(location.Line)
		}
		result = append(result, mapped)
	}
	return result
}
