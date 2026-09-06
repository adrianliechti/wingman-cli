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

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"

	"github.com/adrianliechti/wingman-agent/internal/process"
	"github.com/adrianliechti/wingman-agent/pkg/httpclient"
	"github.com/adrianliechti/wingman-agent/pkg/telemetry"
)

type Manager struct {
	*Config

	Dir string

	// Credentials holds OAuth state for remote servers.
	Credentials *CredentialStore

	elicit atomic.Pointer[ElicitFunc]

	toolListChanged atomic.Pointer[ToolListChangedFunc]
	telemetry       atomic.Pointer[telemetry.Telemetry]

	mu                  sync.RWMutex
	sessions            map[string]*mcp.ClientSession
	sessionObservations map[*mcp.ClientSession]*telemetry.MCPSession
}

type ToolListChangedFunc func(serverName string)

func NewManager(cfg *Config) *Manager {
	return &Manager{
		Config: cfg,

		Credentials: DefaultCredentialStore(),

		sessions:            make(map[string]*mcp.ClientSession),
		sessionObservations: make(map[*mcp.ClientSession]*telemetry.MCPSession),
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
	m.addSession(context.Background(), name, session, m.Servers[name])
}

func (m *Manager) addSession(ctx context.Context, name string, session *mcp.ClientSession, server ServerConfig) {
	if session == nil {
		return
	}
	var observation *telemetry.MCPSession
	if tel := m.telemetry.Load(); tel != nil {
		observation = tel.StartMCPSession(ctx, mcpSessionRequest(session, server))
	}
	m.mu.Lock()
	m.sessions[name] = session
	if observation != nil {
		m.sessionObservations[session] = observation
	}
	m.mu.Unlock()

	if observation != nil {
		go func() {
			observation.End(telemetry.Outcome{Err: session.Wait()})
		}()
	}
}

// SetTelemetry instruments clients created by this manager. It should normally
// be called before Connect so initialization is covered as well.
func (m *Manager) SetTelemetry(tel *telemetry.Telemetry) {
	m.telemetry.Store(tel)
}

func (m *Manager) SetToolListChangedHandler(handler ToolListChangedFunc) {
	if handler == nil {
		m.toolListChanged.Store(nil)
		return
	}
	m.toolListChanged.Store(&handler)
}

func (m *Manager) Close() {
	m.mu.RLock()
	sessions := make([]*mcp.ClientSession, 0, len(m.sessions))
	observations := make([]*telemetry.MCPSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
		observations = append(observations, m.sessionObservations[session])
	}
	m.mu.RUnlock()

	for i, session := range sessions {
		if observations[i] != nil {
			observations[i].End(telemetry.Outcome{})
		}
		_ = session.Close()
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
	client := m.newClient(name, server)

	handler, err := m.oauthHandler(name, server)

	if err != nil {
		return fmt.Errorf("MCP server %s: %w", name, err)
	}

	transport, err := createTransport(server, m.Dir, handler)

	if err != nil {
		return fmt.Errorf("MCP server %s: %w", name, err)
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	session, err := client.Connect(ctx, transport, nil)

	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}

	m.addSession(ctx, name, session, server)

	return nil
}

func (m *Manager) newClient(name string, servers ...ServerConfig) *mcp.Client {
	client := mcp.NewClient(&mcp.Implementation{
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
	if tel := m.telemetry.Load(); tel != nil {
		var server ServerConfig
		if len(servers) > 0 {
			server = servers[0]
		}
		client.AddSendingMiddleware(mcpClientTelemetryMiddleware(tel, mcpTransport(server)))
		client.AddReceivingMiddleware(mcpServerTelemetryMiddleware(tel, mcpTransport(server)))
	}
	return client
}

// oauthHandler equips remote servers with stored credentials. Without a
// usable token the flow fails and points the user at 'wingman mcp login'.
func (m *Manager) oauthHandler(name string, server ServerConfig) (auth.OAuthHandler, error) {
	if server.URL == "" || hasAuthorizationHeader(server.Headers) {
		return nil, nil
	}

	store := m.Credentials

	if store == nil {
		store = DefaultCredentialStore()
	}

	return newOAuthHandler(oauthOptions{
		store:  store,
		server: server,
		fetcher: func(context.Context, *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			return nil, fmt.Errorf("%w: run 'wingman mcp login %s'", ErrLoginRequired, name)
		},
	})
}

func createTransport(server ServerConfig, dir string, handler auth.OAuthHandler) (mcp.Transport, error) {
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
			// The SSE transport has no OAuth hook, so stored tokens ride on the client.
			if handler != nil {
				if ts, err := handler.TokenSource(context.Background()); err == nil && ts != nil {
					client := *httpClient
					client.Transport = &oauth2.Transport{Source: ts, Base: httpClient.Transport}
					httpClient = &client
				}
			}

			return &mcp.SSEClientTransport{
				Endpoint: server.URL,

				HTTPClient: httpClient,
			}, nil
		}

		return &mcp.StreamableClientTransport{
			Endpoint: server.URL,

			HTTPClient: httpClient,

			OAuthHandler: handler,
		}, nil
	}

	return nil, fmt.Errorf("no command or url configured")
}
