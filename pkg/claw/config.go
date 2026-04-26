package claw

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/mcp"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/fetch"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/search"
	"github.com/adrianliechti/wingman-agent/pkg/claw/channel"
	"github.com/adrianliechti/wingman-agent/pkg/claw/channel/cli"
	"github.com/adrianliechti/wingman-agent/pkg/claw/memory"
)

type Config struct {
	AssistantName string

	AgentConfig *agent.Config

	MCP *mcp.Manager

	Tools        []tool.Tool
	Instructions string

	Memory   *memory.Store
	Channels []channel.Channel
}

func DefaultConfig() (*Config, func(), error) {
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

	tools := slices.Concat(fetch.Tools(), search.Tools())

	mainWorkspace := memoryStore.WorkspaceDir("main")
	mcpManager, _ := mcp.Load(filepath.Join(mainWorkspace, "mcp.json"))

	cfg := &Config{
		AssistantName: envOrDefault("ASSISTANT_NAME", "Claw"),
		AgentConfig:   agentCfg,
		MCP:           mcpManager,
		Tools:         tools,
		Memory:        memoryStore,
		Channels:      []channel.Channel{cli.New()},
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
