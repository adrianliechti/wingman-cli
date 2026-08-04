package pi

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/acp-go-sdk"

	acpcommon "github.com/adrianliechti/wingman-agent/pkg/acp"
)

const mcpConfigEnv = "WINGMAN_PI_MCP_CONFIG"

//go:embed mcp_bridge.js
var mcpBridgeSource []byte

type mcpBridgeServer struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

type mcpBridgeConfig struct {
	Cwd        string            `json:"cwd"`
	StatusPath string            `json:"statusPath"`
	Servers    []mcpBridgeServer `json:"servers"`
}

type preparedMCPBridge struct {
	args       []string
	env        []string
	statusPath string
	cleanup    func()
}

func prepareMCPBridge(cwd string, baseEnv []string, servers []acp.McpServer) (*preparedMCPBridge, error) {
	if err := acpcommon.ValidateMCPServers(servers, acp.McpCapabilities{}); err != nil {
		return nil, err
	}
	if len(servers) == 0 {
		return &preparedMCPBridge{env: baseEnv, cleanup: func() {}}, nil
	}

	dir, err := os.MkdirTemp("", "wingman-pi-mcp-")
	if err != nil {
		return nil, fmt.Errorf("create Pi MCP bridge directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	extensionPath := filepath.Join(dir, "mcp_bridge.js")
	configPath := filepath.Join(dir, "mcp.json")
	statusPath := filepath.Join(dir, "status.json")

	config := mcpBridgeConfig{Cwd: cwd, StatusPath: statusPath, Servers: make([]mcpBridgeServer, 0, len(servers))}
	for _, server := range servers {
		stdio := server.Stdio
		env := make(map[string]string, len(stdio.Env))
		for _, variable := range stdio.Env {
			env[variable.Name] = variable.Value
		}
		config.Servers = append(config.Servers, mcpBridgeServer{
			Name: stdio.Name, Command: stdio.Command, Args: stdio.Args, Env: env,
		})
	}

	data, err := json.Marshal(config)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("encode Pi MCP bridge config: %w", err)
	}
	if err := os.WriteFile(extensionPath, mcpBridgeSource, 0o600); err != nil {
		cleanup()
		return nil, fmt.Errorf("write Pi MCP bridge extension: %w", err)
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		cleanup()
		return nil, fmt.Errorf("write Pi MCP bridge config: %w", err)
	}

	env := baseEnv
	if env == nil {
		env = os.Environ()
	} else {
		env = append([]string(nil), env...)
	}
	env = setEnvValue(env, mcpConfigEnv, configPath)

	return &preparedMCPBridge{
		args:       []string{"--extension", extensionPath},
		env:        env,
		statusPath: statusPath,
		cleanup:    cleanup,
	}, nil
}

func setEnvValue(env []string, name, value string) []string {
	prefix := name + "="
	for i := range env {
		if strings.HasPrefix(env[i], prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func (b *preparedMCPBridge) waitReady(ctx context.Context, proc *process) error {
	if b.statusPath == "" {
		return nil
	}

	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	for {
		if data, err := os.ReadFile(b.statusPath); err == nil {
			var status struct {
				Ready bool   `json:"ready"`
				Error string `json:"error"`
			}
			if json.Unmarshal(data, &status) == nil {
				if status.Error != "" {
					return fmt.Errorf("initialize Pi MCP bridge: %s", status.Error)
				}
				if status.Ready {
					return nil
				}
			}
		}

		select {
		case <-waitCtx.Done():
			return fmt.Errorf("initialize Pi MCP bridge: %w", waitCtx.Err())
		case <-proc.done:
			return fmt.Errorf("initialize Pi MCP bridge: %w", errProcessClosed)
		case <-ticker.C:
		}
	}
}

func (b *preparedMCPBridge) resetReady() error {
	if b.statusPath == "" {
		return nil
	}
	if err := os.Remove(b.statusPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reset Pi MCP bridge readiness: %w", err)
	}
	return nil
}
