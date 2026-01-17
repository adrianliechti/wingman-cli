package config

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"text/template"
	"time"

	"github.com/adrianliechti/wingman-cli/pkg/skill"
	"github.com/adrianliechti/wingman-cli/pkg/tool"
	"github.com/adrianliechti/wingman-cli/pkg/tool/fs"
	"github.com/adrianliechti/wingman-cli/pkg/tool/mcp"
	"github.com/adrianliechti/wingman-cli/pkg/tool/shell"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

//go:embed instructions.txt
var instructionsTemplate string

type Config struct {
	Model  string
	Client openai.Client

	Environment  *tool.Environment
	Instructions string

	MCP *mcp.Manager

	Tools  []tool.Tool
	Skills []skill.Skill
}

func Default() (*Config, error) {
	workingDir, err := os.Getwd()

	if err != nil {
		return nil, fmt.Errorf("failed to get working directory: %w", err)
	}

	root, err := os.OpenRoot(workingDir)

	if err != nil {
		return nil, err
	}

	scratchDir := filepath.Join(os.TempDir(), fmt.Sprintf("wingman-%d", time.Now().Unix()))

	if err := os.MkdirAll(scratchDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create scratch directory: %w", err)
	}

	scratch, err := os.OpenRoot(scratchDir)

	if err != nil {
		return nil, fmt.Errorf("failed to open scratch directory: %w", err)
	}

	env := &tool.Environment{
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,

		Root:    root,
		Scratch: scratch,
	}

	tools := slices.Concat(fs.Tools(), shell.Tools())

	mcp, _ := mcp.Load(filepath.Join(workingDir, "mcp.json"))

	skills, _ := skill.Discover(workingDir)

	instructions, err := renderInstructions(env, skills)

	if err != nil {
		return nil, fmt.Errorf("failed to render instructions: %w", err)
	}

	client, model := createClient()

	return &Config{
		Client: client,
		Model:  model,

		Environment:  env,
		Instructions: instructions,

		MCP: mcp,

		Tools:  tools,
		Skills: skills,
	}, nil
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
			model = "gpt-5.2-codex"
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
			model = "gpt-5.2-codex"
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
	), "gpt-5.2-codex"
}

type instructionData struct {
	*tool.Environment
	Skills string
}

func renderInstructions(env *tool.Environment, skills []skill.Skill) (string, error) {
	tmpl, err := template.New("instructions").Parse(instructionsTemplate)
	if err != nil {
		return "", err
	}

	data := instructionData{
		Environment: env,
		Skills:      skill.FormatForPrompt(skills),
	}

	var buf bytes.Buffer

	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}

	return buf.String(), nil
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
