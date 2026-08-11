package claw

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/claw/channel"
	"github.com/adrianliechti/wingman-agent/pkg/claw/memory"
	"github.com/adrianliechti/wingman-agent/pkg/mcp"
)

type Config struct {
	AssistantName string

	AgentConfig *agent.Config

	MCP *mcp.Manager

	Tools        []tool.Tool
	Instructions string

	Memory   *memory.Store
	Channels []channel.Channel

	// Authorize gates inbound messages; nil allows everything.
	Authorize func(msg channel.Message) bool
}

func DefaultConfig() (*Config, func(), error) {
	if os.Getenv("WINGMAN_URL") == "" && os.Getenv("OPENAI_API_KEY") == "" {
		fmt.Fprintln(os.Stderr, "warning: neither WINGMAN_URL nor OPENAI_API_KEY is set; falling back to http://localhost:4242/v1")
	}

	agentCfg, err := agent.DefaultConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create config: %w", err)
	}

	homeDir, _ := os.UserHomeDir()
	dataDir := filepath.Join(homeDir, ".wingman", "claw")

	memoryStore, err := memory.NewStore(filepath.Join(dataDir, "agents"))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create memory store: %w", err)
	}

	if err := memoryStore.EnsureAgent("main"); err != nil {
		return nil, nil, fmt.Errorf("failed to create main agent: %w", err)
	}

	tools := []tool.Tool{}

	mainWorkspace := memoryStore.WorkspaceDir("main")
	mcpManager, _ := mcp.Load(filepath.Join(mainWorkspace, "mcp.json"))
	if mcpManager != nil {
		mcpManager.Dir = mainWorkspace
	}

	cfg := &Config{
		AssistantName: envOrDefault("ASSISTANT_NAME", "Claw"),
		AgentConfig:   agentCfg,
		MCP:           mcpManager,
		Tools:         tools,
		Memory:        memoryStore,
	}

	cleanup := func() {
		if cfg.MCP != nil {
			cfg.MCP.Close()
		}
	}

	return cfg, cleanup, nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
