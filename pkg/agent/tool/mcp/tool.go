package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adrianliechti/wingman-agent/pkg/agent/mcp"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func Tools(ctx context.Context, m *mcp.Manager) ([]tool.Tool, error) {
	var tools []tool.Tool

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	for serverName, session := range m.Sessions() {
		result, err := session.ListTools(ctx, nil)

		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to list tools from MCP server %s: %v\n", serverName, err)
			continue
		}

		for _, mcpTool := range result.Tools {
			t := convertTool(serverName, session, *mcpTool)
			tools = append(tools, t)
		}
	}

	return tools, nil
}

func convertTool(serverName string, session *sdkmcp.ClientSession, mcpTool sdkmcp.Tool) tool.Tool {
	prefixedName := fmt.Sprintf("%s_%s", serverName, mcpTool.Name)

	var params map[string]any

	if mcpTool.InputSchema != nil {
		if schema, ok := mcpTool.InputSchema.(map[string]any); ok {
			params = schema
		} else if schemaBytes, err := json.Marshal(mcpTool.InputSchema); err == nil {
			json.Unmarshal(schemaBytes, &params)
		}
	}

	if params == nil {
		params = map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}

	return tool.Tool{
		Name:        prefixedName,
		Description: mcpTool.Description,

		Parameters: params,

		Execute: func(ctx context.Context, env *tool.Environment, args map[string]any) (string, error) {
			return callTool(ctx, session, mcpTool.Name, args)
		},
	}
}

func callTool(ctx context.Context, session *sdkmcp.ClientSession, name string, args map[string]any) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})

	if err != nil {
		return "", fmt.Errorf("MCP tool call failed: %w", err)
	}

	if result.IsError {
		return "", fmt.Errorf("MCP tool returned error: %s", extractText(result.Content))
	}

	return extractText(result.Content), nil
}

func extractText(content []sdkmcp.Content) string {
	var parts []string

	for _, c := range content {
		if text, ok := c.(*sdkmcp.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}

	return strings.Join(parts, "\n")
}
