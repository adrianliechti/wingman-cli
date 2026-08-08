// Package httpclient contains narrowly scoped HTTP client policies shared by
// plugin-provided MCP servers and lifecycle hooks.
package httpclient

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// WithOriginHeaders returns a shallow copy of base that adds headers only to
// requests targeting the configured endpoint's origin. Existing request
// headers always take precedence. Requests are cloned before modification so
// redirect handling cannot copy injected headers to another origin.
func WithOriginHeaders(base *http.Client, endpoint string, headers map[string]string) (*http.Client, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid HTTP endpoint %q", endpoint)
	}
	if base == nil {
		base = http.DefaultClient
	}
	client := *base
	transport := base.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = &originHeaderTransport{
		base:    transport,
		origin:  origin(parsed),
		headers: headers,
	}
	return &client, nil
}

type originHeaderTransport struct {
	base    http.RoundTripper
	origin  string
	headers map[string]string
}

func (t *originHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if origin(req.URL) != t.origin || len(t.headers) == 0 {
		return t.base.RoundTrip(req)
	}

	request := req.Clone(req.Context())
	request.Header = req.Header.Clone()
	for name, value := range t.headers {
		if len(request.Header.Values(name)) == 0 {
			request.Header.Set(name, value)
		}
	}
	return t.base.RoundTrip(request)
}

func origin(parsed *url.URL) string {
	scheme := strings.ToLower(parsed.Scheme)
	port := parsed.Port()
	if port == "" {
		switch scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	}
	return scheme + "://" + strings.ToLower(parsed.Hostname()) + ":" + port
}
