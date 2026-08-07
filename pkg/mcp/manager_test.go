package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestManagerForwardsToolListChangedWithServerName(t *testing.T) {
	manager := NewManager(&Config{Servers: map[string]ServerConfig{}})
	changed := make(chan string, 1)
	manager.SetToolListChangedHandler(func(serverName string) {
		changed <- serverName
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	clientTransport, serverTransport := sdkmcp.NewInMemoryTransports()
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
	sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "initial"},
		func(context.Context, *sdkmcp.CallToolRequest, struct{}) (*sdkmcp.CallToolResult, any, error) {
			return &sdkmcp.CallToolResult{}, nil, nil
		})
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	clientSession, err := manager.newClient("dynamic").Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()

	sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "added"},
		func(context.Context, *sdkmcp.CallToolRequest, struct{}) (*sdkmcp.CallToolResult, any, error) {
			return &sdkmcp.CallToolResult{}, nil, nil
		})

	select {
	case got := <-changed:
		if got != "dynamic" {
			t.Fatalf("server name = %q, want dynamic", got)
		}
	case <-ctx.Done():
		t.Fatal("tool list change was not forwarded")
	}
}

func TestCreateCommandTransportIncludesServerEnvironment(t *testing.T) {
	t.Setenv("MCP_PARENT", "parent")
	transport, err := createTransport(ServerConfig{
		Command: "server",
		Args:    []string{"--stdio"},
		Env:     map[string]string{"MCP_TOKEN": "secret"},
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	command, ok := transport.(*sdkmcp.CommandTransport)
	if !ok {
		t.Fatalf("transport type = %T", transport)
	}
	if command.Command.Dir == "" || command.Command.Path == "" {
		t.Fatalf("command = %#v", command.Command)
	}
	joined := strings.Join(command.Command.Env, "\n")
	if !strings.Contains(joined, "MCP_PARENT=parent") || !strings.Contains(joined, "MCP_TOKEN=secret") {
		t.Fatalf("environment missing parent or server value: %q", joined)
	}
	if len(command.Command.Env) == 0 {
		t.Fatal("command environment was not set")
	}
}
