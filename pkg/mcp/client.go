package mcp

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Client struct {
	transports map[string]func() (mcp.Transport, error)
}

func New(config *Config) (*Client, error) {
	c := &Client{
		transports: make(map[string]func() (mcp.Transport, error)),
	}

	for n, s := range config.Servers {
		switch s.Type {
		case "stdio", "command":
			env := os.Environ()

			for k, v := range s.Env {
				env = append(env, k+"="+v)
			}

			c.transports[n] = func() (mcp.Transport, error) {
				cmd := exec.Command(s.Command, s.Args...)
				cmd.Env = env

				return mcp.NewCommandTransport(cmd), nil
			}

		case "http":
			var client *http.Client

			if len(s.Headers) > 0 {
				client = &http.Client{
					Transport: &rt{
						headers: s.Headers,
					},
				}
			}

			c.transports[n] = func() (mcp.Transport, error) {
				transport := mcp.NewStreamableClientTransport(s.URL, &mcp.StreamableClientTransportOptions{
					HTTPClient: client,
				})

				return transport, nil
			}

		case "sse":
			var client *http.Client

			if len(s.Headers) > 0 {
				client = &http.Client{
					Transport: &rt{
						headers: s.Headers,
					},
				}
			}

			c.transports[n] = func() (mcp.Transport, error) {
				transport := mcp.NewSSEClientTransport(s.URL, &mcp.SSEClientTransportOptions{
					HTTPClient: client,
				})

				return transport, nil
			}

		default:
			return nil, errors.New("invalid server type")
		}
	}

	return c, nil
}

func (c *Client) createSession(ctx context.Context, server string) (*mcp.ClientSession, error) {
	transportFn, ok := c.transports[server]

	if !ok {
		return nil, errors.New("unknown server: " + server)
	}

	transport, err := transportFn()

	if err != nil {
		return nil, err
	}

	impl := &mcp.Implementation{
		Name:    "wingman",
		Version: "1.0.0",
	}

	opts := &mcp.ClientOptions{
		KeepAlive: time.Second * 30,
	}

	client := mcp.NewClient(impl, opts)

	return client.Connect(ctx, transport, nil)
}

type rt struct {
	headers   map[string]string
	transport http.RoundTripper
}

func (rt *rt) RoundTrip(req *http.Request) (*http.Response, error) {
	for key, value := range rt.headers {
		if req.Header.Get(key) != "" {
			continue // already set
		}

		req.Header.Set(key, value)
	}

	tr := rt.transport

	if tr == nil {
		tr = http.DefaultTransport
	}

	return tr.RoundTrip(req)
}
