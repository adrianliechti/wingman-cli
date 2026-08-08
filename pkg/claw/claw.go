package claw

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/hook"
	"github.com/adrianliechti/wingman-agent/pkg/agent/hook/truncation"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/fs"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/mcp"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/shell"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/subagent"
	"github.com/adrianliechti/wingman-agent/pkg/claw/channel"
	"github.com/adrianliechti/wingman-agent/pkg/claw/memory"
	"github.com/adrianliechti/wingman-agent/pkg/claw/prompt"
	"github.com/adrianliechti/wingman-agent/pkg/claw/tool/manage"
	"github.com/adrianliechti/wingman-agent/pkg/claw/tool/schedule"
)

type managedAgent struct {
	agent *agent.Agent

	// mu serializes runs and session saves for this agent
	mu sync.Mutex

	// notifyRoute is where scheduled reports go: the first conversation
	// that ever messaged this agent
	notifyRoute channel.Route

	// snapshot of the agent state after the last completed run,
	// readable without blocking on an active run
	snapMu       sync.Mutex
	snapMessages []agent.Message
	snapUsage    agent.Usage

	cancel  context.CancelFunc
	root    *os.Root
	scratch string

	// lastPrune is only touched by this agent's scheduler goroutine
	lastPrune time.Time
}

func (ma *managedAgent) updateSnapshot() {
	state := ma.agent.StateSnapshot()
	ma.snapMu.Lock()
	ma.snapMessages = state.Messages
	ma.snapUsage = state.Usage
	ma.snapMu.Unlock()
}

type Claw struct {
	config *Config

	mu     sync.RWMutex
	agents map[string]*managedAgent
	runCtx context.Context

	mcpTools []tool.Tool
}

func New(config *Config) *Claw {
	return &Claw{config: config, agents: map[string]*managedAgent{}}
}

func (c *Claw) Init() error {
	if c.config.MCP != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := c.config.MCP.Connect(ctx); err != nil {
			log.Printf("warning: connect MCP servers: %v", err)
		}

		if tools, err := mcp.Tools(ctx, c.config.MCP); err == nil {
			c.mcpTools = tools
		} else {
			log.Printf("warning: load MCP tools: %v", err)
		}
	}

	names, err := c.config.Memory.ListAgents()
	if err != nil {
		return fmt.Errorf("failed to list agents: %w", err)
	}

	c.mu.Lock()
	for _, name := range names {
		if _, err := c.loadAgent(name); err != nil {
			log.Printf("warning: failed to load agent %q: %v", name, err)
		}
	}
	c.mu.Unlock()

	c.ensureDefaultTasks("main")

	return nil
}

func (c *Claw) Run(ctx context.Context) error {
	if len(c.config.Channels) == 0 {
		return fmt.Errorf("no channels configured")
	}

	c.runCtx = ctx

	c.mu.Lock()
	for name, ma := range c.agents {
		c.startScheduler(name, ma)
	}
	c.mu.Unlock()

	for _, ch := range c.config.Channels[:len(c.config.Channels)-1] {
		go func(ch channel.Channel) {
			if err := ch.Start(ctx, c.handleMessage); err != nil {
				log.Printf("channel %s: %v", ch.Name(), err)
			}
		}(ch)
	}

	primary := c.config.Channels[len(c.config.Channels)-1]
	return primary.Start(ctx, c.handleMessage)
}

func (c *Claw) Close() {
	c.mu.Lock()
	agents := c.agents
	c.agents = map[string]*managedAgent{}
	c.mu.Unlock()

	for _, ma := range agents {
		c.unloadAgent(ma)
	}
}

func (c *Claw) unloadAgent(ma *managedAgent) {
	if ma.cancel != nil {
		ma.cancel()
	}

	ma.mu.Lock()
	defer ma.mu.Unlock()

	if ma.scratch != "" {
		os.RemoveAll(ma.scratch)
	}

	if ma.root != nil {
		ma.root.Close()
	}
}

func (c *Claw) CreateAgent(name string) error {
	if err := memory.ValidName(name); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.config.Memory.AgentExists(name) {
		return fmt.Errorf("agent %q already exists", name)
	}

	if err := c.config.Memory.EnsureAgent(name); err != nil {
		return err
	}

	ma, err := c.loadAgent(name)
	if err != nil {
		return err
	}

	if c.runCtx != nil {
		c.startScheduler(name, ma)
	}

	return nil
}

func (c *Claw) DeleteAgent(name string) error {
	if err := memory.ValidName(name); err != nil {
		return err
	}
	if name == "main" {
		return fmt.Errorf("cannot delete the main agent")
	}

	c.mu.Lock()
	ma := c.agents[name]
	delete(c.agents, name)
	c.mu.Unlock()

	if ma != nil {
		c.unloadAgent(ma)
	}

	return c.config.Memory.RemoveAgent(name)
}

func (c *Claw) ListAgents() ([]string, error) {
	return c.config.Memory.ListAgents()
}

func (c *Claw) agentWorkDir(name string) string {
	if name == "main" {
		return c.config.Memory.Dir()
	}
	return c.config.Memory.WorkspaceDir(name)
}

func (c *Claw) loadAgent(name string) (*managedAgent, error) {
	workDir := c.agentWorkDir(name)

	if err := os.MkdirAll(workDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create workspace for agent %q: %w", name, err)
	}

	root, err := os.OpenRoot(workDir)
	if err != nil {
		return nil, fmt.Errorf("failed to open workspace for agent %q: %w", name, err)
	}

	cfg := c.config.AgentConfig.Derive()
	cfg.Instructions = func() string { return c.buildInstructions(name) }
	cfg.Hooks.PreToolUse = append(cfg.Hooks.PreToolUse, auditHook(name))

	scratchDir, err := os.MkdirTemp("", "claw-scratch-")
	if err != nil {
		return nil, fmt.Errorf("failed to create scratch directory for agent %q: %w", name, err)
	}
	cfg.Hooks.PostToolUse = append(cfg.Hooks.PostToolUse,
		truncation.New(scratchDir),
	)

	agentTools := slices.Concat(
		fs.Tools(root, &fs.Options{AllowedReadRoots: []string{scratchDir}}),
		shell.Tools(workDir, nil, nil, nil),
		c.config.Tools,
		c.mcpTools,
		schedule.Tools(c.config.Memory.AgentDir(name)),
	)

	if name == "main" {
		agentTools = append(agentTools, manage.Tools(c, c.config.Memory)...)
	}

	agentTools = append(agentTools, subagent.Tools(cfg, func() string { return c.buildAgentContext(name) }, nil)...)

	cfg.Tools = func() []tool.Tool { return agentTools }

	a := &agent.Agent{Config: cfg}

	sessionPath := c.sessionPath(name)

	var state agent.State
	if err := state.Load(sessionPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		corrupt := sessionPath + ".corrupt"
		os.Rename(sessionPath, corrupt)
		log.Printf("agent %s: session unreadable, moved to %s: %v", name, corrupt, err)
		state = agent.State{}
	}

	a.Messages = state.Messages
	a.Usage = state.Usage

	ma := &managedAgent{
		agent:   a,
		root:    root,
		scratch: scratchDir,
	}
	ma.updateSnapshot()
	c.agents[name] = ma
	return ma, nil
}

func (c *Claw) AgentState(name string) ([]agent.Message, agent.Usage, bool) {
	ma := c.getAgent(name)
	if ma == nil {
		return nil, agent.Usage{}, false
	}

	ma.snapMu.Lock()
	defer ma.snapMu.Unlock()
	return ma.snapMessages, ma.snapUsage, true
}

func (c *Claw) getAgent(name string) *managedAgent {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.agents[name]
}

func (c *Claw) AgentDir(name string) string {
	return c.config.Memory.AgentDir(name)
}

// Send routes a message to its target agent and returns the turn stream.
// It frames the message, serializes the run, and persists the session when
// the stream ends. Returns nil if the agent does not exist.
func (c *Claw) Send(ctx context.Context, msg channel.Message) iter.Seq2[agent.Message, error] {
	ma := c.getAgent(msg.Agent)
	if ma == nil {
		return nil
	}

	return func(yield func(agent.Message, error) bool) {
		ma.mu.Lock()
		defer ma.mu.Unlock()

		if ma.notifyRoute == (channel.Route{}) && msg.Channel != "" {
			ma.notifyRoute = channel.Route{Channel: msg.Channel, Conversation: msg.Conversation}
		}

		ctx, cancel := context.WithTimeout(ctx, runTimeout)
		defer cancel()

		turn, err := ma.agent.Send(ctx, []agent.Content{{Text: frameMessage(msg)}})
		if err != nil {
			yield(agent.Message{}, err)
			return
		}

		for m, err := range turn {
			if !yield(m, err) {
				break
			}
		}

		c.saveSession(msg.Agent, ma)
		ma.updateSnapshot()
	}
}

// turnText assembles the text of a turn from streamed deltas. Deltas
// concatenate as-is; a blank line separates the text of consecutive
// assistant messages (split by reasoning or tool rounds).
type turnText struct {
	sink     func(string)
	wrote    bool
	boundary bool
}

func (t *turnText) add(m agent.Message) {
	for _, c := range m.Content {
		switch {
		case c.Text != "":
			t.text(c.Text)
		case c.Reasoning != nil, c.ToolCall != nil, c.ToolResult != nil:
			t.boundary = true
		}
	}
}

func (t *turnText) text(text string) {
	if t.wrote && t.boundary {
		t.sink("\n\n")
	}

	t.wrote = true
	t.boundary = false
	t.sink(text)
}

func (c *Claw) handleMessage(ctx context.Context, msg channel.Message) {
	ch := c.findChannel(msg.Channel)
	if ch == nil {
		log.Printf("no channel %q for message to agent %q", msg.Channel, msg.Agent)
		return
	}

	if c.config.Authorize != nil && !c.config.Authorize(msg) {
		ch.Send(ctx, msg.Conversation, "Not authorized.")
		return
	}

	turn := c.Send(ctx, msg)
	if turn == nil {
		ch.Send(ctx, msg.Conversation, fmt.Sprintf("Agent %q does not exist. Ask the main agent to create it.", msg.Agent))
		return
	}

	var stream io.WriteCloser
	if s, ok := ch.(channel.Streamer); ok {
		w, err := s.SendStream(ctx, msg.Conversation)
		if err != nil {
			log.Printf("failed to open stream: %v", err)
			return
		}
		stream = w
		defer stream.Close()
	}

	var buf strings.Builder

	tw := &turnText{
		sink: func(text string) {
			if stream != nil {
				stream.Write([]byte(text))
				return
			}
			buf.WriteString(text)
		},
	}

	for m, err := range turn {
		if err != nil {
			tw.boundary = true
			tw.text(fmt.Sprintf("error: %v", err))
			break
		}

		tw.add(m)
	}

	if stream == nil && buf.Len() > 0 {
		if err := ch.Send(ctx, msg.Conversation, buf.String()); err != nil {
			log.Printf("agent %s: failed to deliver reply: %v", msg.Agent, err)
		}
	}
}

func (c *Claw) sessionPath(name string) string {
	return filepath.Join(c.config.Memory.AgentDir(name), "session.json")
}

func (c *Claw) saveSession(name string, ma *managedAgent) {
	if c.getAgent(name) != ma {
		return
	}

	state := ma.agent.StateSnapshot()

	if err := state.Save(c.sessionPath(name)); err != nil {
		log.Printf("agent %s: failed to save session: %v", name, err)
	}
}

func (c *Claw) findChannel(name string) channel.Channel {
	for _, ch := range c.config.Channels {
		if ch.Name() == name {
			return ch
		}
	}
	return nil
}

const (
	promptTimeFormat  = "Monday, January 2, 2006 15:04 -07:00 (MST)"
	messageTimeFormat = "Mon, 2 Jan 2006 15:04 -07:00"
)

func (c *Claw) buildInstructions(name string) string {
	assistantName := c.config.AssistantName
	if assistantName == "" {
		assistantName = "Claw"
	}

	var b strings.Builder

	if soul := c.config.Memory.SoulContent(name); soul != "" {
		b.WriteString(soul)
		b.WriteString("\n\n")
	}

	fmt.Fprintf(&b, "You are %s (agent: %s).\n", assistantName, name)
	fmt.Fprintf(&b, "Current date and time: %s. Schedules run in this timezone.\n", time.Now().Format(promptTimeFormat))
	fmt.Fprintf(&b, "Working directory: %s\n", c.agentWorkDir(name))

	b.WriteString("\n")
	b.WriteString(prompt.Instructions)

	b.WriteString("\n")
	if name == "main" {
		b.WriteString(prompt.Main)
	} else {
		b.WriteString(prompt.Agent)
	}

	if c.config.Instructions != "" {
		b.WriteString("\n\n")
		b.WriteString(c.config.Instructions)
	}

	if global := c.config.Memory.GlobalContent(); global != "" {
		b.WriteString("\n\n# Shared Guidelines (global/AGENTS.md)\n\n")
		b.WriteString(global)
	}

	if local := c.config.Memory.AgentContent(name); local != "" {
		b.WriteString("\n\n# Your AGENTS.md\n\n")
		b.WriteString(local)
	}

	return b.String()
}

func (c *Claw) buildAgentContext(name string) string {
	var b strings.Builder

	b.WriteString("## Environment\n\n")
	fmt.Fprintf(&b, "- Current Date and Time: %s\n", time.Now().Format(promptTimeFormat))
	fmt.Fprintf(&b, "- Working Directory: %s\n", c.agentWorkDir(name))
	b.WriteString("\nThe file tools are rooted at the working directory: use relative paths with them. The shell runs on the host from the same directory.")

	if global := c.config.Memory.GlobalContent(); global != "" {
		b.WriteString("\n\n## Shared Guidelines\n\n")
		b.WriteString(global)
	}

	if local := c.config.Memory.AgentContent(name); local != "" {
		b.WriteString("\n\n## Guidelines (AGENTS.md)\n\n")
		b.WriteString(local)
	}

	return b.String()
}

func frameMessage(msg channel.Message) string {
	var b strings.Builder

	fmt.Fprintf(&b, "<message channel=%q", msg.Channel)

	if msg.Conversation != "" && msg.Conversation != msg.Agent {
		fmt.Fprintf(&b, " conversation=%q", msg.Conversation)
	}

	if msg.Sender != "" {
		fmt.Fprintf(&b, " sender=%q", msg.Sender)
	}

	fmt.Fprintf(&b, " time=%q>\n", time.Now().Format(messageTimeFormat))
	b.WriteString(msg.Content)
	b.WriteString("\n</message>")

	return b.String()
}

// Unframe strips the envelope frameMessage added, for display purposes.
func Unframe(text string) string {
	body, ok := strings.CutSuffix(text, "\n</message>")
	if !ok || !strings.HasPrefix(body, "<message ") {
		return text
	}

	if _, rest, ok := strings.Cut(body, "\n"); ok {
		return rest
	}

	return text
}

func (c *Claw) ensureDefaultTasks(name string) {
	agentDir := c.config.Memory.AgentDir(name)

	if schedule.HasTaskFile(agentDir) {
		return
	}

	err := schedule.Mutate(agentDir, func(tasks []schedule.Task) ([]schedule.Task, error) {
		task, err := schedule.NewTask(
			"Review your workspace for pending follow-ups: check README.md and your notes for items that are due, stale, or promised to the user, and handle or report anything that needs attention.",
			"every 1h",
		)
		if err != nil {
			return nil, err
		}

		return append(tasks, task), nil
	})
	if err != nil {
		log.Printf("warning: ensure default task for %q: %v", name, err)
	}
}

func auditHook(agentName string) hook.PreToolUse {
	return func(_ context.Context, call tool.ToolCall) (hook.PreToolUseOutcome, error) {
		switch call.Name {
		case "shell", "write", "edit":
			log.Printf("audit %s: %s %s", agentName, call.Name, tool.ExtractHint(call.Args, call.Name))
		}
		return hook.PreToolUseOutcome{}, nil
	}
}
