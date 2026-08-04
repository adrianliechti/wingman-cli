package mcp

import (
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

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
