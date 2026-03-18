package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Manager struct {
	*Config

	sessions map[string]*mcp.ClientSession
}

func NewManager(cfg *Config) *Manager {
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

	return NewManager(cfg), nil
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

func (m *Manager) Sessions() map[string]*mcp.ClientSession {
	return m.sessions
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
