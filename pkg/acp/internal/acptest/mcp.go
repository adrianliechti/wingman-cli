package acptest

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"testing"
)

const (
	// MCPSecretWord is returned by the helper MCP server's only tool; live
	// scenarios assert the model relays it, proving the client-supplied MCP
	// server was wired through to the backend and actually called.
	MCPSecretWord = "MELONBALL"
	// MCPToolName is the tool the helper MCP server exposes.
	MCPToolName = "acp_live_secret"

	mcpHelperEnv = "ACP_LIVE_MCP_SERVER"
)

// MCPServerHelper is the body for a per-bridge helper test that the live
// suite re-executes (via CommandHelper) as a standalone stdio MCP server.
// In normal test runs it skips.
func MCPServerHelper(t *testing.T) {
	if os.Getenv(mcpHelperEnv) == "" {
		t.Skip("MCP helper process; only meaningful when spawned by the live suite")
	}
	serveMCP(os.Stdin, os.Stdout)
	os.Exit(0)
}

func serveMCP(r io.Reader, w io.Writer) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	enc := json.NewEncoder(w)
	for scanner.Scan() {
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			continue
		}
		if len(msg.ID) == 0 || string(msg.ID) == "null" {
			continue
		}

		var result any
		switch msg.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			if p.ProtocolVersion == "" {
				p.ProtocolVersion = "2024-11-05"
			}
			result = map[string]any{
				"protocolVersion": p.ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "acp-live", "version": "1.0.0"},
			}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{
				"name":        MCPToolName,
				"description": "Returns the ACP live secret word.",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
			}}}
		case "tools/call":
			result = map[string]any{
				"content": []any{map[string]any{"type": "text", "text": MCPSecretWord}},
				"isError": false,
			}
		case "ping":
			result = map[string]any{}
		default:
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{"code": -32601, "message": "method not found"}})
			continue
		}
		_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": result})
	}
}
