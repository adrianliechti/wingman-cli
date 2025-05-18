package mcp

import (
	"context"
	"errors"
	"os"
	"os/exec"

	"mcp"
)

type Manager struct {
	clients map[string]*Client
}

func New(config *Config) (*Manager, error) {
	m := &Manager{
		clients: make(map[string]*Client),
	}

	for n, s := range config.Servers {
		switch s.Type {
		case "stdio":
			env := []string{}

			env = append(env, os.Environ()...)

			for k, v := range s.Env {
				env = append(env, k+"="+v)
			}

			cmd := exec.Command(s.Command, s.Args...)
			cmd.Env = env

			c := &Client{
				client:    mcp.NewClient("wingman", "1.0.0", &mcp.ClientOptions{}),
				transport: mcp.NewCommandTransport(cmd),
			}

			m.clients[n] = c

		case "sse":
			c := &Client{
				client:    mcp.NewClient("wingman", "1.0.0", &mcp.ClientOptions{}),
				transport: mcp.NewSSEClientTransport(s.URL),
			}

			m.clients[n] = c

		default:
			return nil, errors.New("invalid server type")
		}
	}

	return m, nil
}

type Client struct {
	client    *mcp.Client
	transport mcp.Transport
}

func (c *Client) connect(ctx context.Context) (*mcp.ClientSession, error) {
	return c.client.Connect(ctx, c.transport)
}
