package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adrianliechti/wingman-cli/pkg/tool"
)

type Manager struct {
	*Config

	sessions map[string]*mcp.ClientSession
}

func New(cfg *Config) *Manager {
	if cfg == nil {
		cfg = &Config{}
	}

	return &Manager{
		Config: cfg,

		sessions: make(map[string]*mcp.ClientSession),
	}
}

func Load(path string) (*Manager, error) {
	cfg, err := loadConfig(path)

	if err != nil {
		return nil, err
	}

	return New(cfg), nil
}

func (m *Manager) Connect(ctx context.Context) error {
	var errs []error

	for name, server := range m.Servers {
		if err := m.connect(ctx, name, server); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (m *Manager) Close() {
	for _, s := range m.sessions {
		s.Close()
	}
}

func (m *Manager) connect(ctx context.Context, name string, server ServerConfig) error {
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "wingman",
		Version: "1.0.0",
	}, nil)

	transport, err := createTransport(server)

	if err != nil {
		return fmt.Errorf("MCP server %s: %w", name, err)
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	session, err := client.Connect(ctx, transport, nil)

	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}

	m.sessions[name] = session

	return nil
}

func createTransport(server ServerConfig) (mcp.Transport, error) {
	if server.Command != "" {
		cmd := exec.Command(server.Command, server.Args...)

		return &mcp.CommandTransport{
			Command: cmd,
		}, nil
	}

	if server.URL != "" {
		httpClient := http.DefaultClient

		if len(server.Headers) > 0 {
			httpClient = &http.Client{
				Transport: &headerTransport{
					base:    http.DefaultTransport,
					headers: server.Headers,
				},
			}
		}

		return &mcp.StreamableClientTransport{
			Endpoint: server.URL,

			HTTPClient: httpClient,
		}, nil
	}

	return nil, fmt.Errorf("no command or url configured")
}

func (m *Manager) Tools(ctx context.Context) ([]tool.Tool, error) {
	var tools []tool.Tool

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	for serverName, session := range m.sessions {
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

func convertTool(serverName string, session *mcp.ClientSession, mcpTool mcp.Tool) tool.Tool {
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

		Execute: func(env *tool.Environment, args map[string]any) (string, error) {
			return callTool(session, mcpTool.Name, args)
		},
	}
}

func callTool(session *mcp.ClientSession, name string, args map[string]any) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
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

func extractText(content []mcp.Content) string {
	var parts []string

	for _, c := range content {
		if text, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}

	return strings.Join(parts, "\n")
}
