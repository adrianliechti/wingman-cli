package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"
)

type session struct {
	id  acp.SessionId
	cwd string

	formElicitation bool

	mu             sync.Mutex
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

func newSession(id acp.SessionId, cwd, model, effort string, additionalDirs []string) *session {
	return &session{
		id:             id,
		cwd:            cwd,
		modelID:        model,
		modelOverride:  model != "" && model != "default",
		effort:         effort,
		mode:           defaultModeID,
		additionalDirs: append([]string(nil), additionalDirs...),
	}
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

func (s *session) runTurn(ctx context.Context, conn *acp.AgentSideConnection, path string, env []string, models []ModelEntry, prompt []acp.ContentBlock) (acp.StopReason, *acp.Usage, error) {
	p, err := s.ensureProc(conn, path, env, models)
	if err != nil {
		return "", nil, err
	}

	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.cancel = nil
		s.mu.Unlock()
	}()

	p.beginTurn()
	if err := p.out.writeJSON(promptMessage(prompt)); err != nil {
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
		s.pushTitleUpdate(ctx, conn)
		return r.stop, r.usage, nil
	case <-p.dead:
		s.dropProc(p)
		return "", nil, fmt.Errorf("claude process exited unexpectedly")
	}
}

// pushTitleUpdate notifies the client when the CLI's auto-generated session
// title has changed since the last time we looked. The CLI has no push event
// for it — it's regenerated in the background and persisted to the session's
// JSONL file — so we read it back at turn end, the same point a new title
// would have landed, and only notify when it actually changed.
func (s *session) pushTitleUpdate(ctx context.Context, conn *acp.AgentSideConnection) {
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
	_ = conn.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: s.id,
		Update: acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{
			SessionUpdate: "session_info_update",
			Title:         &t,
			UpdatedAt:     &updatedAt,
		}},
	})
}

func (s *session) ensureProc(conn *acp.AgentSideConnection, path string, env []string, models []ModelEntry) (*claudeProc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

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
	cmd := exec.CommandContext(procCtx, path, args...)
	cmd.Dir = s.cwd
	if env != nil {
		cmd.Env = env
	}
	cmd.Stderr = os.Stderr
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
		out:             &streamWriter{w: stdin},
		stdin:           stdin,
		sig:             sig,
		kill:            kill,
		cwd:             s.cwd,
		session:         s,
		models:          append([]ModelEntry(nil), models...),
		tools:           toolUseCache{},
		emitted:         newToolCallTracker(),
		streamedMsgs:    map[string]bool{},
		plan:            newTaskPlan(),
		subagentParents: make(map[string]string),
		results:         make(chan turnResult, 1),
		dead:            make(chan struct{}),
	}
	go p.read(procCtx, conn, s.id, stdout)

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
	cmd          *exec.Cmd
	out          *streamWriter
	stdin        io.Closer
	sig          string
	kill         context.CancelFunc
	cwd          string
	session      *session
	models       []ModelEntry
	tools        toolUseCache
	emitted      *toolCallTracker
	streamedMsgs map[string]bool
	plan         *taskPlan

	turnMu          sync.Mutex
	turnActive      bool
	subagentMu      sync.Mutex
	subagentParents map[string]string
	results         chan turnResult
	dead            chan struct{}
}

func (p *claudeProc) beginTurn() {
	p.turnMu.Lock()
	p.turnActive = true
	p.turnMu.Unlock()
}

func (p *claudeProc) finishTurn() bool {
	p.turnMu.Lock()
	defer p.turnMu.Unlock()
	wasActive := p.turnActive
	p.turnActive = false
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

	_ = p.stdin.Close()
	select {
	case <-p.dead:
	case <-time.After(5 * time.Second):
		p.kill()
		<-p.dead
	}
	_ = p.cmd.Wait()
}

func (p *claudeProc) read(ctx context.Context, conn *acp.AgentSideConnection, sid acp.SessionId, r io.Reader) {
	defer close(p.dead)
	app := &approver{ctx: ctx, conn: conn, sid: sid, out: p.out, cwd: p.cwd, emitted: p.emitted, parentForAgent: p.parentForAgent,
		askForm:   p.session.formElicitation,
		applyMode: func(modeID string) { p.applyMode(ctx, conn, sid, modeID) }}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var env cliEnvelope
		if err := json.Unmarshal(line, &env); err != nil {
			fmt.Fprintf(os.Stderr, "claude-acp: skipping non-JSON line: %s\n", line)
			continue
		}
		switch env.Type {
		case "stream_event":
			if env.ParentToolUseID != "" {
				continue
			}
			if err := emitStreamEvent(ctx, conn, sid, env.Event, p.streamedMsgs); err != nil {
				fmt.Fprintf(os.Stderr, "claude-acp: emit stream event: %v\n", err)
			}
		case "assistant":
			if err := emitAssistant(ctx, conn, sid, env.Message, p.cwd, p.tools, p.emitted, p.streamedMsgs, p.plan, env.ParentToolUseID); err != nil {
				fmt.Fprintf(os.Stderr, "claude-acp: emit assistant: %v\n", err)
			}
		case "user":
			if err := emitToolResults(ctx, conn, sid, env.Message, p.tools, p.plan, env.ParentToolUseID); err != nil {
				fmt.Fprintf(os.Stderr, "claude-acp: emit tool result: %v\n", err)
			}
		case "control_request":
			var req controlRequest
			if json.Unmarshal(line, &req) == nil {
				go app.handle(req)
			}
		case "result":
			tr, usageUpd := resultToTurn(line)
			p.finishTurn()
			if usageUpd != nil {
				_ = conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: sid, Update: *usageUpd})
			}
			select {
			case p.results <- tr:
			default:
			}
		case "system":
			p.handleSystem(ctx, conn, sid, env)
		}
	}
}

func (p *claudeProc) handleSystem(ctx context.Context, conn *acp.AgentSideConnection, sid acp.SessionId, env cliEnvelope) {
	switch env.Subtype {
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
		_ = conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: sid, Update: acp.UpdateAgentMessageText(message)})
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
			_ = conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: sid, Update: acp.UpdateAgentMessageText(text)})
		}

	case "local_command_output":
		var out string
		if json.Unmarshal(env.Content, &out) == nil && strings.TrimSpace(out) != "" {
			_ = conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: sid, Update: acp.UpdateAgentMessageText(out)})
		}

	case "permission_denied":
		if env.ToolUseID != "" {
			msg := "Permission denied"
			var detail string
			if json.Unmarshal(env.Message, &detail) == nil && detail != "" {
				msg = "Permission denied: " + detail
			}
			_ = conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: sid, Update: acp.UpdateToolCall(acp.ToolCallId(env.ToolUseID),
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
	case "task_notification":
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

func (p *claudeProc) applyFallbackModel(ctx context.Context, conn *acp.AgentSideConnection, sid acp.SessionId, fallback string) {
	modelID := fallback
	if m := resolveModel(p.models, fallback); m != nil {
		modelID = m.ID
	}

	p.session.mu.Lock()
	p.session.modelID = modelID
	p.session.modelOverride = false
	if m := findModel(p.models, modelID); m != nil && !isValidEffort(m, p.session.effort) {
		p.session.effort = "default"
	}
	p.sig = p.session.spawnSigLocked()
	effort := p.session.effort
	p.session.mu.Unlock()

	_ = conn.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: sid,
		Update: acp.SessionUpdate{ConfigOptionUpdate: &acp.SessionConfigOptionUpdate{
			SessionUpdate: "config_option_update",
			ConfigOptions: buildConfigOptions(p.models, modelID, effort),
		}},
	})
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
	_ = conn.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: sid,
		Update: acp.SessionUpdate{CurrentModeUpdate: &acp.SessionCurrentModeUpdate{
			SessionUpdate: "current_mode_update",
			CurrentModeId: acp.SessionModeId(modeID),
		}},
	})
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
		RequestID: newUUID(),
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
	mode := s.mode
	if mode == "" {
		mode = defaultModeID
	}
	args = append(args, "--permission-mode", mode)
	return args
}

func promptMessage(blocks []acp.ContentBlock) cliInput {
	in := cliInput{Type: "user", Message: cliInputMessage{Role: "user"}}
	add := func(c cliInputContent) { in.Message.Content = append(in.Message.Content, c) }
	for _, b := range blocks {
		switch {
		case b.Text != nil:
			add(cliInputContent{Type: "text", Text: b.Text.Text})
		case b.Image != nil && b.Image.Data != "":
			add(cliInputContent{Type: "image", Source: &cliImageSource{
				Type:      "base64",
				MediaType: b.Image.MimeType,
				Data:      b.Image.Data,
			}})
		case b.ResourceLink != nil:
			add(cliInputContent{Type: "text", Text: fmt.Sprintf("[@%s](%s)", b.ResourceLink.Name, b.ResourceLink.Uri)})
		case b.Resource != nil && b.Resource.Resource.TextResourceContents != nil:
			r := b.Resource.Resource.TextResourceContents
			add(cliInputContent{Type: "text", Text: fmt.Sprintf("\n<context ref=%q>\n%s\n</context>", r.Uri, r.Text)})
		}
	}
	return in
}
