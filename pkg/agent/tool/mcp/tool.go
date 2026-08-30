package mcp

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/mcp"
	"github.com/adrianliechti/wingman-agent/pkg/text"
)

const (
	listToolsTimeout = 30 * time.Second

	callToolTimeout = 5 * time.Minute

	// maxDescriptionBytes bounds what a single MCP tool can permanently add to
	// every request; verbose servers otherwise inflate the prompt unbounded.
	maxDescriptionBytes = 2 * 1024
)

func Tools(ctx context.Context, m *mcp.Manager) ([]tool.Tool, error) {
	var tools []tool.Tool

	sessions := m.Sessions()

	for _, serverName := range slices.Sorted(maps.Keys(sessions)) {
		session := sessions[serverName]
		serverTools, err := ToolsForServer(ctx, serverName, session)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to list tools from MCP server %s: %v\n", serverName, err)
			continue
		}
		tools = append(tools, serverTools...)
	}

	return tools, nil
}

func ToolsForServer(ctx context.Context, serverName string, session *sdkmcp.ClientSession) ([]tool.Tool, error) {
	ctx, cancel := context.WithTimeout(ctx, listToolsTimeout)
	defer cancel()

	var listed []*sdkmcp.Tool
	for mcpTool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		listed = append(listed, mcpTool)
	}

	slices.SortFunc(listed, func(a, b *sdkmcp.Tool) int {
		return cmp.Compare(a.Name, b.Name)
	})

	tools := make([]tool.Tool, 0, len(listed))
	for _, mcpTool := range listed {
		tools = append(tools, convertTool(serverName, session, *mcpTool))
	}
	return tools, nil
}

func convertTool(serverName string, session *sdkmcp.ClientSession, mcpTool sdkmcp.Tool) tool.Tool {
	effect := tool.EffectMutates
	if mcpTool.Annotations != nil && mcpTool.Annotations.ReadOnlyHint {
		effect = tool.EffectReadOnly
	}
	protocolVersion := ""
	if initialized := session.InitializeResult(); initialized != nil {
		protocolVersion = initialized.ProtocolVersion
	}

	return tool.Tool{
		Name:        fmt.Sprintf("%s_%s", serverName, mcpTool.Name),
		Description: text.TruncateHead(mcpTool.Description, maxDescriptionBytes),
		Effect:      tool.StaticEffect(effect),
		Parameters:  schemaToParams(serverName, mcpTool),
		Telemetry: tool.TelemetryMetadata{
			ToolType:           "extension",
			MCPMethod:          "tools/call",
			MCPProtocolVersion: protocolVersion,
		},
		Execute: func(ctx context.Context, args map[string]any) (tool.Result, error) {
			return callTool(ctx, session, mcpTool.Name, args)
		},
	}
}

func schemaToParams(serverName string, mcpTool sdkmcp.Tool) map[string]any {
	empty := map[string]any{"type": "object", "properties": map[string]any{}}

	if mcpTool.InputSchema == nil {
		return empty
	}
	if schema, ok := mcpTool.InputSchema.(map[string]any); ok {
		return schema
	}

	schemaBytes, err := json.Marshal(mcpTool.InputSchema)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: marshal schema for %s_%s: %v\n", serverName, mcpTool.Name, err)
		return empty
	}
	var params map[string]any
	if err := json.Unmarshal(schemaBytes, &params); err != nil {
		fmt.Fprintf(os.Stderr, "warning: unmarshal schema for %s_%s: %v\n", serverName, mcpTool.Name, err)
		return empty
	}
	return params
}

func callTool(ctx context.Context, session *sdkmcp.ClientSession, name string, args map[string]any) (tool.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, callToolTimeout)
	defer cancel()

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})

	if err != nil {
		return tool.Result{}, fmt.Errorf("MCP tool call failed: %w", err)
	}

	content := extractText(result.Content)

	// The provider codec transmits only the content string, so the failure
	// marker must live in the text for the model to see it.
	if result.IsError {
		if content == "" {
			content = "(no error details)"
		}
		return tool.Error("error: MCP tool returned error: " + content), nil
	}

	return tool.Text(content), nil
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
