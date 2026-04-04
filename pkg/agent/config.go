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
	"github.com/adrianliechti/wingman-agent/pkg/agent/prompt"
	"github.com/adrianliechti/wingman-agent/pkg/agent/skill"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/ask"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/fetch"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/fs"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/search"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/shell"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/subagent"
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
		Date: time.Now().Format("January 2, 2006"),

		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,

		Root:    root,
		Scratch: scratch,
	}

	// Initialize memory directory
	memDir := memory.Dir(wd)
	memory.EnsureDir(memDir)

	memRoot, err := os.OpenRoot(memDir)
	if err == nil {
		env.Memory = memRoot
	}

	memContent := memory.LoadEntrypoint(memDir)

	tools := slices.Concat(fs.Tools(), shell.Tools(), fetch.Tools(), search.Tools(), ask.Tools())

	mcp, _ := mcp.Load(filepath.Join(wd, "mcp.json"))

	skills, _ := skill.Discover(wd)

	instructions := readAgentsFile(wd)

	agentinstructions, err := renderAgentInstructions(env, instructions, skills, memDir, memContent)

	if err != nil {
		return nil, nil, fmt.Errorf("failed to render instructions: %w", err)
	}

	planningInstructions, err := renderPlanningInstructions(env, instructions, skills, memDir, memContent)

	if err != nil {
		return nil, nil, fmt.Errorf("failed to render planning instructions: %w", err)
	}

	client, model := createClient()

	// Add sub-agent tool (needs client + model + the other tools)
	tools = append(tools, subagent.SubAgentTool(client, model, tools))

	cfg := &Config{
		Client: client,
		Model:  model,

		Environment: env,

		AgentInstructions:    agentinstructions,
		PlanningInstructions: planningInstructions,

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

type instructionData struct {
	*tool.Environment
	Skills        string
	Instructions  string
	MemoryDir     string
	MemoryContent string
}

func renderAgentInstructions(env *tool.Environment, instructions string, skills []skill.Skill, memDir, memContent string) (string, error) {
	data := instructionData{
		Environment:   env,
		Skills:        skill.FormatForPrompt(skills),
		Instructions:  instructions,
		MemoryDir:     memDir,
		MemoryContent: memContent,
	}

	return prompt.Render(prompt.Instructions, data)
}

func renderPlanningInstructions(env *tool.Environment, instructions string, skills []skill.Skill, memDir, memContent string) (string, error) {
	data := instructionData{
		Environment:   env,
		Skills:        skill.FormatForPrompt(skills),
		Instructions:  instructions,
		MemoryDir:     memDir,
		MemoryContent: memContent,
	}

	return prompt.Render(prompt.Planning, data)
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
