package config

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

	"github.com/adrianliechti/wingman-cli/pkg/prompt"
	"github.com/adrianliechti/wingman-cli/pkg/skill"
	"github.com/adrianliechti/wingman-cli/pkg/tool"
	"github.com/adrianliechti/wingman-cli/pkg/tool/fs"
	"github.com/adrianliechti/wingman-cli/pkg/tool/mcp"
	"github.com/adrianliechti/wingman-cli/pkg/tool/shell"
)

// AvailableModels lists supported models in priority order
var AvailableModels = []string{
	"claude-opus-4-5",
	"claude-sonnet-4-5",
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

	MaxContextTokens int64
	ReserveTokens    int64
	KeepRecentTokens int64

	MCP *mcp.Manager

	Tools  []tool.Tool
	Skills []skill.Skill
}

func Default() (*Config, func(), error) {
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
	}

	tools := slices.Concat(fs.Tools(), shell.Tools())

	mcp, _ := mcp.Load(filepath.Join(wd, "mcp.json"))

	skills, _ := skill.Discover(wd)

	instructions := readAgentsFile(wd)

	agentinstructions, err := renderAgentInstructions(env, instructions, skills)

	if err != nil {
		return nil, nil, fmt.Errorf("failed to render instructions: %w", err)
	}

	planningInstructions, err := renderPlanningInstructions(env, instructions, skills)

	if err != nil {
		return nil, nil, fmt.Errorf("failed to render planning instructions: %w", err)
	}

	client, model := createClient()

	cfg := &Config{
		Client: client,
		Model:  model,

		Environment:          env,
		AgentInstructions:    agentinstructions,
		PlanningInstructions: planningInstructions,

		// MaxContextTokens: 5000,
		// ReserveTokens:    1000,
		// KeepRecentTokens: 2000,

		MaxContextTokens: 180_000,
		ReserveTokens:    16_000,
		KeepRecentTokens: 20_000,

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

		if model == "" {
			model = AvailableModels[0]
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

		if model == "" {
			model = AvailableModels[0]
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
	), AvailableModels[0]
}

type instructionData struct {
	*tool.Environment
	Skills       string
	Instructions string
}

func renderAgentInstructions(env *tool.Environment, instructions string, skills []skill.Skill) (string, error) {
	data := instructionData{
		Environment:  env,
		Skills:       skill.FormatForPrompt(skills),
		Instructions: instructions,
	}

	return prompt.Render(prompt.Instructions, data)
}

func renderPlanningInstructions(env *tool.Environment, instructions string, skills []skill.Skill) (string, error) {
	data := instructionData{
		Environment:  env,
		Skills:       skill.FormatForPrompt(skills),
		Instructions: instructions,
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
