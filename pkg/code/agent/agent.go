package agent

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"iter"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	harness "github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/hook/external"
	"github.com/adrianliechti/wingman-agent/pkg/agent/hook/truncation"
	"github.com/adrianliechti/wingman-agent/pkg/agent/task"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	elicittool "github.com/adrianliechti/wingman-agent/pkg/agent/tool/elicit"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/fetch"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/fs"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/shell"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/subagent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/todo"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/websearch"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/adrianliechti/wingman-agent/pkg/code/prompt"
	"github.com/adrianliechti/wingman-agent/pkg/model"
	"github.com/adrianliechti/wingman-agent/pkg/session"
	skillpkg "github.com/adrianliechti/wingman-agent/pkg/skill"
	"github.com/adrianliechti/wingman-agent/pkg/text"
)

type Agent struct {
	workspace *code.Workspace
	cfg       *harness.Config

	uiMu    sync.RWMutex
	ui      code.UI
	prompts *tool.Elicitation

	lastActive atomic.Value

	sessionsDir string

	modelMu        sync.Mutex
	modelID        string
	planModelID    string
	utilityModelID string
	effortID       string
	planEffortID   string
	upstreamModels map[string]bool

	mu       sync.Mutex
	sessions map[string]*sessionState
	closed   bool
}

type sessionState struct {
	parent *Agent
	aa     *harness.Agent

	modelID      string
	planModelID  string
	effortID     string
	planEffortID string

	planMode    atomic.Bool
	baseTools   []tool.Tool
	execManager *shell.ExecManager
	tasks       *task.Registry

	freshness *fs.Freshness
	watchStop chan struct{}

	changedMu    sync.Mutex
	changedPaths []string

	projectInstructionsMu     sync.Mutex
	projectInstructionsCache  string
	projectInstructionsMtimes map[string]time.Time

	cancelMu  sync.Mutex
	cancelFn  context.CancelFunc
	cancelGen uint64
	closed    bool
}

func New(ws *code.Workspace, cfg *harness.Config, ui code.UI) *Agent {
	a := &Agent{
		workspace:      ws,
		cfg:            cfg,
		ui:             ui,
		modelID:        harness.DefaultModel(),
		planModelID:    harness.DefaultPlanModel(),
		utilityModelID: harness.DefaultUtilityModel(),
		effortID:       harness.DefaultEffort(),
		planEffortID:   harness.DefaultPlanEffort(),
		sessionsDir:    filepath.Join(filepath.Dir(ws.MemoryPath), "sessions"),
		sessions:       map[string]*sessionState{},
	}
	a.prompts = &tool.Elicitation{Elicit: a.elicit, Confirm: a.confirm}

	// MCP servers elicit through the same UI surface as the elicit tool. Their
	// requests arrive on the transport context, which carries no session, so
	// route them to the most recently active session (or any live one before
	// the first turn) for display.
	if ws.MCP != nil {
		ws.MCP.SetElicit(a.elicit)
	}

	return a
}

func (a *Agent) anySessionID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	for id := range a.sessions {
		return id
	}
	return ""
}

func (a *Agent) Name() string               { return code.BuiltinAgentName }
func (a *Agent) Workspace() *code.Workspace { return a.workspace }

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

func (a *Agent) Models(sessionID string) ([]model.Model, string) {
	return a.modelsFor(a.session(sessionID))
}

func (a *Agent) modelsFor(s *sessionState) ([]model.Model, string) {
	a.modelMu.Lock()
	defer a.modelMu.Unlock()
	return a.modelsLocked(s)
}

func (a *Agent) modelsLocked(s *sessionState) ([]model.Model, string) {
	available := model.Available(a.upstreamModels)

	planMode := s != nil && s.planMode.Load()

	// Model choices are role-scoped: plan mode never inherits the coding
	// model — an unset plan model selects a large one automatically.
	current := ""
	if planMode {
		current = a.planModelID
		if s.planModelID != "" {
			current = s.planModelID
		}
	} else {
		current = a.modelID
		if s != nil && s.modelID != "" {
			current = s.modelID
		}
	}

	if current == "" {
		class := model.ClassMedium
		if planMode {
			class = model.ClassLarge
		}
		current = a.classModelLocked(class)
	}

	if len(available) > 0 && !slices.ContainsFunc(available, func(m model.Model) bool { return m.ID == current }) {
		current = available[0].ID
	}
	return available, current
}

// classModelLocked returns the first available model of the wanted class,
// preferring the family of the medium (coding) pick so plan/code switches
// keep encrypted reasoning replayable.
func (a *Agent) classModelLocked(class model.Class) string {
	pick := func(class model.Class, family string) string {
		for _, m := range model.Available(a.upstreamModels) {
			if m.Class != class {
				continue
			}
			if family != "" && model.Family(m.ID) != family {
				continue
			}
			return m.ID
		}
		return ""
	}

	family := ""
	if anchor := pick(model.ClassMedium, ""); anchor != "" {
		family = model.Family(anchor)
	}

	if id := pick(class, family); id != "" {
		return id
	}
	return pick(class, "")
}

// utilityModel returns the model for internal utility calls (recaps,
// compaction summaries): the smallest available, or empty for the main model.
func (a *Agent) utilityModel() string {
	a.modelMu.Lock()
	defer a.modelMu.Unlock()
	if a.utilityModelID != "" {
		return a.utilityModelID
	}
	return a.classModelLocked(model.ClassSmall)
}

// subagentRoleModel resolves the model roles the agent tool may launch
// subagents on: "plan" and "utility" follow the same explicit-override-else-
// class-pick logic as the session roles, "" names the currently inherited
// model so effort overrides clamp against its bounds.
func (a *Agent) subagentRoleModel(s *sessionState, role string) (harness.ModelOption, bool) {
	id := ""
	switch role {
	case "plan":
		a.modelMu.Lock()
		id = a.planModelID
		if s.planModelID != "" {
			id = s.planModelID
		}
		if id == "" {
			id = a.classModelLocked(model.ClassLarge)
		}
		a.modelMu.Unlock()
	case "utility":
		id = a.utilityModel()
	case "":
		_, id = a.modelsFor(s)
	}

	if id == "" {
		return harness.ModelOption{}, false
	}

	lowest, highest := model.EffortBounds(id)
	return harness.ModelOption{ID: id, MinEffort: lowest, MaxEffort: highest}, true
}

// SetModel applies to the session's current role: picking a model while in
// plan mode configures planning, otherwise coding.
func (a *Agent) SetModel(_ context.Context, sessionID, id string) error {
	s := a.session(sessionID)
	a.modelMu.Lock()
	if s != nil && s.planMode.Load() {
		a.planModelID = id
		s.planModelID = id
	} else {
		a.modelID = id
		if s != nil {
			s.modelID = id
		}
	}
	a.modelMu.Unlock()
	return nil
}

func (a *Agent) FetchModels(ctx context.Context) {
	models, err := a.cfg.Models(ctx)
	if err != nil {
		return
	}
	ids := make(map[string]bool, len(models))
	for _, m := range models {
		ids[m.ID] = true
	}
	a.modelMu.Lock()
	a.upstreamModels = ids
	a.modelMu.Unlock()
}

var effortValues = []string{"auto", "none", "low", "medium", "high", "xhigh", "max"}

func (a *Agent) Effort(sessionID string) (string, []string) {
	current := a.effortFor(a.session(sessionID))
	if current == "" {
		current = "auto"
	}
	return current, slices.Clone(effortValues)
}

func (a *Agent) effortFor(s *sessionState) string {
	a.modelMu.Lock()
	defer a.modelMu.Unlock()
	// Effort choices are role-scoped like models: plan mode never inherits
	// the coding effort.
	if s != nil && s.planMode.Load() {
		if s.planEffortID != "" {
			return s.planEffortID
		}
		if a.planEffortID != "" {
			return a.planEffortID
		}
		// xhigh only where a large model backs it.
		if _, current := a.modelsLocked(s); model.ClassOf(current) == model.ClassLarge {
			return "xhigh"
		}
		return "high"
	}

	if s != nil && s.effortID != "" {
		return s.effortID
	}
	if a.effortID != "" {
		return a.effortID
	}
	return "high"
}

func (a *Agent) SetEffort(_ context.Context, sessionID, value string) error {
	switch value {
	case "", "auto":
		value = ""
	case "none", "low", "medium", "high", "xhigh", "max":
	default:
		return fmt.Errorf("effort must be auto, none, low, medium, high, xhigh, or max (got %q)", value)
	}
	s := a.session(sessionID)
	a.modelMu.Lock()
	if s != nil && s.planMode.Load() {
		a.planEffortID = value
		s.planEffortID = value
	} else {
		a.effortID = value
		if s != nil {
			s.effortID = value
		}
	}
	a.modelMu.Unlock()
	return nil
}

func (a *Agent) ListSessions(_ context.Context) ([]code.SessionInfo, error) {
	saved, err := session.List(a.sessionsDir)
	if err != nil {
		return nil, err
	}
	out := make([]code.SessionInfo, 0, len(saved))
	for _, s := range saved {
		out = append(out, code.SessionInfo{
			ID:        s.ID,
			Title:     s.Title,
			UpdatedAt: s.UpdatedAt,
		})
	}
	return out, nil
}

func (a *Agent) NewSession(_ context.Context) (string, error) {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return "", errors.New("agent is closed")
	}
	a.mu.Unlock()

	id := uuid.NewString()
	s := a.buildSession()
	s.aa.CacheKey = id
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		s.close()
		return "", errors.New("agent is closed")
	}
	a.sessions[id] = s
	a.mu.Unlock()
	return id, nil
}

func (a *Agent) LoadSession(_ context.Context, id string) error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return errors.New("agent is closed")
	}
	if _, ok := a.sessions[id]; ok {
		a.mu.Unlock()
		return nil
	}
	a.mu.Unlock()

	saved, err := session.Load(a.sessionsDir, id)
	if err != nil {
		return err
	}
	s := a.buildSession()
	s.aa.CacheKey = id
	s.aa.Messages = saved.State.Messages
	s.aa.Usage = saved.State.Usage

	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		s.close()
		return errors.New("agent is closed")
	}
	if _, loaded := a.sessions[id]; loaded {
		a.mu.Unlock()
		s.close()
		return nil
	}
	a.sessions[id] = s
	a.mu.Unlock()
	return nil
}

func (a *Agent) DeleteSession(_ context.Context, id string) error {
	a.mu.Lock()
	s, inMem := a.sessions[id]
	if inMem {
		delete(a.sessions, id)
	}
	a.mu.Unlock()

	if inMem {
		s.close()
	}
	if err := session.Delete(a.sessionsDir, id); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (a *Agent) Save(id string) error {
	a.mu.Lock()
	s, ok := a.sessions[id]
	a.mu.Unlock()
	if !ok {
		return nil
	}
	return session.Save(a.sessionsDir, id, s.aa.StateSnapshot())
}

func (a *Agent) SessionsDir() string { return a.sessionsDir }

func (a *Agent) Messages(id string) []harness.Message {
	s := a.session(id)
	if s == nil {
		return nil
	}
	return s.aa.MessagesSnapshot()
}

func (a *Agent) Recap(ctx context.Context, id string) (string, error) {
	s := a.session(id)
	if s == nil {
		return "", fmt.Errorf("session %s not found", id)
	}
	return s.aa.Recap(ctx)
}

func (a *Agent) ContextStats(id string) (harness.ContextStats, bool) {
	s := a.session(id)
	if s == nil {
		return harness.ContextStats{}, false
	}
	return s.aa.ContextStats(), true
}

func (a *Agent) Usage(id string) harness.Usage {
	s := a.session(id)
	if s == nil {
		return harness.Usage{}
	}
	return s.aa.UsageSnapshot()
}

func (a *Agent) session(id string) *sessionState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessions[id]
}

func (a *Agent) HasSession(id string) bool { return a.session(id) != nil }

// Tasks exposes the session's background-agent registry so UI surfaces can
// list running agents and deliver completion notifications.
func (a *Agent) Tasks(id string) *task.Registry {
	s := a.session(id)
	if s == nil {
		return nil
	}
	return s.tasks
}

// RunningTaskCount sums running background agents across every live session,
// so quitting warns about agents outside the currently viewed session too.
func (a *Agent) RunningTaskCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	total := 0
	for _, s := range a.sessions {
		running, _ := s.tasks.Counts()
		total += running
	}
	return total
}

func (a *Agent) Send(ctx context.Context, id string, input []harness.Content) (iter.Seq2[harness.Message, error], error) {
	if len(input) == 0 {
		return nil, code.ErrEmptyInput
	}
	a.mu.Lock()
	s, ok := a.sessions[id]
	a.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("session %s not found; call NewSession first", id)
	}

	a.lastActive.Store(id)

	sendCtx, cancel := context.WithCancel(code.WithSessionID(ctx, id))
	stream, gen, err := s.beginSend(sendCtx, input, cancel)
	if err != nil {
		cancel()
		return nil, err
	}

	return func(yield func(harness.Message, error) bool) {
		defer func() {
			s.clearCancel(gen)
			cancel()
		}()
		for msg, err := range stream {
			if !yield(msg, err) {
				return
			}
		}
	}, nil
}

func (a *Agent) TurnFeatures(string) code.TurnFeatures {
	return code.TurnFeatures{Steer: true}
}

func (a *Agent) Steer(_ context.Context, id string, input code.TurnInput) error {
	s := a.session(id)
	if s == nil {
		return fmt.Errorf("session %s not found", id)
	}
	if !s.aa.QueueInput(input.Content) {
		return code.ErrNoActiveTurn
	}
	return nil
}

func (a *Agent) Cancel(id string) {
	if s := a.session(id); s != nil {
		s.cancel()
	}
}

func (a *Agent) Close() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	sessions := slices.Collect(maps.Values(a.sessions))
	a.sessions = map[string]*sessionState{}
	a.mu.Unlock()
	for _, s := range sessions {
		s.close()
	}
	return nil
}

var wingmanModes = []code.Mode{
	{ID: "agent", Name: "Agent", Description: "Read, edit, and run commands without asking."},
	{ID: "plan", Name: "Plan", Description: "Read-only — proposes a plan, doesn't edit code."},
}

func (a *Agent) Modes(sessionID string) ([]code.Mode, string) {
	out := make([]code.Mode, len(wingmanModes))
	copy(out, wingmanModes)
	current := "agent"
	if s := a.session(sessionID); s != nil && s.planMode.Load() {
		current = "plan"
	}
	return out, current
}

func (a *Agent) SetMode(_ context.Context, sessionID, modeID string) error {
	var plan bool
	switch modeID {
	case "agent":
		plan = false
	case "plan":
		plan = true
	default:
		return fmt.Errorf("unknown mode %q", modeID)
	}
	if s := a.session(sessionID); s != nil {
		s.planMode.Store(plan)
	}
	return nil
}

func (a *Agent) Tools(id string) []tool.Tool {
	s := a.session(id)
	if s == nil {
		return nil
	}
	return s.tools()
}

func (a *Agent) buildSession() *sessionState {
	sessionCfg := a.cfg.Derive()
	s := &sessionState{
		parent: a,
		aa:     &harness.Agent{Config: sessionCfg},
	}
	sessionCfg.Tools = s.tools
	sessionCfg.Instructions = s.instructions
	sessionCfg.Model = func() string {
		_, current := a.modelsFor(s)
		return current
	}
	sessionCfg.Effort = func() string {
		return a.effortFor(s)
	}
	sessionCfg.UtilityModel = a.utilityModel
	sessionCfg.SubagentModel = func(role string) (harness.ModelOption, bool) {
		return a.subagentRoleModel(s, role)
	}
	elicit := a.prompts
	ws := a.workspace

	globalHooks, globalErr := external.Load(userHooksConfigPath())
	workspaceHooksPath := filepath.Join(ws.RootPath, "hooks.json")
	workspaceHooks, workspaceErr := external.Load(workspaceHooksPath)

	// Fail closed: a hook config the user wrote but that no longer parses must
	// not silently disable the guards it configures.
	if err := errors.Join(globalErr, workspaceErr); err != nil {
		message := fmt.Sprintf("hook configuration is invalid; fix or remove it to unblock tools: %v", err)
		sessionCfg.Hooks.PreToolUse = append(sessionCfg.Hooks.PreToolUse,
			func(context.Context, tool.ToolCall) (string, error) { return message, nil },
		)
	}

	// Workspace hooks come with the repo, not the user — gate them behind a
	// one-time confirmation so a cloned project cannot run commands unprompted.
	var workspaceGate *external.Gate
	if rules := workspaceHooks.RuleCount(); rules > 0 {
		workspaceGate = &external.Gate{
			Confirm: elicit.Confirm,
			Message: fmt.Sprintf("Run %d workspace hook rule(s) from %s?", rules, workspaceHooksPath),
		}
	}

	sessionCfg.Hooks.PreToolUse = append(sessionCfg.Hooks.PreToolUse, globalHooks.PreHooks(ws.RootPath, nil)...)
	sessionCfg.Hooks.PreToolUse = append(sessionCfg.Hooks.PreToolUse, workspaceHooks.PreHooks(ws.RootPath, workspaceGate)...)
	sessionCfg.Hooks.PostToolUse = append(sessionCfg.Hooks.PostToolUse,
		truncation.New(ws.ScratchPath),
	)
	sessionCfg.Hooks.PostToolUse = append(sessionCfg.Hooks.PostToolUse, globalHooks.PostHooks(ws.RootPath, nil)...)
	sessionCfg.Hooks.PostToolUse = append(sessionCfg.Hooks.PostToolUse, workspaceHooks.PostHooks(ws.RootPath, workspaceGate)...)

	for _, cfg := range []struct {
		hooks *external.Config
		gate  *external.Gate
	}{{globalHooks, nil}, {workspaceHooks, workspaceGate}} {
		sessionCfg.Hooks.UserPromptSubmit = append(sessionCfg.Hooks.UserPromptSubmit, cfg.hooks.PromptHooks(ws.RootPath, cfg.gate)...)
		sessionCfg.Hooks.SessionStart = append(sessionCfg.Hooks.SessionStart, cfg.hooks.StartHooks(ws.RootPath, cfg.gate)...)
		sessionCfg.Hooks.SessionEnd = append(sessionCfg.Hooks.SessionEnd, cfg.hooks.EndHooks(ws.RootPath, cfg.gate)...)
		sessionCfg.Hooks.SubagentStop = append(sessionCfg.Hooks.SubagentStop, cfg.hooks.SubagentHooks(ws.RootPath, cfg.gate)...)
		sessionCfg.Hooks.PreCompact = append(sessionCfg.Hooks.PreCompact, cfg.hooks.CompactHooks(ws.RootPath, cfg.gate)...)
	}

	var allowedReadRoots []string
	for _, sk := range ws.Skills {
		if sk.Location != "" && filepath.IsAbs(sk.Location) {
			allowedReadRoots = append(allowedReadRoots, sk.Location)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		allowedReadRoots = append(allowedReadRoots, filepath.Join(home, ".wingman", "skills"))
	}
	allowedReadRoots = append(allowedReadRoots, ws.ScratchPath)

	var allowedWriteRoots []string
	if ws.MemoryPath != "" {
		allowedReadRoots = append(allowedReadRoots, ws.MemoryPath)
		allowedWriteRoots = append(allowedWriteRoots, ws.MemoryPath)
	}

	// Agents stage scratch files (downscaled images, montages, compile helpers,
	// intermediate outputs) under the OS temp dir. Allow the file tools to both
	// read and write it — the shell tool can already write there, so restricting
	// only the dedicated tools just pushes work onto shell.
	allowedReadRoots = append(allowedReadRoots, os.TempDir())
	allowedWriteRoots = append(allowedWriteRoots, os.TempDir())

	// WINGMAN_SANDBOX=off lifts the workspace path restriction entirely so the
	// file tools reach the whole filesystem like the shell tool already does —
	// e.g. reading and editing /etc configs on system-administration tasks. "*"
	// is the wildcard root the fs matcher treats as "any absolute path": a
	// platform-agnostic marker, avoiding a fragile per-OS filesystem-root path.
	if harness.SandboxDisabled() {
		allowedReadRoots = append(allowedReadRoots, "*")
		allowedWriteRoots = append(allowedWriteRoots, "*")
	}

	s.tasks = task.NewRegistry()
	s.execManager = shell.NewExecManager(func(e shell.ExecExit) {
		s.tasks.Publish(execExitEvent(e))
	})
	s.freshness = fs.NewFreshness(ws.Root)
	s.watchStop = make(chan struct{})
	approvals := shell.NewApprovals()

	sessionCfg.Hooks.UserPromptSubmit = append(sessionCfg.Hooks.UserPromptSubmit,
		func(context.Context, string) (string, error) {
			s.sweepFileChanges()
			return formatFileChangeNotice(s.takeFileChanges()), nil
		},
	)

	go s.watchFileChanges()

	s.baseTools = slices.Concat(
		fs.Tools(ws.Root, &fs.Options{
			AllowedReadRoots:  allowedReadRoots,
			AllowedWriteRoots: allowedWriteRoots,
			Freshness:         s.freshness,
		}),
		shell.Tools(ws.RootPath, elicit, approvals),
		shell.ExecTools(s.execManager, ws.RootPath, elicit, approvals),
		todo.Tools(),
		elicittool.Tools(elicit),
		fetch.Tools(elicit, sessionCfg.Utility),
		websearch.Tools(elicit),
		subagent.Tools(sessionCfg, s.subagentContext, s.tasks),
	)
	return s
}

// execExitNotifyLimit caps how much trailing output of a backgrounded command
// an exit notification injects into the conversation.
const execExitNotifyLimit = 16 * 1024

func execExitEvent(e shell.ExecExit) task.Event {
	description := e.Description
	if description == "" {
		description = text.TruncateHead(e.Command, 80)
	}

	status := task.StatusDone
	if e.Failed {
		status = task.StatusFailed
	}

	result := e.Notice
	if out := strings.TrimSpace(e.Output); out != "" {
		if len(out) > execExitNotifyLimit {
			out = "[earlier output truncated]\n" + text.TailBytes(out, execExitNotifyLimit)
		}
		result += "\n\nOutput:\n" + out
	}

	return task.Event{
		ID:          fmt.Sprintf("exec-%d", e.SessionID),
		Kind:        task.KindCommand,
		AgentType:   "exec",
		Description: description,
		Seq:         1,
		Status:      status,
		Result:      result,
		Elapsed:     e.Elapsed,
	}
}

func userHooksConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".wingman", "hooks.json")
}

func (a *Agent) promptContext(ctx context.Context) context.Context {
	if code.SessionIDFromContext(ctx) != "" {
		return ctx
	}

	sid, _ := a.lastActive.Load().(string)
	if sid != "" && a.session(sid) == nil {
		sid = ""
	}
	if sid == "" {
		sid = a.anySessionID()
	}
	if sid == "" {
		return ctx
	}
	return code.WithSessionID(ctx, sid)
}

func (a *Agent) elicit(ctx context.Context, req tool.ElicitRequest) (tool.ElicitResult, error) {
	ui := a.currentUI()
	if ui == nil {
		return tool.ElicitResult{Action: tool.ElicitCancel}, nil
	}
	return ui.Elicit(a.promptContext(ctx), req)
}

func (a *Agent) confirm(ctx context.Context, message string) (bool, error) {
	ui := a.currentUI()
	if ui == nil {
		return false, nil
	}
	return ui.Confirm(a.promptContext(ctx), message)
}

func (s *sessionState) beginSend(ctx context.Context, input []harness.Content, cancel context.CancelFunc) (iter.Seq2[harness.Message, error], uint64, error) {
	s.cancelMu.Lock()
	if s.closed {
		s.cancelMu.Unlock()
		return nil, 0, errors.New("session is closed")
	}
	stream, err := s.aa.Send(ctx, input)
	if err != nil {
		s.cancelMu.Unlock()
		return nil, 0, err
	}
	if stream == nil {
		s.cancelMu.Unlock()
		return nil, 0, errors.New("agent returned a nil turn stream")
	}
	prev := s.cancelFn
	s.cancelGen++
	gen := s.cancelGen
	s.cancelFn = cancel
	s.cancelMu.Unlock()
	if prev != nil {
		prev()
	}
	return stream, gen, nil
}

func (s *sessionState) clearCancel(gen uint64) {
	s.cancelMu.Lock()
	if s.cancelGen == gen {
		s.cancelFn = nil
	}
	s.cancelMu.Unlock()
}

func (s *sessionState) cancel() {
	s.cancelMu.Lock()
	fn := s.cancelFn
	s.cancelMu.Unlock()
	if fn != nil {
		fn()
	}
}

func (s *sessionState) close() {
	s.cancelMu.Lock()
	if s.closed {
		s.cancelMu.Unlock()
		return
	}
	s.closed = true
	s.cancelGen++
	fn := s.cancelFn
	s.cancelFn = nil
	s.cancelMu.Unlock()
	if fn != nil {
		fn()
	}
	close(s.watchStop)
	s.tasks.Close()
	s.execManager.Close()

	if hooks := s.aa.Hooks.SessionEnd; len(hooks) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, h := range hooks {
			h(ctx)
		}
	}
}

// watchFileChanges announces external file modifications mid-turn: while a
// turn runs, tracked files that changed outside the session's file tools are
// reported to the model as hidden context at the next model boundary. Idle
// periods are swept lazily by the UserPromptSubmit hook instead.
func (s *sessionState) watchFileChanges() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.watchStop:
			return
		case <-ticker.C:
			if !s.aa.Running() {
				continue
			}
			s.sweepFileChanges()
			paths := s.takeFileChanges()
			if len(paths) == 0 {
				continue
			}
			notice := formatFileChangeNotice(paths)
			if !s.aa.QueueInput([]harness.Content{{Text: notice, Hidden: true}}) {
				s.stashFileChanges(paths)
			}
		}
	}
}

func (s *sessionState) sweepFileChanges() {
	if changed := s.freshness.Changed(); len(changed) > 0 {
		s.stashFileChanges(changed)
	}
}

func (s *sessionState) stashFileChanges(paths []string) {
	s.changedMu.Lock()
	for _, p := range paths {
		if !slices.Contains(s.changedPaths, p) {
			s.changedPaths = append(s.changedPaths, p)
		}
	}
	s.changedMu.Unlock()
}

const fileChangeNoticeMax = 20

func (s *sessionState) takeFileChanges() []string {
	s.changedMu.Lock()
	paths := s.changedPaths
	s.changedPaths = nil
	s.changedMu.Unlock()
	return paths
}

func formatFileChangeNotice(paths []string) string {
	if len(paths) == 0 {
		return ""
	}

	listed := paths
	suffix := ""
	if len(listed) > fileChangeNoticeMax {
		suffix = fmt.Sprintf(" (and %d more)", len(listed)-fileChangeNoticeMax)
		listed = listed[:fileChangeNoticeMax]
	}

	return fmt.Sprintf("<system-reminder>These files changed on disk outside this session's file tools (edited by the user, a linter, or a shell command): %s%s. Your memory of their content is stale — re-read them before editing, take the external changes into account, and never revert them.</system-reminder>", strings.Join(listed, ", "), suffix)
}

func (s *sessionState) tools() []tool.Tool {
	tools := append([]tool.Tool{}, s.baseTools...)
	mcpTools, lspTools, graphTools := s.parent.workspace.ManagedTools()
	tools = append(tools, mcpTools...)
	tools = append(tools, lspTools...)
	tools = append(tools, graphTools...)
	if s.planMode.Load() {
		tools = planModeTools(tools)
	}
	slices.SortStableFunc(tools, func(a, b tool.Tool) int { return cmp.Compare(a.Name, b.Name) })
	return tools
}

func planModeTools(tools []tool.Tool) []tool.Tool {
	filtered := make([]tool.Tool, 0, len(tools))
	for _, t := range tools {
		if t.Effect == nil {
			continue
		}
		switch t.Effect(nil) {
		case tool.EffectReadOnly:
			filtered = append(filtered, t)
		case tool.EffectDynamic:
			t.Execute = planModeEffectExecute(t)
			filtered = append(filtered, t)
		}
	}
	return filtered
}

func planModeEffectExecute(t tool.Tool) func(context.Context, map[string]any) (string, error) {
	return func(ctx context.Context, args map[string]any) (string, error) {
		if t.Effect == nil || t.Effect(args) != tool.EffectReadOnly {
			return "", fmt.Errorf("plan mode only allows read-only tool calls")
		}
		return t.Execute(ctx, args)
	}
}

func (s *sessionState) instructions() string {
	return BuildInstructions(s.instructionsData())
}

func (s *sessionState) subagentContext() string {
	return prompt.BuildAgentContext(s.instructionsData())
}

func BuildInstructions(data prompt.SectionData) string {
	base := prompt.Instructions
	if data.PlanMode {
		base = prompt.Planning
	}
	return prompt.BuildInstructions(base, data)
}

func (s *sessionState) instructionsData() prompt.SectionData {
	ws := s.parent.workspace
	return prompt.SectionData{
		PlanMode:            s.planMode.Load(),
		Date:                time.Now().Format("January 2, 2006"),
		OS:                  runtime.GOOS,
		Arch:                runtime.GOARCH,
		WorkingDir:          ws.RootPath,
		MemoryDir:           ws.MemoryPath,
		MemoryContent:       ws.MemoryContent(),
		Skills:              skillpkg.FormatForPrompt(ws.Skills),
		ProjectInstructions: s.projectInstructions(),
	}
}

const projectInstructionsMaxBytes = 25 * 1024

type projectInstructionsEntry struct {
	path  string
	rel   string
	mtime time.Time
}

func findProjectInstructions(wd string) []projectInstructionsEntry {
	wd = filepath.Clean(wd)
	var groups [][]projectInstructionsEntry
	for dir := wd; ; {
		var group []projectInstructionsEntry
		for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
			p := filepath.Join(dir, name)
			info, err := os.Stat(p)
			if err != nil {
				continue
			}
			rel, _ := filepath.Rel(wd, p)
			if rel == "" {
				rel = name
			}
			group = append(group, projectInstructionsEntry{path: p, rel: rel, mtime: info.ModTime()})
		}
		if len(group) > 0 {
			groups = append(groups, group)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	// Root-level guidance first, most-specific (closest to wd) last, so the
	// deeper file reads as overriding the general one.
	var found []projectInstructionsEntry
	for i := len(groups) - 1; i >= 0; i-- {
		found = append(found, groups[i]...)
	}
	return found
}

func renderProjectInstructions(entries []projectInstructionsEntry) (string, map[string]time.Time) {
	parts := make([]string, 0, len(entries))
	mtimes := make(map[string]time.Time, len(entries))
	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		data, err := os.ReadFile(e.path)
		if err != nil {
			continue
		}
		mtimes[e.path] = e.mtime
		content := strings.TrimSpace(string(data))
		if content == "" || seen[content] {
			continue
		}
		seen[content] = true
		parts = append(parts, fmt.Sprintf("From %s:\n\n%s", e.rel, content))
	}

	// Over budget, drop the broadest guidance (front of the list) first — the
	// file closest to the working directory must survive.
	total := 0
	for _, p := range parts {
		total += len(p)
	}
	omitted := 0
	for len(parts) > 1 && total > projectInstructionsMaxBytes {
		total -= len(parts[0])
		parts = parts[1:]
		omitted++
	}

	result := strings.Join(parts, "\n\n---\n\n")
	if omitted > 0 {
		result = fmt.Sprintf("[%d broader instruction file(s) omitted — over the %dKB budget]\n\n%s", omitted, projectInstructionsMaxBytes/1024, result)
	}
	if len(result) > projectInstructionsMaxBytes {
		result = result[:projectInstructionsMaxBytes] + "\n\n[truncated]"
	}
	return result, mtimes
}

func (s *sessionState) projectInstructions() string {
	s.projectInstructionsMu.Lock()
	defer s.projectInstructionsMu.Unlock()

	found := findProjectInstructions(s.parent.workspace.RootPath)
	if len(found) == len(s.projectInstructionsMtimes) {
		unchanged := true
		for _, e := range found {
			if prev, ok := s.projectInstructionsMtimes[e.path]; !ok || !prev.Equal(e.mtime) {
				unchanged = false
				break
			}
		}
		if unchanged {
			return s.projectInstructionsCache
		}
	}
	result, mtimes := renderProjectInstructions(found)
	s.projectInstructionsCache = result
	s.projectInstructionsMtimes = mtimes
	return result
}
