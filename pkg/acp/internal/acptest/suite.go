package acptest

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"
)

const (
	NormalPrompt = "ACP_CONTRACT_NORMAL"
	CancelPrompt = "ACP_CONTRACT_CANCEL"
	NormalText   = "ACP_CONTRACT_OK"
	CancelText   = "ACP_CONTRACT_BLOCKING"
)

type Agent interface {
	acp.Agent
}

type connectionSetter interface {
	SetAgentConnection(*acp.AgentSideConnection)
}

type closer interface {
	Close() error
}

type Factory func(*testing.T) Agent

func CommandHelper(t *testing.T, testName, envKey string) (path, dir string, env []string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "acp-contract-helper")
	binary := "'" + strings.ReplaceAll(os.Args[0], "'", "'\\''") + "'"
	body := fmt.Sprintf("#!/bin/sh\nexec %s -test.run '^%s$' -- \"$@\"\n", binary, testName)
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path, dir, append(os.Environ(), envKey+"=1")
}

func Run(t *testing.T, factory Factory) {
	t.Helper()
	t.Run("initialize", func(t *testing.T) { testInitialize(t, factory) })
	t.Run("session_contract", func(t *testing.T) { testSessionContract(t, factory) })
	t.Run("cancel_contract", func(t *testing.T) { testCancelContract(t, factory) })
}

type harness struct {
	conn   *acp.ClientSideConnection
	client *recordingClient
}

func newHarness(t *testing.T, factory Factory) *harness {
	t.Helper()
	agentSide, clientSide := net.Pipe()
	agent := factory(t)
	agentConn := acp.NewAgentSideConnection(agent, agentSide, agentSide)
	setter, ok := agent.(connectionSetter)
	if !ok {
		t.Fatalf("contract agent %T cannot receive its ACP connection", agent)
	}
	setter.SetAgentConnection(agentConn)
	client := newRecordingClient()
	clientConn := acp.NewClientSideConnection(client, clientSide, clientSide)
	t.Cleanup(func() {
		if c, ok := agent.(closer); ok {
			_ = c.Close()
		}
		_ = agentSide.Close()
		_ = clientSide.Close()
	})
	return &harness{conn: clientConn, client: client}
}

func initialize(t *testing.T, h *harness) acp.InitializeResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := h.conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientInfo:      &acp.Implementation{Name: "contract-test", Version: "1"},
		ClientCapabilities: acp.ClientCapabilities{
			Elicitation: &acp.ElicitationCapabilities{
				Form: &acp.ElicitationFormCapabilities{},
				Url:  &acp.ElicitationUrlCapabilities{},
			},
		},
	})
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	return resp
}

func testInitialize(t *testing.T, factory Factory) {
	h := newHarness(t, factory)
	resp := initialize(t, h)
	if resp.ProtocolVersion != acp.ProtocolVersionNumber {
		t.Fatalf("protocol version = %d, want %d", resp.ProtocolVersion, acp.ProtocolVersionNumber)
	}
	if resp.AgentInfo == nil || resp.AgentInfo.Name == "" || resp.AgentInfo.Version == "" {
		t.Fatalf("invalid agentInfo: %#v", resp.AgentInfo)
	}
}

func testSessionContract(t *testing.T, factory Factory) {
	h := newHarness(t, factory)
	init := initialize(t, h)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := h.conn.NewSession(ctx, acp.NewSessionRequest{Cwd: "relative", McpServers: []acp.McpServer{}}); err == nil {
		t.Fatal("session/new accepted a relative cwd")
	}

	newReq := acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}}
	if init.AgentCapabilities.SessionCapabilities.AdditionalDirectories != nil {
		newReq.AdditionalDirectories = []string{t.TempDir()}
	}
	if init.AgentCapabilities.McpCapabilities.Http {
		newReq.McpServers = append(newReq.McpServers, acp.McpServer{Http: &acp.McpServerHttpInline{Name: "contract-http", Url: "https://example.invalid/mcp"}})
	}
	if init.AgentCapabilities.McpCapabilities.Sse {
		newReq.McpServers = append(newReq.McpServers, acp.McpServer{Sse: &acp.McpServerSseInline{Name: "contract-sse", Url: "https://example.invalid/sse"}})
	}

	session, err := h.conn.NewSession(ctx, newReq)
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	if session.SessionId == "" {
		t.Fatal("session/new returned an empty sessionId")
	}
	validateModes(t, session.Modes)
	validateConfigOptions(t, session.ConfigOptions)

	if session.Modes != nil {
		if _, err := h.conn.SetSessionMode(ctx, acp.SetSessionModeRequest{SessionId: session.SessionId, ModeId: session.Modes.CurrentModeId}); err != nil {
			t.Fatalf("round-trip current mode: %v", err)
		}
	}
	for _, option := range session.ConfigOptions {
		var req acp.SetSessionConfigOptionRequest
		switch {
		case option.Select != nil:
			req.ValueId = &acp.SetSessionConfigOptionValueId{SessionId: session.SessionId, ConfigId: option.Select.Id, Value: option.Select.CurrentValue}
		case option.Boolean != nil:
			req.Boolean = &acp.SetSessionConfigOptionBoolean{SessionId: session.SessionId, ConfigId: option.Boolean.Id, Type: "boolean", Value: option.Boolean.CurrentValue}
		default:
			t.Fatal("config option has no variant")
		}
		resp, err := h.conn.SetSessionConfigOption(ctx, req)
		if err != nil {
			t.Fatalf("round-trip config option: %v", err)
		}
		validateConfigOptions(t, resp.ConfigOptions)
	}

	messageID := uuid.NewString()
	prompt := []acp.ContentBlock{acp.TextBlock(NormalPrompt)}
	if init.AgentCapabilities.PromptCapabilities.Image {
		prompt = append(prompt, acp.ImageBlock("AA==", "image/png"))
	}
	if init.AgentCapabilities.PromptCapabilities.EmbeddedContext {
		mime := "text/plain"
		prompt = append(prompt, acp.ResourceBlock(acp.EmbeddedResourceResource{
			TextResourceContents: &acp.TextResourceContents{
				Uri: "file:///contract/context.txt", MimeType: &mime, Text: "contract context",
			},
		}))
	}
	result, err := h.conn.Prompt(ctx, acp.PromptRequest{SessionId: session.SessionId, MessageId: &messageID, Prompt: prompt})
	if err != nil {
		t.Fatalf("session/prompt: %v", err)
	}
	if result.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stop reason = %q, want end_turn", result.StopReason)
	}
	if result.UserMessageId == nil || *result.UserMessageId != messageID {
		t.Fatalf("userMessageId = %v, want %q", result.UserMessageId, messageID)
	}
	validateUsage(t, result.Usage)
	updates := h.client.snapshot()
	validateUpdates(t, session.SessionId, updates)
	if !containsText(updates, NormalText) {
		t.Fatalf("agent output did not contain %q", NormalText)
	}
	if !containsToolCall(updates) {
		t.Fatal("contract backend did not exercise the ACP tool-call lifecycle")
	}

	caps := init.AgentCapabilities.SessionCapabilities
	if caps.Fork != nil {
		forkUpdateStart := len(h.client.snapshot())
		forked, err := h.conn.UnstableForkSession(ctx, acp.UnstableForkSessionRequest{
			SessionId:             session.SessionId,
			Cwd:                   newReq.Cwd,
			AdditionalDirectories: newReq.AdditionalDirectories,
			McpServers:            unstableMCPServers(newReq.McpServers),
		})
		if err != nil {
			t.Fatalf("session/fork advertised but failed: %v", err)
		}
		if forked.SessionId == "" || forked.SessionId == session.SessionId {
			t.Fatalf("session/fork returned invalid sessionId %q", forked.SessionId)
		}
		validateModes(t, forked.Modes)
		validateUnstableConfigOptions(t, forked.ConfigOptions)
		forkUpdates := h.client.snapshot()[forkUpdateStart:]
		validateUpdates(t, forked.SessionId, forkUpdates)
		if caps.Close != nil {
			if _, err := h.conn.CloseSession(ctx, acp.CloseSessionRequest{SessionId: forked.SessionId}); err != nil {
				t.Fatalf("close forked session: %v", err)
			}
		}
	}

	if caps.List != nil {
		if _, err := h.conn.ListSessions(ctx, acp.ListSessionsRequest{}); err != nil {
			t.Fatalf("session/list advertised but failed: %v", err)
		}
	}
	if caps.Close != nil {
		if _, err := h.conn.CloseSession(ctx, acp.CloseSessionRequest{SessionId: session.SessionId}); err != nil {
			t.Fatalf("session/close advertised but failed: %v", err)
		}
		if _, err := h.conn.Prompt(ctx, acp.PromptRequest{SessionId: session.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("after close")}}); err == nil {
			t.Fatal("closed session still accepted a prompt")
		}
	}

	if caps.Resume != nil {
		resumed, err := h.conn.ResumeSession(ctx, acp.ResumeSessionRequest{
			SessionId:             session.SessionId,
			Cwd:                   newReq.Cwd,
			AdditionalDirectories: newReq.AdditionalDirectories,
			McpServers:            newReq.McpServers,
		})
		if err != nil {
			t.Fatalf("session/resume advertised but failed: %v", err)
		}
		validateModes(t, resumed.Modes)
		validateConfigOptions(t, resumed.ConfigOptions)
		if caps.Close != nil {
			if _, err := h.conn.CloseSession(ctx, acp.CloseSessionRequest{SessionId: session.SessionId}); err != nil {
				t.Fatalf("close resumed session: %v", err)
			}
		}
	}

	if init.AgentCapabilities.LoadSession {
		loaded, err := h.conn.LoadSession(ctx, acp.LoadSessionRequest{
			SessionId:             session.SessionId,
			Cwd:                   newReq.Cwd,
			AdditionalDirectories: newReq.AdditionalDirectories,
			McpServers:            newReq.McpServers,
		})
		if err != nil {
			t.Fatalf("session/load advertised but failed: %v", err)
		}
		validateModes(t, loaded.Modes)
		validateConfigOptions(t, loaded.ConfigOptions)
		validateUpdates(t, session.SessionId, updatesForSession(h.client.snapshot(), session.SessionId))
	}

	if caps.Delete != nil {
		if _, err := h.conn.UnstableDeleteSession(ctx, acp.UnstableDeleteSessionRequest{SessionId: session.SessionId}); err != nil {
			t.Fatalf("session/delete advertised but failed: %v", err)
		}
	} else if init.AgentCapabilities.LoadSession && caps.Close != nil {
		if _, err := h.conn.CloseSession(ctx, acp.CloseSessionRequest{SessionId: session.SessionId}); err != nil {
			t.Fatalf("close loaded session: %v", err)
		}
	}
}

func unstableMCPServers(servers []acp.McpServer) []acp.UnstableMcpServer {
	out := make([]acp.UnstableMcpServer, 0, len(servers))
	for _, server := range servers {
		converted := acp.UnstableMcpServer{Stdio: server.Stdio}
		if server.Http != nil {
			converted.Http = &acp.UnstableMcpServerHttp{
				Meta: server.Http.Meta, Headers: server.Http.Headers,
				Name: server.Http.Name, Type: server.Http.Type, Url: server.Http.Url,
			}
		}
		if server.Sse != nil {
			converted.Sse = &acp.UnstableMcpServerSse{
				Meta: server.Sse.Meta, Headers: server.Sse.Headers,
				Name: server.Sse.Name, Type: server.Sse.Type, Url: server.Sse.Url,
			}
		}
		if server.Acp != nil {
			converted.Acp = &acp.UnstableMcpServerAcpInline{
				Meta: server.Acp.Meta, Id: acp.UnstableMcpServerAcpId(server.Acp.Id),
				Name: server.Acp.Name, Type: server.Acp.Type,
			}
		}
		out = append(out, converted)
	}
	return out
}

func testCancelContract(t *testing.T, factory Factory) {
	h := newHarness(t, factory)
	_ = initialize(t, h)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := h.conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}

	messageID := uuid.NewString()
	type outcome struct {
		resp acp.PromptResponse
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		resp, err := h.conn.Prompt(ctx, acp.PromptRequest{SessionId: session.SessionId, MessageId: &messageID, Prompt: []acp.ContentBlock{acp.TextBlock(CancelPrompt)}})
		done <- outcome{resp: resp, err: err}
	}()
	if !h.client.waitForText(ctx, CancelText) {
		t.Fatalf("timed out waiting for %q", CancelText)
	}
	if err := h.conn.Cancel(ctx, acp.CancelNotification{SessionId: session.SessionId}); err != nil {
		t.Fatalf("session/cancel: %v", err)
	}
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("cancelled prompt: %v", got.err)
		}
		if got.resp.StopReason != acp.StopReasonCancelled {
			t.Fatalf("cancelled stop reason = %q", got.resp.StopReason)
		}
		if got.resp.UserMessageId == nil || *got.resp.UserMessageId != messageID {
			t.Fatalf("cancelled userMessageId = %v", got.resp.UserMessageId)
		}
		validateUsage(t, got.resp.Usage)
	case <-ctx.Done():
		t.Fatal("cancelled prompt did not terminate")
	}
}

func validateModes(t *testing.T, modes *acp.SessionModeState) {
	t.Helper()
	if modes == nil {
		return
	}
	seen := map[acp.SessionModeId]bool{}
	for _, mode := range modes.AvailableModes {
		if mode.Id == "" || mode.Name == "" || seen[mode.Id] {
			t.Fatalf("invalid or duplicate mode: %#v", mode)
		}
		seen[mode.Id] = true
	}
	if !seen[modes.CurrentModeId] {
		t.Fatalf("current mode %q is not available", modes.CurrentModeId)
	}
}

func validateConfigOptions(t *testing.T, options []acp.SessionConfigOption) {
	t.Helper()
	seen := map[acp.SessionConfigId]bool{}
	for _, option := range options {
		var id acp.SessionConfigId
		switch {
		case option.Select != nil:
			id = option.Select.Id
			if option.Select.Name == "" {
				t.Fatal("select config option has an empty name")
			}
			validateSelectOptions(t, option.Select.CurrentValue, option.Select.Options)
		case option.Boolean != nil:
			id = option.Boolean.Id
			if option.Boolean.Name == "" {
				t.Fatal("boolean config option has an empty name")
			}
		default:
			t.Fatal("config option has no variant")
		}
		if id == "" || seen[id] {
			t.Fatalf("empty or duplicate config option id %q", id)
		}
		seen[id] = true
	}
}

func validateUnstableConfigOptions(t *testing.T, options []acp.UnstableSessionConfigOption) {
	t.Helper()
	seen := map[acp.SessionConfigId]bool{}
	for _, option := range options {
		var id acp.SessionConfigId
		name := ""
		switch {
		case option.Select != nil:
			id, name = option.Select.Id, option.Select.Name
		case option.Boolean != nil:
			id, name = option.Boolean.Id, option.Boolean.Name
		default:
			t.Fatal("unstable config option has no variant")
		}
		if id == "" || name == "" || seen[id] {
			t.Fatalf("invalid or duplicate unstable config option %q (%q)", id, name)
		}
		seen[id] = true
	}
}

func validateSelectOptions(t *testing.T, current acp.SessionConfigValueId, options acp.SessionConfigSelectOptions) {
	t.Helper()
	var values []acp.SessionConfigSelectOption
	switch {
	case options.Ungrouped != nil:
		values = append(values, (*options.Ungrouped)...)
	case options.Grouped != nil:
		for _, group := range *options.Grouped {
			if group.Group == "" || group.Name == "" {
				t.Fatalf("invalid config option group: %#v", group)
			}
			values = append(values, group.Options...)
		}
	default:
		t.Fatal("select config option has no options variant")
	}
	seen := map[acp.SessionConfigValueId]bool{}
	for _, value := range values {
		if value.Value == "" || value.Name == "" || seen[value.Value] {
			t.Fatalf("invalid or duplicate config option value: %#v", value)
		}
		seen[value.Value] = true
	}
	if !seen[current] {
		t.Fatalf("current config value %q is not available", current)
	}
}

func validateUsage(t *testing.T, usage *acp.Usage) {
	t.Helper()
	if usage == nil {
		return
	}
	total := usage.InputTokens + usage.OutputTokens
	if usage.CachedReadTokens != nil {
		total += *usage.CachedReadTokens
	}
	if usage.CachedWriteTokens != nil {
		total += *usage.CachedWriteTokens
	}
	if usage.ThoughtTokens != nil {
		total += *usage.ThoughtTokens
	}
	if usage.TotalTokens != total {
		t.Fatalf("usage total = %d, component sum = %d", usage.TotalTokens, total)
	}
}

func validateUpdates(t *testing.T, sessionID acp.SessionId, updates []acp.SessionNotification) {
	t.Helper()
	seenTools := map[acp.ToolCallId]bool{}
	for _, notification := range updates {
		if notification.SessionId != sessionID {
			t.Fatalf("notification sessionId = %q, want %q", notification.SessionId, sessionID)
		}
		u := notification.Update
		for _, chunk := range []*acp.SessionUpdateAgentMessageChunk{u.AgentMessageChunk} {
			if chunk != nil {
				if chunk.Content.Text != nil && chunk.Content.Text.Text == "" {
					t.Fatal("empty agent_message_chunk")
				}
				validateMessageID(t, chunk.MessageId)
			}
		}
		if u.AgentThoughtChunk != nil {
			if u.AgentThoughtChunk.Content.Text != nil && u.AgentThoughtChunk.Content.Text.Text == "" {
				t.Fatal("empty agent_thought_chunk")
			}
			validateMessageID(t, u.AgentThoughtChunk.MessageId)
		}
		if u.UserMessageChunk != nil {
			validateMessageID(t, u.UserMessageChunk.MessageId)
		}
		if u.ToolCall != nil {
			seenTools[u.ToolCall.ToolCallId] = true
		}
		if u.ToolCallUpdate != nil && !seenTools[u.ToolCallUpdate.ToolCallId] {
			t.Fatalf("tool_call_update %q arrived before tool_call", u.ToolCallUpdate.ToolCallId)
		}
	}
}

func updatesForSession(updates []acp.SessionNotification, sessionID acp.SessionId) []acp.SessionNotification {
	filtered := make([]acp.SessionNotification, 0, len(updates))
	for _, notification := range updates {
		if notification.SessionId == sessionID {
			filtered = append(filtered, notification)
		}
	}
	return filtered
}

func validateMessageID(t *testing.T, id *string) {
	t.Helper()
	if id != nil {
		if _, err := uuid.Parse(*id); err != nil {
			t.Fatalf("messageId %q is not a UUID", *id)
		}
	}
}

func containsText(updates []acp.SessionNotification, want string) bool {
	var b strings.Builder
	for _, n := range updates {
		if chunk := n.Update.AgentMessageChunk; chunk != nil && chunk.Content.Text != nil {
			b.WriteString(chunk.Content.Text.Text)
		}
	}
	return strings.Contains(b.String(), want)
}

func containsToolCall(updates []acp.SessionNotification) bool {
	for _, n := range updates {
		if n.Update.ToolCall != nil {
			return true
		}
	}
	return false
}

type recordingClient struct {
	mu      sync.Mutex
	updates []acp.SessionNotification
	changed chan struct{}
}

func newRecordingClient() *recordingClient {
	return &recordingClient{changed: make(chan struct{}, 1)}
}

func (c *recordingClient) SessionUpdate(_ context.Context, n acp.SessionNotification) error {
	c.mu.Lock()
	c.updates = append(c.updates, n)
	c.mu.Unlock()
	select {
	case c.changed <- struct{}{}:
	default:
	}
	return nil
}

func (c *recordingClient) snapshot() []acp.SessionNotification {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]acp.SessionNotification(nil), c.updates...)
}

func (c *recordingClient) waitForText(ctx context.Context, want string) bool {
	for {
		if containsText(c.snapshot(), want) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-c.changed:
		}
	}
}

func (c *recordingClient) RequestPermission(_ context.Context, p acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	if len(p.Options) == 0 {
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}}, nil
	}
	selected := p.Options[0].OptionId
	for _, option := range p.Options {
		if option.Kind == acp.PermissionOptionKindAllowOnce || option.Kind == acp.PermissionOptionKindAllowAlways {
			selected = option.OptionId
			break
		}
	}
	return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Selected: &acp.RequestPermissionOutcomeSelected{OptionId: selected}}}, nil
}

func (*recordingClient) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, errors.ErrUnsupported
}
func (*recordingClient) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, errors.ErrUnsupported
}
func (*recordingClient) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, errors.ErrUnsupported
}
func (*recordingClient) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, errors.ErrUnsupported
}
func (*recordingClient) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, errors.ErrUnsupported
}
func (*recordingClient) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, errors.ErrUnsupported
}
func (*recordingClient) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, errors.ErrUnsupported
}
func (*recordingClient) UnstableCreateElicitation(_ context.Context, _ acp.UnstableCreateElicitationRequest) (acp.UnstableCreateElicitationResponse, error) {
	return acp.UnstableCreateElicitationResponse{Accept: &acp.UnstableCreateElicitationAccept{Action: "accept", Content: map[string]any{}}}, nil
}
func (*recordingClient) UnstableCompleteElicitation(context.Context, acp.UnstableCompleteElicitationNotification) error {
	return nil
}
