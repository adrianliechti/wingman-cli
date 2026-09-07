package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"

	acpcommon "github.com/adrianliechti/wingman-agent/pkg/acp"
	"github.com/google/uuid"

	"github.com/adrianliechti/wingman-agent/internal/process"
)

type session struct {
	id    acp.SessionId
	cwd   string
	agent *Agent

	promptMu       acpcommon.PromptGate
	mu             sync.Mutex
	closed         bool
	modelID        string
	modelOverride  bool
	effort         string
	mode           string
	mcpServers     []acp.McpServer
	additionalDirs []string
	resumeFrom     string
	forkOnResume   bool
	started        bool
	lastTitle      string
	cancel         context.CancelFunc
	proc           *claudeProc
}

func (a *Agent) newSession(id acp.SessionId, cwd, model, effort string, additionalDirs []string) *session {
	s := &session{
		id:             id,
		cwd:            cwd,
		agent:          a,
		modelID:        model,
		modelOverride:  model != "" && model != "default",
		effort:         effort,
		mode:           defaultModeID,
		additionalDirs: append([]string(nil), additionalDirs...),
	}
	return s
}

func (s *session) cancelTurn() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *session) close() {
	s.mu.Lock()
	s.closed = true
	cancel := s.cancel
	proc := s.proc
	s.proc = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if proc != nil {
		proc.shutdown()
	}
}

func (s *session) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *session) runTurn(ctx context.Context, prompt []acp.ContentBlock) (acp.StopReason, *acp.Usage, error) {
	p, err := s.ensureProc()
	if err != nil {
		return "", nil, err
	}

	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return acp.StopReasonCancelled, nil, nil
	}
	s.cancel = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.cancel = nil
		s.mu.Unlock()
	}()

	p.beginTurn(turnCtx)
	input := promptMessage(prompt)
	p.turnMu.Lock()
	input.UUID = p.turnID
	p.turnMu.Unlock()
	if err := p.out.writeJSON(input); err != nil {
		p.finishTurn()
		s.dropProc(p)
		return "", nil, fmt.Errorf("write prompt: %w", err)
	}

	select {
	case <-turnCtx.Done():

		_ = p.out.writeJSON(interruptRequest())
		select {
		case r := <-p.results:
			return acp.StopReasonCancelled, r.usage, nil
		case <-p.dead:
		case <-time.After(5 * time.Second):
			s.dropProc(p)
		}
		return acp.StopReasonCancelled, nil, nil
	case r := <-p.results:
		if r.err != nil {
			s.dropProc(p)
			return "", nil, r.err
		}
		s.pushTitleUpdate(ctx)
		return r.stop, r.usage, nil
	case <-p.dead:
		// Preserve a result already read immediately before process EOF.
		select {
		case r := <-p.results:
			return r.stop, r.usage, r.err
		default:
		}
		s.dropProc(p)
		return "", nil, fmt.Errorf("claude process exited unexpectedly")
	}
}

// pushTitleUpdate notifies the client when the CLI's auto-generated session
// title has changed since the last time we looked. The CLI has no push event
// for it — it's regenerated in the background and persisted to the session's
// JSONL file — so we read it back at turn end, the same point a new title
// would have landed, and only notify when it actually changed.
func (s *session) pushTitleUpdate(ctx context.Context) {
	dir := projectDirFor(s.cwd)
	if dir == "" {
		return
	}
	title, _ := scanSessionMetadata(filepath.Join(dir, string(s.id)+".jsonl"))
	if title == "" {
		return
	}

	s.mu.Lock()
	changed := title != s.lastTitle
	if changed {
		s.lastTitle = title
	}
	s.mu.Unlock()
	if !changed {
		return
	}

	t := title
	updatedAt := time.Now().UTC().Format(time.RFC3339)
	_ = s.agent.conn.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: s.id,
		Update: acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{
			SessionUpdate: "session_info_update",
			Title:         &t,
			UpdatedAt:     &updatedAt,
		}},
	})
}

func (s *session) ensureProc() (*claudeProc, error) {
	a := s.agent
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("session %s is closed", s.id)
	}

	sig := s.spawnSigLocked()
	if s.proc != nil && s.proc.sig == sig && !s.proc.isDead() {
		return s.proc, nil
	}
	if s.proc != nil {
		s.proc.shutdown()
		s.proc = nil
	}

	args := s.cliArgsLocked()
	procCtx, kill := context.WithCancel(context.Background())
	cmd := exec.CommandContext(procCtx, a.path, args...)
	cmd.WaitDelay = 2 * time.Second
	process.Hide(cmd)
	cmd.Dir = s.cwd
	if a.env != nil {
		cmd.Env = a.env
	}
	cmd.Stderr = a.stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		kill()
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		kill()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		kill()
		return nil, fmt.Errorf("start claude: %w", err)
	}

	p := &claudeProc{
		cmd:             cmd,
		out:             &streamWriter{w: acpcommon.NewConnectionWriter(stdin, 0)},
		stdin:           stdin,
		sig:             sig,
		kill:            kill,
		cwd:             s.cwd,
		session:         s,
		models:          append([]ModelEntry(nil), a.models...),
		tools:           toolUseCache{},
		emitted:         newToolCallTracker(),
		streamedContent: &streamedBlockTracker{},
		subagentParents: make(map[string]string),
		results:         make(chan turnResult, 1),
		dead:            make(chan struct{}),
	}
	go p.read(procCtx, a.conn, s.id, stdout)

	s.started = true
	s.resumeFrom = ""
	s.forkOnResume = false
	s.proc = p
	return p, nil
}

func (s *session) dropProc(p *claudeProc) {
	p.shutdown()
	s.mu.Lock()
	if s.proc == p {
		s.proc = nil
	}
	s.mu.Unlock()
}

func (s *session) spawnSigLocked() string {
	return strings.Join(append([]string{s.modelID, s.effort, s.mode}, s.additionalDirs...), "\x00")
}

type claudeProc struct {
	cmd             *exec.Cmd
	out             *streamWriter
	stdin           io.Closer
	sig             string
	kill            context.CancelFunc
	cwd             string
	session         *session
	models          []ModelEntry
	tools           toolUseCache
	emitted         *toolCallTracker
	streamedContent *streamedBlockTracker

	turnMu          sync.Mutex
	turnActive      bool
	turnID          string
	turnCtx         context.Context
	turnCancel      context.CancelFunc
	subagentMu      sync.Mutex
	subagentParents map[string]string
	results         chan turnResult
	dead            chan struct{}
	shutdownOnce    sync.Once
}

func (p *claudeProc) beginTurn(ctx context.Context) {
	p.turnMu.Lock()
	if p.turnCancel != nil {
		p.turnCancel()
	}
	p.turnCtx, p.turnCancel = context.WithCancel(ctx)
	p.turnID = uuid.NewString()
	p.turnActive = true
	p.turnMu.Unlock()
}

func (p *claudeProc) finishTurn() bool {
	p.turnMu.Lock()
	defer p.turnMu.Unlock()
	wasActive := p.turnActive
	p.turnActive = false
	if p.turnCancel != nil {
		p.turnCancel()
		p.turnCancel = nil
	}
	return wasActive
}

func (p *claudeProc) parentForAgent(agentID string) string {
	if agentID == "" {
		return ""
	}
	p.subagentMu.Lock()
	defer p.subagentMu.Unlock()
	return p.subagentParents[agentID]
}

type turnResult struct {
	stop  acp.StopReason
	err   error
	usage *acp.Usage
}

func (p *claudeProc) isDead() bool {
	select {
	case <-p.dead:
		return true
	default:
		return false
	}
}

func (p *claudeProc) shutdown() {
	p.shutdownOnce.Do(func() {
		_ = p.stdin.Close()
		exited := make(chan struct{})
		go func() {
			_ = p.cmd.Wait()
			close(exited)
		}()
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			p.kill()
			<-exited
		}
	})
}

func (p *claudeProc) read(ctx context.Context, conn *acp.AgentSideConnection, sid acp.SessionId, r io.Reader) {
	defer close(p.dead)
	defer p.finishTurn()
	stderr := p.session.agent.stderr
	app := &approver{ctx: ctx, conn: conn, sid: sid, out: p.out, cwd: p.cwd, emitted: p.emitted, parentForAgent: p.parentForAgent,
		askForm:   p.session.agent.supportsFormElicitation(),
		applyMode: func(modeID string) { p.applyMode(ctx, conn, sid, modeID) }}

	scanner := newCLIScanner(r)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var env cliEnvelope
		if err := json.Unmarshal(line, &env); err != nil {
			fmt.Fprintf(stderr, "claude-acp: skipping non-JSON line: %s\n", line)
			continue
		}
		switch env.Type {
		case "stream_event":
			if env.ParentToolUseID != "" {
				continue
			}
			if err := emitStreamEvent(ctx, conn, sid, env.Event, p.streamedContent); err != nil {
				fmt.Fprintf(stderr, "claude-acp: emit stream event: %v\n", err)
			}
		case "assistant":
			if err := emitAssistant(ctx, conn, sid, env.Message, p.cwd, p.tools, p.emitted, p.streamedContent, env.ParentToolUseID); err != nil {
				fmt.Fprintf(stderr, "claude-acp: emit assistant: %v\n", err)
			}
		case "user":
			if err := emitToolResults(ctx, conn, sid, env.Message, p.tools, p.emitted, env.ParentToolUseID); err != nil {
				fmt.Fprintf(stderr, "claude-acp: emit tool result: %v\n", err)
			}
		case "tool_progress":
			p.handleToolProgress(ctx, conn, sid, env)
		case "control_request":
			var req controlRequest
			if json.Unmarshal(line, &req) == nil {
				// Capture this turn before launching the dialog goroutine. A later
				// turn must never inherit an earlier request's permission answer.
				perTurn := *app
				p.turnMu.Lock()
				perTurn.ctx = p.turnCtx
				p.turnMu.Unlock()
				if perTurn.ctx == nil {
					app.respondDeny(req.RequestID)
					continue
				}
				go perTurn.handle(req)
			}
		case "result":
			var result cliResult
			if json.Unmarshal(line, &result) != nil {
				continue
			}
			p.turnMu.Lock()
			matches := result.UserMessageUUID == "" || result.UserMessageUUID == p.turnID
			p.turnMu.Unlock()
			if !matches {
				continue
			}
			tr, usageUpd := resultToTurn(line)
			if !p.finishTurn() {
				continue
			}
			if usageUpd != nil {
				_ = acpcommon.Notify(ctx, conn, sid, *usageUpd)
			}
			select {
			case p.results <- tr:
			default:
			}
		case "rate_limit_event":
			if note := rateLimitNote(env); note != "" {
				_ = acpcommon.Notify(ctx, conn, sid, acp.UpdateAgentMessageText(note))
			}
		case "system":
			p.handleSystem(ctx, conn, sid, env)
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(stderr, "claude-acp: scan error: %v\n", err)
	}
}

// rateLimitNote skips the routine "allowed" heartbeat.
func rateLimitNote(env cliEnvelope) string {
	status := strings.TrimSpace(env.Status)
	if status == "" || status == "allowed" {
		return ""
	}
	note := "Rate limit " + status
	if env.RateLimitType != "" {
		note += " (" + strings.ReplaceAll(env.RateLimitType, "_", " ") + ")"
	}
	if resets := formatResetsAt(env.ResetsAt); resets != "" {
		note += ", resets " + resets
	}
	return "*" + note + ".*\n\n"
}

func formatResetsAt(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return time.Unix(int64(t), 0).Format(time.RFC3339)
	}
	return ""
}

// subagentParent falls back to tool_use_id when the task was never registered.
func (p *claudeProc) subagentParent(taskID, toolUseID string) string {
	if taskID != "" {
		p.subagentMu.Lock()
		id := p.subagentParents[taskID]
		p.subagentMu.Unlock()
		if id != "" {
			return id
		}
	}
	return toolUseID
}

func taskProgressNote(env cliEnvelope) string {
	parts := make([]string, 0, 3)
	if d := strings.TrimSpace(env.Description); d != "" {
		parts = append(parts, d)
	}
	if t := strings.TrimSpace(env.SubagentType); t != "" {
		parts = append(parts, "("+t+")")
	}
	if tool := strings.TrimSpace(env.LastToolName); tool != "" {
		parts = append(parts, "— "+tool)
	}
	return strings.Join(parts, " ")
}

// reportLoadErrors surfaces system/init's mcp_server_errors and plugin_errors.
func reportLoadErrors(ctx context.Context, conn *acp.AgentSideConnection, sid acp.SessionId, env cliEnvelope) {
	var lines []string
	for _, e := range env.MCPServerErrors {
		lines = append(lines, fmt.Sprintf("- MCP server %q skipped (%s): %s", e.label(), e.Type, e.Message))
	}
	for _, e := range env.PluginErrors {
		lines = append(lines, fmt.Sprintf("- Plugin %q failed to load (%s): %s", e.label(), e.Type, e.Message))
	}
	for _, s := range env.MCPServers {
		if s.Status == "failed" {
			lines = append(lines, fmt.Sprintf("- MCP server %q failed to connect", s.Name))
		}
	}
	if len(lines) == 0 {
		return
	}
	_ = conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: sid,
		Update: acp.UpdateAgentMessageText("Startup warnings:\n" + strings.Join(lines, "\n") + "\n\n")})
}

func (p *claudeProc) handleSystem(ctx context.Context, conn *acp.AgentSideConnection, sid acp.SessionId, env cliEnvelope) {
	switch env.Subtype {
	case "init":
		reportLoadErrors(ctx, conn, sid, env)
	case "session_state_changed":
		if env.State == "idle" && p.finishTurn() {
			select {
			case p.results <- turnResult{err: acp.NewInternalError("claude went idle without producing a result; partial output may be incomplete")}:
			default:
			}
		}

	case "model_refusal_fallback":
		if env.OriginalModel == "" || env.FallbackModel == "" {
			return
		}
		category := ""
		if env.RefusalCategory != "" {
			category = " (" + env.RefusalCategory + ")"
		}
		outcome := "The session will continue on " + env.FallbackModel + "."
		if env.Direction == "revert" {
			outcome = "The session stays on " + env.OriginalModel + "."
		}
		message := fmt.Sprintf("**Model fallback:** %s declined this request%s; retried with %s. %s", env.OriginalModel, category, env.FallbackModel, outcome)
		if env.RefusalExplanation != "" {
			message += "\n\n" + env.RefusalExplanation
		}
		_ = acpcommon.Notify(ctx, conn, sid, acp.UpdateAgentMessageText(message))
		if env.Direction != "revert" {
			p.applyFallbackModel(ctx, conn, sid, env.FallbackModel)
		}

	case "status":
		var text string
		switch {
		case env.Status == "compacting":
			text = "Compacting context...\n\n"
		case env.CompactResult == "success":
			text = "Context compacted.\n\n"
		case env.CompactResult == "failed":
			text = "Compacting failed.\n\n"
		}
		if text != "" {
			_ = acpcommon.Notify(ctx, conn, sid, acp.UpdateAgentMessageText(text))
		}

	case "local_command_output":
		var out string
		if json.Unmarshal(env.Content, &out) == nil && strings.TrimSpace(out) != "" {
			_ = acpcommon.Notify(ctx, conn, sid, acp.UpdateAgentMessageText(out))
		}

	case "permission_denied":
		toolCallID := env.ToolUseID
		if !p.emitted.has(toolCallID) {
			toolCallID = env.ParentToolUseID
		}
		if p.emitted.has(toolCallID) {
			msg := "Permission denied"
			var detail string
			if json.Unmarshal(env.Message, &detail) == nil && detail != "" {
				msg = "Permission denied: " + detail
			}
			_ = conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: sid, Update: acp.UpdateToolCall(acp.ToolCallId(toolCallID),
				acp.WithUpdateStatus(acp.ToolCallStatusFailed),
				acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock(msg))}),
			)})
		}

	case "task_started":
		if env.TaskID != "" && env.ToolUseID != "" {
			p.subagentMu.Lock()
			p.subagentParents[env.TaskID] = env.ToolUseID
			p.subagentMu.Unlock()
		}
	case "task_progress":
		if toolCallID := p.subagentParent(env.TaskID, env.ToolUseID); toolCallID != "" {
			if note := taskProgressNote(env); note != "" {
				_ = conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: sid, Update: acp.UpdateToolCall(acp.ToolCallId(toolCallID),
					acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock(note))}),
				)})
			}
		}

	case "task_notification":
		if toolCallID := p.subagentParent(env.TaskID, env.ToolUseID); toolCallID != "" && strings.TrimSpace(env.Summary) != "" {
			_ = conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: sid, Update: acp.UpdateToolCall(acp.ToolCallId(toolCallID),
				acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock(strings.TrimSpace(env.Summary)))}),
			)})
		}
		p.subagentMu.Lock()
		delete(p.subagentParents, env.TaskID)
		p.subagentMu.Unlock()
	case "task_updated":
		if env.Patch.Status == "completed" || env.Patch.Status == "failed" || env.Patch.Status == "killed" {
			p.subagentMu.Lock()
			delete(p.subagentParents, env.TaskID)
			p.subagentMu.Unlock()
		}
	}
}

func (p *claudeProc) handleToolProgress(ctx context.Context, conn *acp.AgentSideConnection, sid acp.SessionId, env cliEnvelope) {
	toolCallID := env.ToolUseID
	if !p.emitted.has(toolCallID) {
		toolCallID = env.ParentToolUseID
	}
	if !p.emitted.has(toolCallID) {
		return
	}
	name := env.ToolName
	if name == "" {
		name = p.tools[toolCallID]
	}
	update := acp.UpdateToolCall(acp.ToolCallId(toolCallID), acp.WithUpdateStatus(acp.ToolCallStatusInProgress))
	claudeMeta := map[string]any{"toolName": name, "toolResponse": map[string]any{"elapsedTimeSeconds": env.ElapsedTimeSeconds}}
	if env.SubagentType != "" {
		claudeMeta["toolResponse"].(map[string]any)["subagentType"] = env.SubagentType
	}
	if env.SubagentRetry != nil {
		claudeMeta["toolResponse"].(map[string]any)["subagentRetry"] = env.SubagentRetry
	}
	if update.ToolCallUpdate != nil {
		update.ToolCallUpdate.Meta = map[string]any{"claudeCode": claudeMeta}
	}
	_ = acpcommon.Notify(ctx, conn, sid, update)
}

func (p *claudeProc) applyFallbackModel(ctx context.Context, conn *acp.AgentSideConnection, sid acp.SessionId, fallback string) {
	modelID := fallback
	if m := resolveModel(p.models, fallback); m != nil {
		modelID = m.ID
	}

	p.session.mu.Lock()
	p.session.modelID = modelID
	p.session.modelOverride = false
	if m := findModel(p.models, modelID); m != nil && !acpcommon.IsValidEffort(m.EffortLevels, p.session.effort) {
		p.session.effort = "default"
	}
	p.sig = p.session.spawnSigLocked()
	effort := p.session.effort
	p.session.mu.Unlock()

	_ = acpcommon.Notify(ctx, conn, sid, acp.SessionUpdate{ConfigOptionUpdate: &acp.SessionConfigOptionUpdate{
		SessionUpdate: "config_option_update",
		ConfigOptions: buildConfigOptions(p.models, modelID, effort),
	}})
}

func (p *claudeProc) applyMode(ctx context.Context, conn *acp.AgentSideConnection, sid acp.SessionId, modeID string) {
	if findMode(modeID) == nil {
		return
	}
	p.session.mu.Lock()
	changed := p.session.mode != modeID
	p.session.mode = modeID
	p.sig = p.session.spawnSigLocked()
	p.session.mu.Unlock()
	if !changed {
		return
	}
	_ = acpcommon.Notify(ctx, conn, sid, acp.SessionUpdate{CurrentModeUpdate: &acp.SessionCurrentModeUpdate{
		SessionUpdate: "current_mode_update",
		CurrentModeId: acp.SessionModeId(modeID),
	}})
}

func resultToTurn(line []byte) (turnResult, *acp.SessionUpdate) {
	var r cliResult
	_ = json.Unmarshal(line, &r)

	tr := resultOutcome(r)
	tr.usage = resultUsage(r)
	return tr, usageUpdate(r, tr.usage)
}

func resultOutcome(r cliResult) turnResult {
	if strings.Contains(r.Result, "Please run /login") {
		return turnResult{err: acp.NewAuthRequired(nil)}
	}
	switch r.Subtype {
	case "success", "error_during_execution":
		if r.StopReason == "max_tokens" {
			return turnResult{stop: acp.StopReasonMaxTokens}
		}
		if r.StopReason == "refusal" {
			return turnResult{stop: acp.StopReasonRefusal}
		}
		if r.IsError {
			return turnResult{err: acp.NewInternalError(resultErrMessage(r))}
		}
		return turnResult{stop: acp.StopReasonEndTurn}
	case "error_max_budget_usd", "error_max_turns", "error_max_structured_output_retries":
		if r.IsError {
			return turnResult{err: acp.NewInternalError(resultErrMessage(r))}
		}
		return turnResult{stop: acp.StopReasonMaxTurnRequests}
	default:
		if r.IsError {
			return turnResult{err: acp.NewInternalError(resultErrMessage(r))}
		}
		return turnResult{stop: acp.StopReasonEndTurn}
	}
}

func resultUsage(r cliResult) *acp.Usage {
	if r.Usage == nil {
		return nil
	}
	u := *r.Usage
	cacheRead, cacheWrite := u.CacheReadInputTokens, u.CacheCreationInputTokens
	return &acp.Usage{
		InputTokens:       u.InputTokens,
		OutputTokens:      u.OutputTokens,
		CachedReadTokens:  &cacheRead,
		CachedWriteTokens: &cacheWrite,
		TotalTokens:       u.InputTokens + u.OutputTokens + cacheRead + cacheWrite,
	}
}

func usageUpdate(r cliResult, usage *acp.Usage) *acp.SessionUpdate {
	if usage == nil {
		return nil
	}
	size := 0
	for _, mu := range r.ModelUsage {
		if mu.ContextWindow > size {
			size = mu.ContextWindow
		}
	}
	if size == 0 {
		return nil
	}
	upd := &acp.SessionUsageUpdate{SessionUpdate: "usage_update", Used: usage.TotalTokens, Size: size}
	if r.TotalCostUSD > 0 {
		upd.Cost = &acp.Cost{Amount: r.TotalCostUSD, Currency: "USD"}
	}
	return &acp.SessionUpdate{UsageUpdate: upd}
}

func resultErrMessage(r cliResult) string {
	if msg := strings.Join(r.Errors, ", "); msg != "" {
		return msg
	}
	if r.Result != "" {
		return r.Result
	}
	return r.Subtype
}

func interruptRequest() controlInterrupt {
	return controlInterrupt{
		Type:      "control_request",
		RequestID: uuid.NewString(),
		Request:   controlInterruptBody{Subtype: "interrupt"},
	}
}

type streamWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *streamWriter) writeJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.w.Write(append(b, '\n'))
	return err
}

func (s *session) cliArgsLocked() []string {

	args := []string{
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--permission-prompt-tool", "stdio",

		"--settings", `{"disableRemoteControl":true}`,
	}
	switch {
	case s.started:
		args = append(args, "--resume", string(s.id))
	case s.forkOnResume:
		args = append(args,
			"--resume", s.resumeFrom,
			"--session-id", string(s.id),
			"--fork-session",
		)
	case s.resumeFrom != "":
		args = append(args, "--resume", s.resumeFrom)
	default:
		args = append(args, "--session-id", string(s.id))
	}
	for _, d := range s.additionalDirs {
		args = append(args, "--add-dir", d)
	}
	if cfg := mcpConfigJSON(s.mcpServers); cfg != "" {
		args = append(args, "--mcp-config", cfg)
	}
	if s.modelID != "" && s.modelID != "default" && (s.resumeFrom == "" || s.modelOverride) {
		args = append(args, "--model", s.modelID)
	}
	if s.effort != "" && s.effort != "default" {
		args = append(args, "--effort", s.effort)
	}
	mode := findMode(s.mode)
	if mode == nil {
		mode = findMode(defaultModeID)
	}
	args = append(args, "--permission-mode", mode.permissionMode)
	return args
}

func promptMessage(blocks []acp.ContentBlock) cliInput {
	in := cliInput{Type: "user", Message: cliInputMessage{Role: "user"}}
	add := func(c cliInputContent) { in.Message.Content = append(in.Message.Content, c) }
	var contextBlocks []cliInputContent
	for _, b := range blocks {
		switch {
		case b.Text != nil:
			add(cliInputContent{Type: "text", Text: b.Text.Text})
		case b.Image != nil:
			switch {
			case b.Image.Data != "":
				add(cliInputContent{Type: "image", Source: &cliInputImageSource{
					Type:      "base64",
					MediaType: b.Image.MimeType,
					Data:      b.Image.Data,
				}})
			case b.Image.Uri != nil && (strings.HasPrefix(*b.Image.Uri, "http://") || strings.HasPrefix(*b.Image.Uri, "https://")):
				add(cliInputContent{Type: "image", Source: &cliInputImageSource{Type: "url", URL: *b.Image.Uri}})
			}
		case b.ResourceLink != nil:
			add(cliInputContent{Type: "text", Text: formatResourceLink(b.ResourceLink.Name, b.ResourceLink.Uri)})
		case b.Resource != nil:
			resource := b.Resource.Resource
			switch {
			case resource.TextResourceContents != nil:
				r := resource.TextResourceContents
				add(cliInputContent{Type: "text", Text: formatResourceLink("", r.Uri)})
				contextBlocks = append(contextBlocks, cliInputContent{Type: "text", Text: fmt.Sprintf("\n<context ref=%q>\n%s\n</context>", r.Uri, r.Text)})
			case resource.BlobResourceContents != nil:
				r := resource.BlobResourceContents
				mimeType := "application/octet-stream"
				if r.MimeType != nil && *r.MimeType != "" {
					mimeType = *r.MimeType
				}
				if strings.HasPrefix(strings.ToLower(mimeType), "image/") && r.Blob != "" {
					add(cliInputContent{Type: "image", Source: &cliInputImageSource{
						Type: "base64", MediaType: mimeType, Data: r.Blob,
					}})
					break
				}
				add(cliInputContent{Type: "text", Text: formatResourceLink("", r.Uri)})
				contextBlocks = append(contextBlocks, cliInputContent{Type: "text", Text: fmt.Sprintf(
					"\n<context ref=%q mimeType=%q encoding=%q>\n%s\n</context>", r.Uri, mimeType, "base64", r.Blob,
				)})
			}
		}
	}
	in.Message.Content = append(in.Message.Content, contextBlocks...)
	return in
}

func formatResourceLink(name, uri string) string {
	if name != "" {
		return fmt.Sprintf("[@%s](%s)", name, uri)
	}
	if path, ok := strings.CutPrefix(uri, "file://"); ok {
		return fmt.Sprintf("[@%s](%s)", filepath.Base(path), uri)
	}
	return uri
}
