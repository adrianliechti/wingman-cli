package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/adrianliechti/wingman-agent/pkg/agent/mcp"
	"github.com/adrianliechti/wingman-agent/pkg/agent/memory"
	"github.com/adrianliechti/wingman-agent/pkg/agent/plan"
	"github.com/adrianliechti/wingman-agent/pkg/agent/prompt"
	"github.com/adrianliechti/wingman-agent/pkg/agent/skill"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/ask"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/fetch"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/fs"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/search"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/shell"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/subagent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tracker"
)

// AvailableModels lists supported models in priority order
var AvailableModels = []string{
	"claude-opus-4-6",
	"claude-opus-4-5",

	"claude-sonnet-4-6",
	"claude-sonnet-4-5",

	"gpt-5.4",
	"gpt-5.3-codex",
	"gpt-5.2-codex",
	"gpt-5.2",
	"gpt-5.1-codex-max",
	"gpt-5.1-codex",
	"gpt-5.1",
	"gpt-5-codex",
	"gpt-5",
}

type Config struct {
	Model  string
	Client openai.Client

	Environment *tool.Environment

	AgentInstructions    string
	PlanningInstructions string

	MCP *mcp.Manager

	Tools  []tool.Tool
	Skills []skill.Skill
}

func DefaultConfig() (*Config, func(), error) {
	wd, err := os.Getwd()

	if err != nil {
		return nil, nil, fmt.Errorf("failed to get working directory: %w", err)
	}

	root, err := os.OpenRoot(wd)

	if err != nil {
		return nil, nil, err
	}

	scratchDir := filepath.Join(os.TempDir(), fmt.Sprintf("wingman-%d", time.Now().Unix()))

	if err := os.MkdirAll(scratchDir, 0755); err != nil {
		return nil, nil, fmt.Errorf("failed to create scratch directory: %w", err)
	}

	scratch, err := os.OpenRoot(scratchDir)

	if err != nil {
		return nil, nil, fmt.Errorf("failed to open scratch directory: %w", err)
	}

	env := &tool.Environment{
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,

		Root:    root,
		Scratch: scratch,

		Tracker: tracker.New(),
	}

	// Initialize memory directory
	memDir := projectMemoryDir(wd)

	if err := os.MkdirAll(memDir, 0755); err != nil {
		return nil, nil, fmt.Errorf("failed to create memory directory: %w", err)
	}

	memRoot, err := os.OpenRoot(memDir)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open memory directory: %w", err)
	}

	env.Memory = memRoot

	tools := slices.Concat(fs.Tools(), shell.Tools(), fetch.Tools(), search.Tools(), ask.Tools())

	mcp, _ := mcp.Load(filepath.Join(wd, "mcp.json"))

	skills := skill.Merge(skill.BundledSkills(), skill.MustDiscover(wd))

	client, model := createClient()

	// Add sub-agent tool (needs client + model + the other tools)
	tools = append(tools, subagent.SubAgentTool(client, model, tools))

	cfg := &Config{
		Client: client,
		Model:  model,

		Environment: env,

		AgentInstructions:    prompt.Instructions,
		PlanningInstructions: prompt.Planning,

		MCP: mcp,

		Tools:  tools,
		Skills: skills,
	}

	return cfg, cfg.Cleanup, nil
}

func createClient() (openai.Client, string) {
	if url, ok := os.LookupEnv("WINGMAN_URL"); ok {
		baseURL := strings.TrimRight(url, "/") + "/v1"

		token, _ := os.LookupEnv("WINGMAN_TOKEN")
		model := os.Getenv("WINGMAN_MODEL")

		if token == "" {
			token = "-"
		}

		client := openai.NewClient(
			option.WithBaseURL(baseURL),
			option.WithAPIKey(token),
		)

		return client, model
	}

	if token, ok := os.LookupEnv("OPENAI_API_KEY"); ok {
		baseURL := "https://api.openai.com/v1"
		model := os.Getenv("OPENAI_MODEL")

		if url, ok := os.LookupEnv("OPENAI_BASE_URL"); ok {
			baseURL = url
		}

		if m, ok := os.LookupEnv("OPENAI_MODEL"); ok {
			model = m
		}

		client := openai.NewClient(
			option.WithBaseURL(baseURL),
			option.WithAPIKey(token),
		)

		return client, model
	}

	return openai.NewClient(
		option.WithBaseURL("http://localhost:8080/v1"),
		option.WithAPIKey("-"),
	), ""
}

func (a *Agent) BuildInstructions(planMode bool, bridgeInstructions string) string {
	if a == nil || a.Config == nil {
		return ""
	}

	return a.Config.BuildInstructions(planMode, bridgeInstructions)
}

func (c *Config) BuildInstructions(planMode bool, bridgeInstructions string) string {
	if c == nil {
		return ""
	}

	base := c.AgentInstructions
	if planMode {
		base = c.PlanningInstructions
	}

	memDir := c.Environment.MemoryDir()

	data := prompt.SectionData{
		PlanMode:            planMode,
		Date:                time.Now().Format("January 2, 2006"),
		OS:                  c.Environment.OS,
		Arch:                c.Environment.Arch,
		WorkingDir:          c.Environment.WorkingDir(),
		MemoryDir:           memDir,
		MemoryContent:       memory.Load(memDir),
		PlanFile:            c.Environment.PlanFile(),
		PlanContent:         plan.Load(memDir),
		Skills:              skill.FormatForPrompt(c.Skills),
		ProjectInstructions: readAgentsFile(c.Environment.WorkingDir()),
		BridgeInstructions:  bridgeInstructions,
	}

	sections := append([]prompt.Section{{Content: base}}, prompt.RenderSections(data)...)
	return prompt.ComposeSections(sections...)
}

func projectMemoryDir(workingDir string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}

	sanitized := strings.TrimPrefix(filepath.Clean(workingDir), string(filepath.Separator))
	sanitized = strings.ReplaceAll(sanitized, string(filepath.Separator), "_")

	return filepath.Join(home, ".wingman", "projects", sanitized, "memory")
}

func (c *Config) Cleanup() {
	if c.MCP != nil {
		c.MCP.Close()
	}

	if c.Environment == nil {
		return
	}

	if c.Environment.Scratch != nil {
		scratchDir := c.Environment.Scratch.Name()
		c.Environment.Scratch.Close()
		os.RemoveAll(scratchDir)
	}

	if c.Environment.Memory != nil {
		c.Environment.Memory.Close()
	}

	if c.Environment.Root != nil {
		c.Environment.Root.Close()
	}
}

func readAgentsFile(wd string) string {
	data, err := os.ReadFile(filepath.Join(wd, "AGENTS.md"))

	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(data))
}
