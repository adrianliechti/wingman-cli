package acptest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
)

// LiveConfig drives RunLive, the end-to-end counterpart of Run: the same
// factory pattern, but against the real agent binary with real model turns.
// Scenarios are selected from the capabilities the agent advertises at
// initialize, so one suite runs against every bridge.
type LiveConfig struct {
	// Gate is the bridge-specific env var (e.g. "CLAUDE_ACP_LIVE"). The
	// suite also runs when the cross-bridge ACP_LIVE is set.
	Gate string
	// Binary must resolve on PATH or the suite skips.
	Binary string
	// Factory builds the bridge agent; path is the resolved Binary.
	Factory func(t *testing.T, path string) Agent
	// Timeout bounds each scenario. Defaults to 3 minutes.
	Timeout time.Duration
	// Subagents opts the bridge into the subagent scenario. ACP has no
	// subagent capability, so each bridge supplies its own spawn prompt
	// and expectations; a zero value skips the scenario.
	Subagents LiveSubagents
	// MCPHelperTest names a test in the bridge package whose body is
	// acptest.MCPServerHelper; the mcp_tool scenario re-executes it as a
	// stdio MCP server handed to the agent via session/new.
	MCPHelperTest string
	// WantUsageUpdate asserts a usage_update notification arrives per turn.
	WantUsageUpdate bool
	// WantCommands asserts an available_commands_update arrives.
	WantCommands bool
}

// LiveSubagents describes how to drive and verify a subagent turn.
type LiveSubagents struct {
	// Prompt must instruct the agent to spawn a subagent and finish with
	// Marker. Empty skips the scenario.
	Prompt string
	// Marker is the final-reply text expected in the top-level feed.
	Marker string
	// Leak, when set, is subagent-internal text that must NOT surface in
	// top-level agent messages.
	Leak string
	// WantToolTitle, when set, must appear as a substring of at least one
	// tool call title (e.g. codex's "Start subagent …" activity).
	WantToolTitle string
}

func RunLive(t *testing.T, cfg LiveConfig) {
	t.Helper()
	if os.Getenv(cfg.Gate) == "" && os.Getenv("ACP_LIVE") == "" {
		t.Skipf("set %s=1 or ACP_LIVE=1 to run the live ACP suite", cfg.Gate)
	}
	path, err := exec.LookPath(cfg.Binary)
	if err != nil {
		t.Skipf("%s not found: %v", cfg.Binary, err)
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 3 * time.Minute
	}
	factory := func(t *testing.T) Agent { return cfg.Factory(t, path) }

	t.Run("basic_turn", func(t *testing.T) { liveBasicTurn(t, factory, cfg) })
	t.Run("prompt_content", func(t *testing.T) { livePromptContent(t, factory, cfg.Timeout) })
	t.Run("multi_turn", func(t *testing.T) { liveMultiTurn(t, factory, cfg.Timeout) })
	t.Run("file_tool_turn", func(t *testing.T) { liveFileToolTurn(t, factory, cfg.Timeout) })
	t.Run("cancel", func(t *testing.T) { liveCancel(t, factory, cfg.Timeout) })
	t.Run("session_lifecycle", func(t *testing.T) { liveSessionLifecycle(t, factory, cfg.Timeout) })
	t.Run("session_tree", func(t *testing.T) { liveSessionTree(t, factory, cfg.Timeout) })
	t.Run("additional_directories", func(t *testing.T) { liveAdditionalDirectories(t, factory, cfg.Timeout) })
	t.Run("mcp_tool", func(t *testing.T) { liveMCPTool(t, factory, cfg.Timeout, cfg.MCPHelperTest) })
	t.Run("subagents", func(t *testing.T) { liveSubagents(t, factory, cfg.Timeout, cfg.Subagents) })
}

// liveSessionTree exercises the advertised session-graph operations with
// semantic checks: list must contain the session, resume and fork must carry
// the conversation context forward.
func liveSessionTree(t *testing.T, factory Factory, timeout time.Duration) {
	h := newHarness(t, factory)
	init := initialize(t, h)
	caps := init.AgentCapabilities.SessionCapabilities
	if caps.List == nil && caps.Resume == nil && caps.Fork == nil {
		t.Skip("agent advertises none of session/list, session/resume, session/fork")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cwd := t.TempDir()
	session, err := h.conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	livePrompt(t, h, ctx, session.SessionId, "Remember this codeword: PERISCOPE. Reply with exactly: SAVED")

	if caps.List != nil {
		list, err := h.conn.ListSessions(ctx, acp.ListSessionsRequest{Cwd: &cwd})
		if err != nil {
			t.Fatalf("session/list advertised but failed: %v", err)
		}
		found := false
		for _, info := range list.Sessions {
			if info.SessionId == session.SessionId {
				found = true
			}
		}
		if !found {
			t.Errorf("session/list did not contain the just-created session %s", session.SessionId)
		}
	}

	if caps.Resume != nil {
		if caps.Close != nil {
			if _, err := h.conn.CloseSession(ctx, acp.CloseSessionRequest{SessionId: session.SessionId}); err != nil {
				t.Fatalf("session/close: %v", err)
			}
		}
		if _, err := h.conn.ResumeSession(ctx, acp.ResumeSessionRequest{
			SessionId: session.SessionId, Cwd: cwd, McpServers: []acp.McpServer{},
		}); err != nil {
			t.Fatalf("session/resume advertised but failed: %v", err)
		}
		before := len(h.client.snapshot())
		livePrompt(t, h, ctx, session.SessionId, "What codeword did I ask you to remember? Reply with only the codeword.")
		if !containsText(h.client.snapshot()[before:], "PERISCOPE") {
			t.Error("resumed session lost conversation context")
		}
	}

	if caps.Fork != nil {
		forked, err := h.conn.UnstableForkSession(ctx, acp.UnstableForkSessionRequest{
			SessionId: session.SessionId, Cwd: cwd,
		})
		if err != nil {
			t.Fatalf("session/fork advertised but failed: %v", err)
		}
		if forked.SessionId == "" || forked.SessionId == session.SessionId {
			t.Fatalf("session/fork returned invalid sessionId %q", forked.SessionId)
		}
		before := len(h.client.snapshot())
		resp, err := h.conn.Prompt(ctx, acp.PromptRequest{
			SessionId: forked.SessionId,
			Prompt:    []acp.ContentBlock{acp.TextBlock("What codeword did I ask you to remember? Reply with only the codeword.")},
		})
		if err != nil {
			t.Fatalf("prompt in forked session: %v", err)
		}
		if resp.StopReason != acp.StopReasonEndTurn {
			t.Errorf("forked stop reason = %q", resp.StopReason)
		}
		var forkedText strings.Builder
		for _, n := range h.client.snapshot()[before:] {
			if n.SessionId == forked.SessionId {
				if chunk := n.Update.AgentMessageChunk; chunk != nil && chunk.Content.Text != nil {
					forkedText.WriteString(chunk.Content.Text.Text)
				}
			}
		}
		if !strings.Contains(forkedText.String(), "PERISCOPE") {
			t.Errorf("forked session lost conversation context; reply = %q", forkedText.String())
		}
	}
}

func liveAdditionalDirectories(t *testing.T, factory Factory, timeout time.Duration) {
	h := newHarness(t, factory)
	init := initialize(t, h)
	if init.AgentCapabilities.SessionCapabilities.AdditionalDirectories == nil {
		t.Skip("agent does not advertise additionalDirectories")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	extra := t.TempDir()
	session, err := h.conn.NewSession(ctx, acp.NewSessionRequest{
		Cwd: t.TempDir(), AdditionalDirectories: []string{extra}, McpServers: []acp.McpServer{},
	})
	if err != nil {
		t.Fatalf("session/new with additional directory: %v", err)
	}

	resp := livePrompt(t, h, ctx, session.SessionId,
		"Create a file at the absolute path "+filepath.Join(extra, "extra.txt")+" containing the word EXTRA using your file write tool, then reply with exactly: DONE")
	if resp.StopReason != acp.StopReasonEndTurn {
		t.Errorf("stop reason = %q, want end_turn", resp.StopReason)
	}
	if _, err := os.Stat(filepath.Join(extra, "extra.txt")); err != nil {
		t.Errorf("file in additional directory not created: %v", err)
	}
}

func liveMCPTool(t *testing.T, factory Factory, timeout time.Duration, helperTest string) {
	if helperTest == "" {
		t.Skip("bridge has no MCP helper configured")
	}
	h := newHarness(t, factory)
	_ = initialize(t, h)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	serverPath, _, _ := CommandHelper(t, helperTest, mcpHelperEnv)
	session, err := h.conn.NewSession(ctx, acp.NewSessionRequest{
		Cwd: t.TempDir(),
		McpServers: []acp.McpServer{{Stdio: &acp.McpServerStdio{
			Name:    "acplive",
			Command: serverPath,
			Args:    []string{},
			Env:     []acp.EnvVariable{{Name: mcpHelperEnv, Value: "1"}},
		}}},
	})
	if err != nil {
		t.Fatalf("session/new with MCP server: %v", err)
	}

	resp := livePrompt(t, h, ctx, session.SessionId,
		"Call the tool "+MCPToolName+" from the acplive MCP server and reply with exactly the word it returns.")
	if resp.StopReason != acp.StopReasonEndTurn {
		t.Errorf("stop reason = %q, want end_turn", resp.StopReason)
	}
	updates := h.client.snapshot()
	if !containsText(updates, MCPSecretWord) {
		t.Errorf("agent output did not contain %q — MCP server may not be wired through", MCPSecretWord)
	}
	if !containsToolCall(updates) {
		t.Error("MCP tool call produced no tool_call updates")
	}
}

func liveSubagents(t *testing.T, factory Factory, timeout time.Duration, sub LiveSubagents) {
	if sub.Prompt == "" {
		t.Skip("bridge has no subagent scenario configured")
	}
	h := newHarness(t, factory)
	_ = initialize(t, h)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	session, err := h.conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}

	resp := livePrompt(t, h, ctx, session.SessionId, sub.Prompt)
	if resp.StopReason != acp.StopReasonEndTurn {
		t.Errorf("stop reason = %q, want end_turn", resp.StopReason)
	}

	updates := h.client.snapshot()
	validateLiveUpdates(t, session.SessionId, updates)
	if sub.Marker != "" && !containsText(updates, sub.Marker) {
		t.Errorf("agent output did not contain %q", sub.Marker)
	}
	if sub.Leak != "" && containsText(updates, sub.Leak) {
		t.Errorf("subagent-internal text %q leaked into the top-level message feed", sub.Leak)
	}
	if sub.WantToolTitle != "" {
		found := false
		for _, n := range updates {
			if n.Update.ToolCall != nil && strings.Contains(n.Update.ToolCall.Title, sub.WantToolTitle) {
				found = true
			}
			if u := n.Update.ToolCallUpdate; u != nil && u.Title != nil && strings.Contains(*u.Title, sub.WantToolTitle) {
				found = true
			}
		}
		if !found {
			t.Errorf("no tool call title contained %q", sub.WantToolTitle)
		}
	}
}

// tinyPNG is a valid 1x1 transparent PNG, used to verify image prompt blocks
// survive the round trip without asserting on model vision output.
const tinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

func livePrompt(t *testing.T, h *harness, ctx context.Context, id acp.SessionId, text string) acp.PromptResponse {
	t.Helper()
	resp, err := h.conn.Prompt(ctx, acp.PromptRequest{
		SessionId: id,
		Prompt:    []acp.ContentBlock{acp.TextBlock(text)},
	})
	if err != nil {
		t.Fatalf("session/prompt: %v", err)
	}
	return resp
}

func liveBasicTurn(t *testing.T, factory Factory, cfg LiveConfig) {
	h := newHarness(t, factory)
	init := initialize(t, h)
	if init.ProtocolVersion != acp.ProtocolVersionNumber {
		t.Fatalf("protocol version = %d, want %d", init.ProtocolVersion, acp.ProtocolVersionNumber)
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	session, err := h.conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	validateModes(t, session.Modes)
	validateConfigOptions(t, session.ConfigOptions)

	if session.Modes != nil {
		for _, mode := range session.Modes.AvailableModes {
			if _, err := h.conn.SetSessionMode(ctx, acp.SetSessionModeRequest{SessionId: session.SessionId, ModeId: mode.Id}); err != nil {
				t.Fatalf("set advertised mode %q: %v", mode.Id, err)
			}
		}
		if _, err := h.conn.SetSessionMode(ctx, acp.SetSessionModeRequest{SessionId: session.SessionId, ModeId: session.Modes.CurrentModeId}); err != nil {
			t.Fatalf("restore current mode: %v", err)
		}
		if _, err := h.conn.SetSessionMode(ctx, acp.SetSessionModeRequest{SessionId: session.SessionId, ModeId: "acp-live-bogus-mode"}); err == nil {
			t.Error("setting an unknown session mode did not error")
		}
	}
	for _, option := range session.ConfigOptions {
		if option.Select == nil {
			continue
		}
		resp, err := h.conn.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{ValueId: &acp.SetSessionConfigOptionValueId{
			SessionId: session.SessionId, ConfigId: option.Select.Id, Value: option.Select.CurrentValue,
		}})
		if err != nil {
			t.Fatalf("round-trip config option %q: %v", option.Select.Id, err)
		}
		validateConfigOptions(t, resp.ConfigOptions)
	}

	if _, err := h.conn.Prompt(ctx, acp.PromptRequest{
		SessionId: "acp-live-unknown-session",
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err == nil {
		t.Error("prompt against an unknown session did not error")
	}

	resp := livePrompt(t, h, ctx, session.SessionId, "Reply with exactly: OK")
	if resp.StopReason != acp.StopReasonEndTurn {
		t.Errorf("stop reason = %q, want end_turn", resp.StopReason)
	}
	validateUsage(t, resp.Usage)

	updates := h.client.snapshot()
	validateLiveUpdates(t, session.SessionId, updates)
	if !containsText(updates, "OK") {
		t.Errorf("agent output did not contain OK")
	}
	if cfg.WantUsageUpdate && !containsUsageUpdate(updates) {
		t.Error("no usage_update notification arrived")
	}
	if cfg.WantCommands && !containsCommandsUpdate(updates) {
		t.Error("no available_commands_update notification arrived")
	}

	if init.AgentCapabilities.SessionCapabilities.Close != nil {
		if _, err := h.conn.CloseSession(ctx, acp.CloseSessionRequest{SessionId: session.SessionId}); err != nil {
			t.Fatalf("session/close advertised but failed: %v", err)
		}
	}
}

func containsUsageUpdate(updates []acp.SessionNotification) bool {
	for _, n := range updates {
		if n.Update.UsageUpdate != nil {
			return true
		}
	}
	return false
}

func containsCommandsUpdate(updates []acp.SessionNotification) bool {
	for _, n := range updates {
		if n.Update.AvailableCommandsUpdate != nil {
			return true
		}
	}
	return false
}

// livePromptContent sends the richest prompt the agent advertises support
// for: embedded context (asserted semantically — the model must read it),
// a resource link, and an image block (asserted as accepted, not described).
func livePromptContent(t *testing.T, factory Factory, timeout time.Duration) {
	h := newHarness(t, factory)
	init := initialize(t, h)
	pc := init.AgentCapabilities.PromptCapabilities
	if !pc.Image && !pc.EmbeddedContext {
		t.Skip("agent advertises neither image nor embeddedContext prompt capabilities")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	session, err := h.conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}

	var prompt []acp.ContentBlock
	want := "OK"
	if pc.EmbeddedContext {
		mime := "text/plain"
		prompt = append(prompt, acp.ResourceBlock(acp.EmbeddedResourceResource{
			TextResourceContents: &acp.TextResourceContents{
				Uri: "file:///acp-live/secret.txt", MimeType: &mime, Text: "The secret word is XYLOPHONE.",
			},
		}))
		prompt = append(prompt, acp.TextBlock("Read the provided context. Reply with exactly the secret word it contains."))
		want = "XYLOPHONE"
	} else {
		prompt = append(prompt, acp.TextBlock("Reply with exactly: OK"))
	}
	if pc.Image {
		prompt = append(prompt, acp.ImageBlock(tinyPNG, "image/png"))
	}

	resp, err := h.conn.Prompt(ctx, acp.PromptRequest{SessionId: session.SessionId, Prompt: prompt})
	if err != nil {
		t.Fatalf("session/prompt with rich content: %v", err)
	}
	if resp.StopReason != acp.StopReasonEndTurn {
		t.Errorf("stop reason = %q, want end_turn", resp.StopReason)
	}
	updates := h.client.snapshot()
	validateLiveUpdates(t, session.SessionId, updates)
	if !containsText(updates, want) {
		t.Errorf("agent output did not contain %q — prompt content may not reach the model", want)
	}
}

func liveMultiTurn(t *testing.T, factory Factory, timeout time.Duration) {
	h := newHarness(t, factory)
	_ = initialize(t, h)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	session, err := h.conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}

	livePrompt(t, h, ctx, session.SessionId, "Remember this codeword: TANGERINE. Reply with exactly: SAVED")
	before := len(h.client.snapshot())

	resp := livePrompt(t, h, ctx, session.SessionId, "What codeword did I ask you to remember? Reply with only the codeword.")
	if resp.StopReason != acp.StopReasonEndTurn {
		t.Errorf("stop reason = %q, want end_turn", resp.StopReason)
	}
	if !containsText(h.client.snapshot()[before:], "TANGERINE") {
		t.Error("second turn lost first-turn context")
	}
}

func liveFileToolTurn(t *testing.T, factory Factory, timeout time.Duration) {
	h := newHarness(t, factory)
	_ = initialize(t, h)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cwd := t.TempDir()
	session, err := h.conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}

	resp := livePrompt(t, h, ctx, session.SessionId,
		"Create a file named acp-live.txt containing the word ACPLIVE using your file write tool, then reply with exactly: DONE")
	if resp.StopReason != acp.StopReasonEndTurn {
		t.Errorf("stop reason = %q, want end_turn", resp.StopReason)
	}

	data, err := os.ReadFile(filepath.Join(cwd, "acp-live.txt"))
	if err != nil {
		t.Errorf("acp-live.txt not created: %v", err)
	} else if !strings.Contains(string(data), "ACPLIVE") {
		t.Errorf("acp-live.txt content = %q, want ACPLIVE", data)
	}

	updates := h.client.snapshot()
	validateLiveUpdates(t, session.SessionId, updates)
	if !containsToolCall(updates) {
		t.Error("file write produced no tool_call updates")
	}
	if !containsText(updates, "DONE") {
		t.Errorf("agent output did not contain DONE")
	}
}

func liveCancel(t *testing.T, factory Factory, timeout time.Duration) {
	h := newHarness(t, factory)
	_ = initialize(t, h)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	session, err := h.conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}

	type outcome struct {
		resp acp.PromptResponse
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		resp, err := h.conn.Prompt(ctx, acp.PromptRequest{
			SessionId: session.SessionId,
			Prompt:    []acp.ContentBlock{acp.TextBlock("Count from 1 to 200, one number per line. Do not use any tools.")},
		})
		done <- outcome{resp: resp, err: err}
	}()

	if !h.client.waitForAgentText(ctx) {
		t.Fatal("timed out waiting for streamed output before cancelling")
	}
	if err := h.conn.Cancel(ctx, acp.CancelNotification{SessionId: session.SessionId}); err != nil {
		t.Fatalf("session/cancel: %v", err)
	}

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("cancelled prompt returned error: %v", got.err)
		}
		if got.resp.StopReason != acp.StopReasonCancelled {
			t.Errorf("cancelled stop reason = %q, want cancelled", got.resp.StopReason)
		}
	case <-ctx.Done():
		t.Fatal("cancelled prompt did not terminate")
	}
}

func liveSessionLifecycle(t *testing.T, factory Factory, timeout time.Duration) {
	h := newHarness(t, factory)
	init := initialize(t, h)
	caps := init.AgentCapabilities.SessionCapabilities
	if !init.AgentCapabilities.LoadSession && caps.Delete == nil {
		t.Skip("agent advertises neither session/load nor session/delete")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cwd := t.TempDir()
	session, err := h.conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	livePrompt(t, h, ctx, session.SessionId, "Reply with exactly: LIFEMARK")

	if caps.Close != nil {
		if _, err := h.conn.CloseSession(ctx, acp.CloseSessionRequest{SessionId: session.SessionId}); err != nil {
			t.Fatalf("session/close: %v", err)
		}
	}

	if init.AgentCapabilities.LoadSession {
		beforeLoad := len(h.client.snapshot())
		loaded, err := h.conn.LoadSession(ctx, acp.LoadSessionRequest{
			SessionId:  session.SessionId,
			Cwd:        cwd,
			McpServers: []acp.McpServer{},
		})
		if err != nil {
			t.Fatalf("session/load advertised but failed: %v", err)
		}
		validateModes(t, loaded.Modes)
		validateConfigOptions(t, loaded.ConfigOptions)
		if !containsText(h.client.snapshot()[beforeLoad:], "LIFEMARK") {
			t.Error("session/load did not replay the prior turn's history")
		}
		if caps.Close != nil {
			if _, err := h.conn.CloseSession(ctx, acp.CloseSessionRequest{SessionId: session.SessionId}); err != nil {
				t.Fatalf("close loaded session: %v", err)
			}
		}
	}

	if caps.Delete != nil {
		if _, err := h.conn.UnstableDeleteSession(ctx, acp.UnstableDeleteSessionRequest{SessionId: session.SessionId}); err != nil {
			t.Fatalf("session/delete advertised but failed: %v", err)
		}
		if _, err := h.conn.UnstableDeleteSession(ctx, acp.UnstableDeleteSessionRequest{SessionId: session.SessionId}); err != nil {
			t.Fatalf("session/delete is not idempotent: %v", err)
		}
	}
}

// validateLiveUpdates is validateUpdates without the messageId-is-a-UUID
// check: real backends use their own item-id schemes, which ACP permits.
func validateLiveUpdates(t *testing.T, sessionID acp.SessionId, updates []acp.SessionNotification) {
	t.Helper()
	seenTools := map[acp.ToolCallId]bool{}
	for _, notification := range updates {
		if notification.SessionId != sessionID {
			t.Fatalf("notification sessionId = %q, want %q", notification.SessionId, sessionID)
		}
		u := notification.Update
		if u.AgentMessageChunk != nil && u.AgentMessageChunk.Content.Text != nil && u.AgentMessageChunk.Content.Text.Text == "" {
			t.Fatal("empty agent_message_chunk")
		}
		if u.AgentThoughtChunk != nil && u.AgentThoughtChunk.Content.Text != nil && u.AgentThoughtChunk.Content.Text.Text == "" {
			t.Fatal("empty agent_thought_chunk")
		}
		if u.ToolCall != nil {
			seenTools[u.ToolCall.ToolCallId] = true
		}
		if u.ToolCallUpdate != nil && !seenTools[u.ToolCallUpdate.ToolCallId] {
			t.Fatalf("tool_call_update %q arrived before tool_call", u.ToolCallUpdate.ToolCallId)
		}
	}
}

func (c *recordingClient) waitForAgentText(ctx context.Context) bool {
	for {
		for _, n := range c.snapshot() {
			if chunk := n.Update.AgentMessageChunk; chunk != nil && chunk.Content.Text != nil && strings.TrimSpace(chunk.Content.Text.Text) != "" {
				return true
			}
		}
		select {
		case <-ctx.Done():
			return false
		case <-c.changed:
		}
	}
}
