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
	"github.com/adrianliechti/wingman-agent/pkg/agent/hook"
	"github.com/adrianliechti/wingman-agent/pkg/agent/hook/external"
	"github.com/adrianliechti/wingman-agent/pkg/agent/hook/truncation"
	"github.com/adrianliechti/wingman-agent/pkg/agent/task"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	elicittool "github.com/adrianliechti/wingman-agent/pkg/agent/tool/elicit"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/fetch"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/fs"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/schedule"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/shell"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/subagent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/todo"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/websearch"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/adrianliechti/wingman-agent/pkg/code/prompt"
	"github.com/adrianliechti/wingman-agent/pkg/layout"
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
	modelByRole    map[modelRole]string
	effortByRole   map[modelRole]string
	upstreamModels map[string]bool

	mu       sync.Mutex
	sessions map[string]*sessionState
	closed   bool
}

var _ code.Agent = (*Agent)(nil)

type sessionState struct {
	parent *Agent
	aa     *harness.Agent

	modelByRole  map[modelRole]string
	effortByRole map[modelRole]string

	// mode is switchable while a turn is running, so it is read from the turn
	// goroutine and written from the UI one.
	mode        atomic.Value // sessionMode
	toolSet     *tool.Set
	turnTools   atomic.Value // []tool.Tool, pinned for the running turn
	execManager *shell.ExecManager
	tasks       *task.Registry

	schedules   *schedule.MemoryStore
	scheduleSeq atomic.Uint64

	freshness *fs.Freshness
	watchStop chan struct{}

	changedMu    sync.Mutex
	changedPaths []string

	projectInstructionsMu     sync.Mutex
	projectInstructionsCache  string
	projectInstructionsMtimes map[string]time.Time

	cancelMu    sync.Mutex
	cancelFn    context.CancelFunc
	cancelGen   uint64
	closed      bool
	startSource string
}

type sessionMode string

type modelRole string

const (
	modeAgent      sessionMode = code.AgentModeID
	modePlan       sessionMode = code.PlanModeID
	modeUnattended sessionMode = code.UnattendedModeID

	modelRoleMain    modelRole = "main"
	modelRolePlan    modelRole = "plan"
	modelRoleUtility modelRole = "utility"
)

var modelClassByRole = map[modelRole]model.Class{
	modelRoleMain:    model.ClassMedium,
	modelRolePlan:    model.ClassLarge,
	modelRoleUtility: model.ClassSmall,
}

func (s *sessionState) currentMode() sessionMode {
	if mode, ok := s.mode.Load().(sessionMode); ok && mode != "" {
		return mode
	}
	return modeAgent
}

func (s *sessionState) setMode(mode sessionMode) {
	s.mode.Store(mode)
}

func New(ws *code.Workspace, cfg *harness.Config, ui code.UI) *Agent {
	a := &Agent{
		workspace: ws,
		cfg:       cfg,
		ui:        ui,
		modelByRole: map[modelRole]string{
			modelRoleMain:    harness.DefaultModel(),
			modelRolePlan:    harness.DefaultPlanModel(),
			modelRoleUtility: harness.DefaultUtilityModel(),
		},
		effortByRole: map[modelRole]string{
			modelRoleMain:    harness.DefaultEffort(),
			modelRolePlan:    harness.DefaultPlanEffort(),
			modelRoleUtility: "",
		},
		sessionsDir: filepath.Join(filepath.Dir(ws.MemoryPath), "sessions"),
		sessions:    map[string]*sessionState{},
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
	s := a.session(sessionID)
	a.modelMu.Lock()
	defer a.modelMu.Unlock()
	available := model.Available(a.upstreamModels)
	current, _ := a.roleModelLocked(s, "")
	return available, current
}

func activeModelRole(s *sessionState) modelRole {
	if s != nil && s.currentMode() == modePlan {
		return modelRolePlan
	}
	return modelRoleMain
}

func resolvedModelRole(s *sessionState, name string) (modelRole, bool) {
	role := modelRole(name)
	if role == "" {
		role = activeModelRole(s)
	}
	_, ok := modelClassByRole[role]
	return role, ok
}

// roleModelLocked applies the same resolution order to every model role:
// session override, agent setting, then class-based selection. Utility waits
// for model discovery before making a class-based pick and deliberately keeps
// an explicit model even when it is absent from the discovered catalog.
func (a *Agent) roleModelLocked(s *sessionState, name string) (string, bool) {
	role, ok := resolvedModelRole(s, name)
	if !ok {
		return "", false
	}

	current := ""
	if s != nil {
		current = s.modelByRole[role]
	}
	if current == "" {
		current = a.modelByRole[role]
	}
	if current == "" {
		if role == modelRoleUtility && a.upstreamModels == nil {
			return "", false
		}
		current = a.classModelLocked(modelClassByRole[role])
	}

	if role != modelRoleUtility {
		available := model.Available(a.upstreamModels)
		if len(available) > 0 && !slices.ContainsFunc(available, func(m model.Model) bool { return m.ID == current }) {
			current = available[0].ID
		}
	}
	if current == "" {
		return "", false
	}
	return current, true
}

func (a *Agent) roleModel(s *sessionState, role string) (harness.ModelOption, bool) {
	a.modelMu.Lock()
	defer a.modelMu.Unlock()
	id, ok := a.roleModelLocked(s, role)
	if !ok {
		return harness.ModelOption{}, false
	}
	return harness.ModelOption{
		ID:      id,
		Efforts: slices.Clone(model.ProfileFor(id).Efforts),
	}, true
}

// RoleModel resolves "main", "plan", or "utility" without a session.
// An empty role selects main; session-derived configs replace this with a
// resolver whose empty role follows the session's active mode.
func (a *Agent) RoleModel(role string) (harness.ModelOption, bool) {
	return a.roleModel(nil, role)
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

// SetModel applies to the session's current role: picking a model while in
// plan mode configures planning, otherwise coding.
func (a *Agent) SetModel(_ context.Context, sessionID, id string) error {
	s := a.session(sessionID)
	a.modelMu.Lock()
	role := activeModelRole(s)
	if a.modelByRole == nil {
		a.modelByRole = map[modelRole]string{}
	}
	if a.effortByRole == nil {
		a.effortByRole = map[modelRole]string{}
	}
	a.modelByRole[role] = id
	a.effortByRole[role] = ""
	// Switching models resets the reasoning effort to the new model's default:
	// a level the previous model allowed (e.g. "max") may exceed what this one
	// supports, so drop back to the default instead of carrying it over.
	if s != nil {
		if s.modelByRole == nil {
			s.modelByRole = map[modelRole]string{}
		}
		if s.effortByRole == nil {
			s.effortByRole = map[modelRole]string{}
		}
		s.modelByRole[role] = id
		s.effortByRole[role] = ""
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

func effortValuesFor(id string) []string {
	if supported := model.ProfileFor(id).Efforts; len(supported) > 0 {
		return append([]string{"auto"}, supported...)
	}
	return effortValues
}

func (a *Agent) Effort(sessionID string) (string, []string) {
	s := a.session(sessionID)
	a.modelMu.Lock()
	role := activeModelRole(s)
	currentModel, _ := a.roleModelLocked(s, string(role))
	current := ""
	if s != nil {
		current = s.effortByRole[role]
	}
	if current == "" {
		current = a.effortByRole[role]
	}
	a.modelMu.Unlock()
	if current == "" {
		current = "auto"
	}
	return current, slices.Clone(effortValuesFor(currentModel))
}

func (a *Agent) effortFor(s *sessionState) string {
	a.modelMu.Lock()
	defer a.modelMu.Unlock()
	role := activeModelRole(s)
	if s != nil && s.effortByRole[role] != "" {
		return s.effortByRole[role]
	}
	if a.effortByRole[role] != "" {
		return a.effortByRole[role]
	}
	current, _ := a.roleModelLocked(s, string(role))
	if effort := model.ProfileFor(current).DefaultEffort; effort != "" {
		return effort
	}
	// xhigh is the planning default only where a large model backs it.
	if role == modelRolePlan && model.ClassOf(current) == model.ClassLarge {
		return "xhigh"
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
	role := activeModelRole(s)
	currentModel, _ := a.roleModelLocked(s, string(role))
	if supported := model.ProfileFor(currentModel).Efforts; value != "" && len(supported) > 0 && !slices.Contains(supported, value) {
		a.modelMu.Unlock()
		return fmt.Errorf("effort %q is not supported by %s (supported: %s)", value, currentModel, strings.Join(supported, ", "))
	}
	if a.effortByRole == nil {
		a.effortByRole = map[modelRole]string{}
	}
	a.effortByRole[role] = value
	if s != nil {
		if s.effortByRole == nil {
			s.effortByRole = map[modelRole]string{}
		}
		s.effortByRole[role] = value
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
	s.startSource = "startup"
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
	s.startSource = "resume"
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

func (a *Agent) HistorySnapshot(id string) code.HistorySnapshot {
	s := a.session(id)
	if s == nil {
		return code.HistorySnapshot{}
	}
	state := s.aa.StateSnapshot()
	return code.HistorySnapshot{Messages: state.Messages, Revision: state.Revision}
}

func (a *Agent) HistoryVersion(id string) code.HistoryVersion {
	s := a.session(id)
	if s == nil {
		return code.HistoryVersion{}
	}
	messageCount, revision := s.aa.StateVersion()
	return code.HistoryVersion{Revision: revision, MessageCount: messageCount}
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

// Schedules exposes the session's scheduled tasks so UI surfaces can list
// them alongside its background agents.
func (a *Agent) Schedules(id string) *schedule.MemoryStore {
	s := a.session(id)
	if s == nil {
		return nil
	}
	return s.schedules
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
	{ID: code.AgentModeID, Name: "Agent", Description: "Works interactively and asks before risky actions or consequential decisions."},
	{ID: code.PlanModeID, Name: "Plan", Description: "Read-only — proposes a plan, doesn't edit code."},
	code.UnattendedMode(),
}

func (a *Agent) Modes(sessionID string) ([]code.Mode, string) {
	out := make([]code.Mode, len(wingmanModes))
	copy(out, wingmanModes)
	current := code.AgentModeID
	if s := a.session(sessionID); s != nil {
		current = string(s.currentMode())
	}
	return out, current
}

func (a *Agent) SetMode(_ context.Context, sessionID, modeID string) error {
	var mode sessionMode
	switch modeID {
	case code.AgentModeID:
		mode = modeAgent
	case code.PlanModeID:
		mode = modePlan
	case code.UnattendedModeID:
		mode = modeUnattended
	default:
		return fmt.Errorf("unknown mode %q", modeID)
	}
	if s := a.session(sessionID); s != nil {
		s.setMode(mode)
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
		parent:       a,
		aa:           &harness.Agent{Config: sessionCfg},
		modelByRole:  map[modelRole]string{},
		effortByRole: map[modelRole]string{modelRoleUtility: ""},
	}
	s.setMode(modeAgent)
	sessionCfg.Tools = s.tools
	sessionCfg.Instructions = s.instructions
	sessionCfg.Model = func() string {
		option, _ := a.roleModel(s, "")
		return option.ID
	}
	sessionCfg.Effort = func() string {
		return a.effortFor(s)
	}
	sessionCfg.RoleModel = func(role string) (harness.ModelOption, bool) {
		return a.roleModel(s, role)
	}
	elicit := a.prompts
	ws := a.workspace

	globalHooks, globalErr := external.Load(userHooksConfigPath())
	workspaceHooksPath := filepath.Join(ws.RootPath, ".codex", "hooks.json")
	workspaceHooks, workspaceErr := external.Load(workspaceHooksPath)

	// Fail closed: a hook config the user wrote but that no longer parses must
	// not silently disable the guards it configures.
	if err := errors.Join(globalErr, workspaceErr); err != nil {
		message := fmt.Sprintf("hook configuration is invalid; fix or remove it to unblock tools: %v", err)
		sessionCfg.Hooks.PreToolUse = append(sessionCfg.Hooks.PreToolUse,
			func(context.Context, tool.ToolCall) (hook.PreToolUseOutcome, error) {
				return hook.PreToolUseOutcome{Outcome: hook.Outcome{Block: true, Reason: message}}, nil
			},
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

	sessionCfg.Hooks.Append(globalHooks.Build(ws.RootPath, nil))
	sessionCfg.Hooks.Append(workspaceHooks.Build(ws.RootPath, workspaceGate))
	for i := range ws.Plugins {
		p := &ws.Plugins[i]
		if p.Hooks == nil || p.Hooks.RuleCount() == 0 {
			continue
		}
		pluginGate := &external.Gate{
			Confirm: elicit.Confirm,
			Message: fmt.Sprintf("Run %d hook rule(s) from plugin %q at %s?", p.Hooks.RuleCount(), p.Name, p.Root),
		}
		sessionCfg.Hooks.Append(p.Hooks.BuildWithOptions(ws.RootPath, external.BuildOptions{
			Gate: pluginGate,
			Environment: map[string]string{
				"PLUGIN_ROOT": p.Root,
				"PLUGIN_DATA": p.Data,
			},
		}))
	}
	sessionCfg.Hooks.PostToolUse = append(sessionCfg.Hooks.PostToolUse,
		truncation.New(ws.ScratchPath),
	)

	var allowedReadRoots []string
	for _, sk := range ws.Skills {
		if sk.Location != "" && filepath.IsAbs(sk.Location) {
			allowedReadRoots = append(allowedReadRoots, sk.Location)
		}
	}
	if path, err := layout.WingmanPath("skills"); err == nil {
		allowedReadRoots = append(allowedReadRoots, path)
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
	s.schedules = schedule.NewMemoryStore()
	s.watchStop = make(chan struct{})
	approvals := shell.NewApprovals()

	sessionCfg.Hooks.UserPromptSubmit = append(sessionCfg.Hooks.UserPromptSubmit,
		func(context.Context, string) (hook.Outcome, error) {
			s.sweepFileChanges()
			notice := formatFileChangeNotice(s.takeFileChanges())
			if notice == "" {
				return hook.Outcome{}, nil
			}
			return hook.Outcome{AdditionalContext: []string{notice}}, nil
		},
	)

	go s.watchFileChanges()
	go s.runSchedules()

	shellOpts := &shell.Options{ScratchDir: ws.ScratchPath}

	baseTools := slices.Concat(
		ws.WithEditDiagnostics(fs.Tools(ws.Root, &fs.Options{
			AllowedReadRoots:  allowedReadRoots,
			AllowedWriteRoots: allowedWriteRoots,
			Freshness:         s.freshness,
		})),
		shell.Tools(ws.RootPath, elicit, approvals, shellOpts),
		shell.ExecTools(s.execManager, ws.RootPath, elicit, approvals, shellOpts),
		todo.Tools(),
		schedule.Tools(s.schedules),
		elicittool.Tools(elicit),
		fetch.Tools(elicit, sessionCfg.Utility),
		websearch.Tools(elicit),
		subagent.Tools(sessionCfg, s.subagentContext, s.tasks, subagent.Discover(ws.RootPath)...),
	)
	s.toolSet = tool.NewSet(baseTools...)
	// Close runs in reverse: tasks first so background subagents are canceled
	// before their shared exec sessions are killed.
	s.toolSet.Own("exec manager", func() error {
		s.execManager.Close()
		return nil
	})
	s.toolSet.Own("task registry", func() error {
		s.tasks.Close()
		return nil
	})
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
	return filepath.Join(home, ".codex", "hooks.json")
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
	ctx = a.promptContext(ctx)
	if s := a.session(code.SessionIDFromContext(ctx)); s != nil && s.currentMode() == modeUnattended {
		return code.UnattendedElicitation(req), nil
	}
	ui := a.currentUI()
	if ui == nil {
		return tool.ElicitResult{Action: tool.ElicitCancel}, nil
	}
	return ui.Elicit(ctx, req)
}

func (a *Agent) confirm(ctx context.Context, message string) (bool, error) {
	ctx = a.promptContext(ctx)
	if s := a.session(code.SessionIDFromContext(ctx)); s != nil {
		if call, ok := hook.ToolCallFromContext(ctx); ok {
			allowed := false
			for _, run := range s.aa.Hooks.PermissionRequest {
				outcome, err := run(ctx, call)
				if err != nil {
					continue
				}
				switch outcome.Behavior {
				case hook.PermissionDeny:
					return false, nil
				case hook.PermissionAllow:
					allowed = true
				}
			}
			if allowed {
				return true, nil
			}
		}
		if s.currentMode() == modeUnattended {
			return true, nil
		}
	}
	ui := a.currentUI()
	if ui == nil {
		return false, nil
	}
	return ui.Confirm(ctx, message)
}

func (s *sessionState) beginSend(ctx context.Context, input []harness.Content, cancel context.CancelFunc) (iter.Seq2[harness.Message, error], uint64, error) {
	catalog := collectManagedTools(s.parent.workspace)

	s.cancelMu.Lock()
	if s.closed {
		s.cancelMu.Unlock()
		return nil, 0, errors.New("session is closed")
	}
	s.turnTools.Store(catalog)

	permissionMode := "default"
	switch s.currentMode() {
	case modePlan:
		permissionMode = "plan"
	case modeUnattended:
		permissionMode = "bypassPermissions"
	}
	ctx = hook.WithRuntime(ctx, hook.Runtime{
		SessionID:      s.aa.CacheKey,
		CWD:            s.parent.workspace.RootPath,
		Model:          s.aa.Model(),
		PermissionMode: permissionMode,
		StartSource:    s.startSource,
	})
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
	last := s.cancelGen == gen
	if last {
		s.cancelFn = nil
	}
	s.cancelMu.Unlock()
	if last {
		s.turnTools.Store([]tool.Tool(nil))
	}
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
	_ = s.toolSet.Close()

	if hooks := s.aa.Hooks.SessionEnd; len(hooks) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ctx = hook.WithRuntime(ctx, hook.Runtime{
			SessionID:      s.aa.CacheKey,
			CWD:            s.parent.workspace.RootPath,
			Model:          s.aa.Model(),
			PermissionMode: "default",
		})
		for _, h := range hooks {
			h(ctx, "other")
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
	tools := s.toolSet.Slice()
	tools = append(tools, s.managedTools()...)
	switch s.currentMode() {
	case modePlan:
		tools = planModeTools(tools)
	case modeUnattended:
		tools = slices.DeleteFunc(tools, func(t tool.Tool) bool { return t.Name == "elicit" })
	}
	slices.SortStableFunc(tools, func(a, b tool.Tool) int { return cmp.Compare(a.Name, b.Name) })
	return tools
}

// managedTools stays pinned for the length of a turn: the harness asks for the
// tool set once per round so a mode switch lands mid-turn, and an MCP
// tools/list_changed arriving between rounds must not reshape the catalog under
// the running turn.
func (s *sessionState) managedTools() []tool.Tool {
	if pinned, ok := s.turnTools.Load().([]tool.Tool); ok && pinned != nil {
		return pinned
	}
	return collectManagedTools(s.parent.workspace)
}

func collectManagedTools(ws *code.Workspace) []tool.Tool {
	mcpTools, lspTools, graphTools := ws.ManagedTools()
	return slices.Concat(mcpTools, lspTools, graphTools)
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

func planModeEffectExecute(t tool.Tool) func(context.Context, map[string]any) (tool.Result, error) {
	return func(ctx context.Context, args map[string]any) (tool.Result, error) {
		if t.Effect == nil || t.Effect(args) != tool.EffectReadOnly {
			return tool.Result{}, fmt.Errorf("plan mode only allows read-only tool calls")
		}
		return t.Execute(ctx, args)
	}
}

func (s *sessionState) instructions() string {
	option, _ := s.parent.roleModel(s, "")
	return BuildInstructions(option.ID, s.instructionsData())
}

func (s *sessionState) subagentContext() string {
	return prompt.BuildAgentContext(s.instructionsData())
}

func BuildInstructions(modelID string, data prompt.SectionData) string {
	variant := prompt.VariantFor(modelID)
	selected, ok := model.Find(modelID)
	if !ok {
		selected.ID = modelID
		selected.Name = modelID
	}
	data.Model = selected

	base := variant.Agent
	if data.PlanMode {
		base = variant.Plan
	} else if data.UnattendedMode {
		base += "\n\n" + variant.Unattended
	}
	return prompt.BuildInstructions(base, data)
}

func (s *sessionState) instructionsData() prompt.SectionData {
	ws := s.parent.workspace
	now := time.Now()
	return prompt.SectionData{
		PlanMode:            s.currentMode() == modePlan,
		UnattendedMode:      s.currentMode() == modeUnattended,
		Date:                now.Format("2006-01-02"),
		Timezone:            localTimezone(now),
		OS:                  runtime.GOOS,
		Arch:                runtime.GOARCH,
		WorkingDir:          ws.RootPath,
		Shell:               localShell(),
		MemoryDir:           ws.MemoryPath,
		MemoryContent:       ws.MemoryContent(),
		Skills:              skillpkg.FormatForPrompt(ws.Skills),
		ProjectInstructions: s.projectInstructions(),
	}
}

func localShell() string {
	for _, name := range []string{"SHELL", "COMSPEC"} {
		if shell := strings.TrimSpace(os.Getenv(name)); shell != "" {
			return filepath.Base(shell)
		}
	}
	return ""
}

func localTimezone(now time.Time) string {
	if timezone := strings.TrimPrefix(strings.TrimSpace(os.Getenv("TZ")), ":"); timezone != "" && !filepath.IsAbs(timezone) {
		return timezone
	}
	if timezone := now.Location().String(); timezone != "" && timezone != "Local" {
		return timezone
	}
	if target, err := filepath.EvalSymlinks("/etc/localtime"); err == nil {
		const marker = "/zoneinfo/"
		normalized := filepath.ToSlash(target)
		if index := strings.LastIndex(normalized, marker); index >= 0 {
			return normalized[index+len(marker):]
		}
	}

	name, offset := now.Zone()
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	return fmt.Sprintf("%s (UTC%s%02d:%02d)", name, sign, offset/3600, offset%3600/60)
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
	for _, group := range slices.Backward(groups) {
		found = append(found, group...)
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
