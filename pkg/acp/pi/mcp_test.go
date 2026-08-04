package pi

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/acp/internal/acptest"
	acp "github.com/coder/acp-go-sdk"
)

func TestPrepareMCPBridgeConfiguresStdioServers(t *testing.T) {
	cwd := t.TempDir()
	bridge, err := prepareMCPBridge(cwd, []string{"PATH=/contract/bin", "KEEP=yes"}, []acp.McpServer{{
		Stdio: &acp.McpServerStdio{
			Name: "files", Command: "server", Args: []string{"--root", cwd},
			Env: []acp.EnvVariable{{Name: "TOKEN", Value: "secret"}},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.cleanup()

	if len(bridge.args) != 2 || bridge.args[0] != "--extension" {
		t.Fatalf("bridge args = %v", bridge.args)
	}
	if bridge.statusPath == "" {
		t.Fatal("bridge has no readiness path")
	}

	var configPath string
	for _, value := range bridge.env {
		if strings.HasPrefix(value, mcpConfigEnv+"=") {
			configPath = strings.TrimPrefix(value, mcpConfigEnv+"=")
		}
	}
	if configPath == "" {
		t.Fatalf("bridge env has no %s: %v", mcpConfigEnv, bridge.env)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config mcpBridgeConfig
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config.Cwd != cwd || len(config.Servers) != 1 {
		t.Fatalf("bridge config = %+v", config)
	}
	server := config.Servers[0]
	if server.Name != "files" || server.Command != "server" || server.Env["TOKEN"] != "secret" {
		t.Fatalf("bridge server = %+v", server)
	}
}

func TestPrepareMCPBridgeRejectsUnsupportedTransport(t *testing.T) {
	bridge, err := prepareMCPBridge(t.TempDir(), nil, []acp.McpServer{{
		Http: &acp.McpServerHttpInline{Name: "remote", Url: "https://example.invalid/mcp"},
	}})
	if bridge != nil || err == nil || !strings.Contains(err.Error(), "HTTP transport is not supported") {
		t.Fatalf("bridge = %v, error = %v", bridge, err)
	}
}

func TestLiveMCPBridgeStartup(t *testing.T) {
	if os.Getenv("PI_MCP_BRIDGE_LIVE") == "" {
		t.Skip("set PI_MCP_BRIDGE_LIVE=1 to exercise the bundled extension with installed Pi")
	}
	piPath, err := exec.LookPath("pi")
	if err != nil {
		t.Skipf("pi not found: %v", err)
	}
	serverPath, _, _ := acptest.CommandHelper(t, "TestMCPServerHelper", "ACP_LIVE_MCP_SERVER")
	agentDir := t.TempDir()
	env := setEnvValue(os.Environ(), "PI_CODING_AGENT_DIR", agentDir)
	agent := New(Options{Path: piPath, Env: env})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	proc, bridge, err := agent.spawnSessionProcess(ctx, t.TempDir(), nil, []acp.McpServer{{
		Stdio: &acp.McpServerStdio{
			Name: "live", Command: serverPath, Args: []string{},
			Env: []acp.EnvVariable{{Name: "ACP_LIVE_MCP_SERVER", Value: "1"}},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	proc.dispose()
	bridge.cleanup()
}
