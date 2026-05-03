package code

import (
	"context"
	"crypto/sha256"
	"embed"
	"fmt"
	iofs "io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/ask"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/fetch"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/fs"
	lsptool "github.com/adrianliechti/wingman-agent/pkg/agent/tool/lsp"
	toolmcp "github.com/adrianliechti/wingman-agent/pkg/agent/tool/mcp"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/search"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/shell"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/subagent"
	"github.com/adrianliechti/wingman-agent/pkg/code/bridge"
	"github.com/adrianliechti/wingman-agent/pkg/code/prompt"
	"github.com/adrianliechti/wingman-agent/pkg/lsp"
	"github.com/adrianliechti/wingman-agent/pkg/mcp"
	"github.com/adrianliechti/wingman-agent/pkg/rewind"
	"github.com/adrianliechti/wingman-agent/pkg/skill"

	"github.com/go-git/go-git/v5"
)

// isGitRepo reports whether dir is the root of a real git working tree.
// Used as the single project-mode gate for LSP detection and rewind init —
// keeping the predicate in one place ensures both features agree on what
// "this is a project" means.
func isGitRepo(dir string) bool {
	_, err := git.PlainOpen(dir)
	return err == nil
}

// isSupportedWorkspace decides whether wingman's heavier features (rewind,
// LSP, diffs panel) should run for this directory. A real git repo is
// always supported. Otherwise we walk the tree with a wall-clock budget:
// if it finishes in time the directory is small enough; if it doesn't,
// classify as unsupported and let the UI fall back to chat + file
// browsing. The walk runs in a goroutine so a slow readdir can't block
// startup; the leftover walk drains harmlessly in the background.
func isSupportedWorkspace(dir string) bool {
	if isGitRepo(dir) {
		return true
	}

	const budget = 4 * time.Second
	done := make(chan struct{})

	go func() {
		filepath.WalkDir(dir, func(_ string, _ iofs.DirEntry, _ error) error {
			return nil
		})
		close(done)
	}()

	select {
	case <-done:
		return true
	case <-time.After(budget):
		return false
	}
}

//go:embed skills/*/SKILL.md
var bundledFS embed.FS

// UI is the interface a frontend must provide to the coding agent.
type UI interface {
	Ask(ctx context.Context, message string) (string, error)
	Confirm(ctx context.Context, message string) (bool, error)
	StatusUpdate(status string)
}

type Agent struct {
	*agent.Agent

	Root        *os.Root
	RootPath    string
	MemoryPath  string
	ScratchPath string

	Skills []skill.Skill

	MCP *mcp.Manager
	// LSP is set by WarmUp when the workspace is a supported git repo;
	// nil otherwise. Callers nil-check before use.
	LSP *lsp.Manager
	// Rewind is set by WarmUp when the workspace is supported (git repo or
	// small enough to walk in time); nil otherwise. Rewind == nil is the
	// canonical "unsupported workspace" signal — UI shows the limited-mode
	// banner and hides the Changes/Problems tabs.
	Rewind *rewind.Manager
	Bridge *bridge.Bridge

	// warmupDone is closed when WarmUp completes (success or otherwise) so
	// callers can await its readiness without polling.
	warmupOnce sync.Once
	warmupDone chan struct{}

	PlanMode bool

	baseTools []tool.Tool
	mcpTools  []tool.Tool
	lspTools  []tool.Tool

	lastMemoryHash string
	mu             sync.Mutex
}

func New(workDir string, ui UI) (*Agent, error) {
	agentCfg, err := agent.DefaultConfig()

	if err != nil {
		return nil, err
	}

	root, err := os.OpenRoot(workDir)

	if err != nil {
		return nil, fmt.Errorf("failed to open workspace root: %w", err)
	}

	scratchDir := filepath.Join(os.TempDir(), fmt.Sprintf("wingman-%d", time.Now().Unix()))

	if err := os.MkdirAll(scratchDir, 0755); err != nil {
		root.Close()
		return nil, fmt.Errorf("failed to create scratch directory: %w", err)
	}

	memoryDir := projectMemoryDir(workDir)

	if err := os.MkdirAll(memoryDir, 0755); err != nil {
		os.RemoveAll(scratchDir)
		root.Close()
		return nil, fmt.Errorf("failed to create memory directory: %w", err)
	}

	elicit := &tool.Elicitation{
		Ask: func(ctx context.Context, msg string) (string, error) {
			if ui == nil {
				return "", nil
			}

			return ui.Ask(ctx, msg)
		},
		Confirm: func(ctx context.Context, msg string) (bool, error) {
			if ui == nil {
				return true, nil
			}

			return ui.Confirm(ctx, msg)
		},
	}

	// Skill precedence (later overrides earlier):
	//   bundled  → shipped with the binary, hidden from catalog until invoked
	//   personal → ~/.claude/skills, ~/.wingman/skills (user-wide)
	//   project  → .claude, .wingman, .skills, .github, .opencode (this repo)
	//
	// Bundled skills aren't materialized at startup — the user discovers
	// them via the slash-command picker (which lists all skills, on-disk or
	// not). On first invocation MaterializeBundled writes the file under
	// ~/.wingman/skills and updates Location, so the catalog picks them up
	// from the next prompt build onward.
	bundled := loadBundledSkills()
	personal := skill.MustDiscoverPersonal()
	discovered := skill.MustDiscover(workDir)
	mergedSkills := skill.Merge(skill.Merge(bundled, personal), discovered)

	// The read tool is sandboxed to workDir; let it also reach personal
	// skill directories that live outside the workspace. We add each
	// already-discovered personal skill dir (so the model can read its
	// SKILL.md plus any bundled scripts/references), AND ~/.wingman/skills
	// as a whole — bundled skills materialize into that tree on first
	// invocation and need to be readable in the same session.
	// Also allow the scratch dir: the truncation hook saves full tool
	// outputs there, and its hint tells the model to `read` that path.
	var allowedReadRoots []string
	for _, s := range mergedSkills {
		if s.Location != "" && filepath.IsAbs(s.Location) {
			allowedReadRoots = append(allowedReadRoots, s.Location)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		allowedReadRoots = append(allowedReadRoots, filepath.Join(home, ".wingman", "skills"))
	}
	allowedReadRoots = append(allowedReadRoots, scratchDir)

	baseTools := slices.Concat(
		fs.Tools(root, allowedReadRoots...),
		shell.Tools(workDir, elicit),
		fetch.Tools(),
		search.Tools(),
		ask.Tools(elicit),
		subagent.Tools(agentCfg),
	)

	mcpManager, _ := mcp.Load(filepath.Join(workDir, "mcp.json"))

	a := &Agent{
		Agent: &agent.Agent{Config: agentCfg},

		Root:        root,
		RootPath:    workDir,
		MemoryPath:  memoryDir,
		ScratchPath: scratchDir,

		Skills: mergedSkills,

		MCP: mcpManager,

		warmupDone: make(chan struct{}),

		baseTools: baseTools,
	}

	agentCfg.Tools = a.tools
	agentCfg.ContextMessages = a.memoryContextMessages

	return a, nil
}

// WarmUp runs the slow workspace probe and initializes Rewind/LSP if the
// directory is supported. Idempotent and safe to call from any goroutine —
// the first call does the work, the rest wait on warmupDone. Callers that
// want to gate on completion (e.g. server startup) can use WaitWarmUp.
//
// Three resulting modes:
//
//   - supported git repo  → Rewind set, LSP set, lspTools set
//   - supported scratch   → Rewind set, LSP nil, lspTools nil
//   - unsupported (huge)  → Rewind nil, LSP nil; UI falls back to chat-only
func (a *Agent) WarmUp() {
	a.warmupOnce.Do(func() {
		defer close(a.warmupDone)

		if !isSupportedWorkspace(a.RootPath) {
			return
		}

		// Sweep stale shadow repos from prior crashed sessions before
		// starting a new one. Cheap and safe (mtime-gated).
		rewind.CleanupOrphans()
		rewindManager := rewind.New(a.RootPath)

		var lspManager *lsp.Manager
		var lspTools []tool.Tool
		if isGitRepo(a.RootPath) {
			lspManager = lsp.NewManager(a.RootPath)
			lspTools = lsptool.NewTools(lspManager)
		}

		a.mu.Lock()
		a.Rewind = rewindManager
		a.LSP = lspManager
		a.lspTools = lspTools
		a.mu.Unlock()
	})
}

// WaitWarmUp blocks until WarmUp has completed.
func (a *Agent) WaitWarmUp() {
	<-a.warmupDone
}

// InitMCP connects MCP servers, fetches their tools, and sets up the
// IDE bridge. Call this after the UI is ready (typically async).
func (a *Agent) InitMCP(ctx context.Context) error {
	if a.MCP == nil {
		return nil
	}

	if err := a.MCP.Connect(ctx); err != nil {
		return err
	}

	mcpTools, err := toolmcp.Tools(ctx, a.MCP)
	if err != nil {
		return err
	}

	a.mu.Lock()
	a.mcpTools = mcpTools
	a.mu.Unlock()

	a.Bridge = bridge.Setup(ctx, a.RootPath, a.MCP)

	return nil
}

func (a *Agent) tools() []tool.Tool {
	a.mu.Lock()
	defer a.mu.Unlock()

	tools := append([]tool.Tool{}, a.baseTools...)
	tools = append(tools, a.mcpTools...)

	if a.Bridge == nil || !a.Bridge.IsConnected() {
		tools = append(tools, a.lspTools...)
	}

	if a.PlanMode {
		tools = planModeTools(tools)
	}

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

// IsGitRepo reports whether the agent's working directory is currently a git
// repo. Re-evaluated on each call so callers can react to `git init` (or
// `rm -rf .git`) happening mid-session.
func (a *Agent) IsGitRepo() bool {
	return isGitRepo(a.RootPath)
}

// RestartRewind tears down the existing rewind manager and creates a fresh
// one, re-baselining at the current state. Used on /sessions/new so the
// checkpoint history is scoped to one conversation. No-op on unsupported
// workspaces (Rewind == nil). LSP is intentionally untouched —
// gopls/etc. are slow to spin up and shouldn't churn on session boundaries.
func (a *Agent) RestartRewind() {
	if a.Rewind == nil {
		return
	}
	a.Rewind.Cleanup()
	a.Rewind = rewind.New(a.RootPath)
}

// SyncProjectMode rebuilds LSP when the working dir's git status flips
// (typically: agent ran `git init` in a scratch dir). No-op on unsupported
// workspaces — `git init` alone doesn't make a 1M-file home folder small
// enough to support full features.
func (a *Agent) SyncProjectMode() {
	if a.Rewind == nil {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	oldLSP := a.LSP
	if isGitRepo(a.RootPath) {
		a.LSP = lsp.NewManager(a.RootPath)
		a.lspTools = lsptool.NewTools(a.LSP)
	} else {
		a.LSP = nil
		a.lspTools = nil
	}

	if oldLSP != nil {
		oldLSP.Close()
	}
}

func (a *Agent) Close() {
	if a.Bridge != nil {
		a.Bridge.Close()
	}

	if a.MCP != nil {
		a.MCP.Close()
	}

	if a.LSP != nil {
		a.LSP.Close()
	}

	if a.Rewind != nil {
		a.Rewind.Cleanup()
	}

	if a.ScratchPath != "" {
		os.RemoveAll(a.ScratchPath)
	}

	if a.Root != nil {
		a.Root.Close()
	}
}

// Path accessors

// Memory and plan content

const (
	memoryFileName      = "MEMORY.md"
	memoryMaxBytes      = 25 * 1024
	memoryContextPrefix = "Current MEMORY.md:\n\n"
	memoryContextEmpty  = "MEMORY.md is currently empty."
)

func (a *Agent) MemoryContent() string {
	data, err := os.ReadFile(filepath.Join(a.MemoryPath, memoryFileName))
	if err != nil {
		return ""
	}

	content := strings.TrimSpace(string(data))
	if content == "" {
		return ""
	}

	if len(content) > memoryMaxBytes {
		truncated := content[:memoryMaxBytes]
		if idx := strings.LastIndex(truncated, "\n"); idx > 0 {
			truncated = truncated[:idx]
		}

		content = truncated + "\n\n> WARNING: MEMORY.md exceeded 25KB and was truncated."
	}

	return content
}

func (a *Agent) memoryContextMessages() []agent.Message {
	content := a.MemoryContent()
	messageText := ""
	if content != "" {
		messageText = memoryContextPrefix + content
	}

	sum := sha256.Sum256([]byte(content))
	hash := string(sum[:])

	a.mu.Lock()
	defer a.mu.Unlock()

	prevHash := a.lastMemoryHash
	if hash == a.lastMemoryHash {
		return nil
	}
	a.lastMemoryHash = hash

	if messageText == "" {
		messageText = memoryContextEmpty
		if prevHash == "" && a.latestMemoryContextText() == "" {
			return nil
		}
	}

	if prevHash == "" && a.latestMemoryContextText() == messageText {
		return nil
	}

	return []agent.Message{{
		Role:   agent.RoleUser,
		Hidden: true,
		Content: []agent.Content{{
			Text: messageText,
		}},
	}}
}

func (a *Agent) latestMemoryContextText() string {
	if a.Agent == nil {
		return ""
	}

	for i := len(a.Messages) - 1; i >= 0; i-- {
		m := a.Messages[i]
		if !m.Hidden || m.Role != agent.RoleUser || len(m.Content) != 1 {
			continue
		}

		text := m.Content[0].Text
		if strings.HasPrefix(text, memoryContextPrefix) || text == memoryContextEmpty {
			return text
		}
	}

	return ""
}

// Instructions

func BuildInstructions(data prompt.SectionData) string {
	base := prompt.Instructions

	if data.PlanMode {
		base = prompt.Planning
	}

	return prompt.BuildInstructions(base, data)
}

func (a *Agent) InstructionsData() prompt.SectionData {
	data := prompt.SectionData{
		PlanMode:            a.PlanMode,
		Date:                time.Now().Format("January 2, 2006"),
		OS:                  runtime.GOOS,
		Arch:                runtime.GOARCH,
		WorkingDir:          a.RootPath,
		MemoryDir:           a.MemoryPath,
		Skills:              skill.FormatForPrompt(a.Skills),
		ProjectInstructions: ReadProjectInstructions(a.RootPath),
	}

	if a.Bridge != nil && a.Bridge.IsConnected() {
		data.BridgeInstructions = a.Bridge.GetInstructions()
	}

	return data
}

// Helpers

func projectMemoryDir(workingDir string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}

	sanitized := filepath.Clean(workingDir)

	if vol := filepath.VolumeName(sanitized); vol != "" {
		sanitized = strings.TrimPrefix(sanitized, vol)
	}

	sanitized = strings.TrimPrefix(sanitized, string(filepath.Separator))
	sanitized = strings.ReplaceAll(sanitized, string(filepath.Separator), "_")
	sanitized = strings.ToLower(sanitized)

	return filepath.Join(home, ".wingman", "projects", sanitized, "memory")
}

func loadBundledSkills() []skill.Skill {
	skills, _ := skill.LoadBundled(bundledFS, "skills")
	return skills
}

const projectInstructionsMaxBytes = 25 * 1024

// ReadProjectInstructions walks from wd up to the filesystem root,
// collecting AGENTS.md and CLAUDE.md files. Returns them concatenated
// with headers, closest ancestor first. Truncates at 25KB.
func ReadProjectInstructions(wd string) string {
	var parts []string

	dir := filepath.Clean(wd)

	for {
		for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				continue
			}

			content := strings.TrimSpace(string(data))
			if content == "" {
				continue
			}

			rel, _ := filepath.Rel(wd, filepath.Join(dir, name))
			if rel == "" {
				rel = name
			}

			parts = append(parts, fmt.Sprintf("From %s:\n\n%s", rel, content))
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}

		dir = parent
	}

	result := strings.Join(parts, "\n\n---\n\n")

	if len(result) > projectInstructionsMaxBytes {
		result = result[:projectInstructionsMaxBytes] + "\n\n[truncated]"
	}

	return result
}
