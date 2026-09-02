package mcp

import (
	"context"
	"slices"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestToolsForServerCollectsAllPages(t *testing.T) {
	server := sdkmcp.NewServer(
		&sdkmcp.Implementation{Name: "test", Version: "1.0.0"},
		&sdkmcp.ServerOptions{PageSize: 1},
	)
	for _, name := range []string{"third", "first", "second"} {
		sdkmcp.AddTool(server, &sdkmcp.Tool{Name: name},
			func(context.Context, *sdkmcp.CallToolRequest, struct{}) (*sdkmcp.CallToolResult, any, error) {
				return &sdkmcp.CallToolResult{}, nil, nil
			})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	clientTransport, serverTransport := sdkmcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "client", Version: "1.0.0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()

	tools, err := ToolsForServer(ctx, "remote", clientSession)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
		if tool.Telemetry.ToolType != "extension" || tool.Telemetry.MCPMethod != "tools/call" || tool.Telemetry.MCPProtocolVersion == "" {
			t.Fatalf("tool %q telemetry = %#v", tool.Name, tool.Telemetry)
		}
	}
	if want := []string{"remote_first", "remote_second", "remote_third"}; !slices.Equal(names, want) {
		t.Fatalf("tool names = %v, want %v", names, want)
	}
}
