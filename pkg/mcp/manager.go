package mcp

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adrianliechti/wingman-agent/internal/process"
	"github.com/adrianliechti/wingman-agent/pkg/httpclient"
)

type Manager struct {
	*Config

	Dir string

	elicit atomic.Pointer[ElicitFunc]

	toolListChanged atomic.Pointer[ToolListChangedFunc]

	mu       sync.RWMutex
	sessions map[string]*mcp.ClientSession
}

type ToolListChangedFunc func(serverName string)

func NewManager(cfg *Config) *Manager {
	return &Manager{
		Config: cfg,

		sessions: make(map[string]*mcp.ClientSession),
	}
}

func Load(paths ...string) (*Manager, error) {
	merged := &Config{Servers: map[string]ServerConfig{}}

	var found bool

	for _, path := range paths {
		if path == "" {
			continue
		}

		cfg, err := loadConfig(path)

		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}

			return nil, err
		}

		found = true
		maps.Copy(merged.Servers, cfg.Servers)
	}

	if !found {
		return nil, os.ErrNotExist
	}

	return NewManager(merged), nil
}

func (m *Manager) Connect(ctx context.Context) error {
	var errs []error

	for _, name := range slices.Sorted(maps.Keys(m.Servers)) {
		if err := m.connect(ctx, name, m.Servers[name]); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (m *Manager) AddServer(ctx context.Context, name string, server ServerConfig) error {
	if m.Servers == nil {
		m.Servers = make(map[string]ServerConfig)
	}

	m.Servers[name] = server
	return m.connect(ctx, name, server)
}

func (m *Manager) AddSession(name string, session *mcp.ClientSession) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sessions[name] = session
}

func (m *Manager) SetToolListChangedHandler(handler ToolListChangedFunc) {
	if handler == nil {
		m.toolListChanged.Store(nil)
		return
	}
	m.toolListChanged.Store(&handler)
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, s := range m.sessions {
		s.Close()
	}
}

func (m *Manager) Sessions() map[string]*mcp.ClientSession {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]*mcp.ClientSession, len(m.sessions))
	maps.Copy(result, m.sessions)
	return result
}

func (m *Manager) Session(name string) *mcp.ClientSession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[name]
}

func (m *Manager) connect(ctx context.Context, name string, server ServerConfig) error {
	client := m.newClient(name)

	transport, err := createTransport(server, m.Dir)

	if err != nil {
		return fmt.Errorf("MCP server %s: %w", name, err)
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	session, err := client.Connect(ctx, transport, nil)

	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}

	m.mu.Lock()
	m.sessions[name] = session
	m.mu.Unlock()

	return nil
}

func (m *Manager) newClient(name string) *mcp.Client {
	return mcp.NewClient(&mcp.Implementation{
		Name:    "wingman",
		Version: "1.0.0",
	}, &mcp.ClientOptions{
		ElicitationHandler: m.handleElicitation,
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) {
			if handler := m.toolListChanged.Load(); handler != nil {
				(*handler)(name)
			}
		},
	})
}

func createTransport(server ServerConfig, dir string) (mcp.Transport, error) {
	if server.Dir != "" {
		dir = server.Dir
	}

	if server.Command != "" {
		cmd := exec.Command(server.Command, server.Args...)
		process.Hide(cmd)
		cmd.Dir = dir
		if len(server.Env) > 0 {
			cmd.Env = os.Environ()
			for _, name := range slices.Sorted(maps.Keys(server.Env)) {
				cmd.Env = append(cmd.Env, name+"="+server.Env[name])
			}
		}

		return &mcp.CommandTransport{
			Command: cmd,
		}, nil
	}

	if server.URL != "" {
		httpClient := http.DefaultClient

		if len(server.Headers) > 0 {
			var err error
			httpClient, err = httpclient.WithOriginHeaders(http.DefaultClient, server.URL, server.Headers)
			if err != nil {
				return nil, err
			}
		}

		if server.Transport == "sse" {
			return &mcp.SSEClientTransport{
				Endpoint: server.URL,

				HTTPClient: httpClient,
			}, nil
		}

		return &mcp.StreamableClientTransport{
			Endpoint: server.URL,

			HTTPClient: httpClient,
		}, nil
	}

	return nil, fmt.Errorf("no command or url configured")
}
