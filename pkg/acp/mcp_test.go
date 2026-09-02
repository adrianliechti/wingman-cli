package acp

import (
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
)

func TestValidateMCPServers(t *testing.T) {
	stdio := func(name, command string, env ...acpsdk.EnvVariable) acpsdk.McpServer {
		return acpsdk.McpServer{Stdio: &acpsdk.McpServerStdio{Name: name, Command: command, Env: env}}
	}
	http := func(name, url string, headers ...acpsdk.HttpHeader) acpsdk.McpServer {
		return acpsdk.McpServer{Http: &acpsdk.McpServerHttpInline{Name: name, Url: url, Headers: headers}}
	}
	sse := func(name, url string, headers ...acpsdk.HttpHeader) acpsdk.McpServer {
		return acpsdk.McpServer{Sse: &acpsdk.McpServerSseInline{Name: name, Url: url, Headers: headers}}
	}
	proxied := func(name, id string) acpsdk.McpServer {
		return acpsdk.McpServer{Acp: &acpsdk.McpServerAcpInline{Name: name, Id: acpsdk.McpServerAcpId(id)}}
	}
	local := stdio("local", "server", acpsdk.EnvVariable{Name: "TOKEN", Value: "secret"})

	valid := []struct {
		name    string
		servers []acpsdk.McpServer
		caps    acpsdk.McpCapabilities
	}{
		{name: "empty"},
		{name: "stdio", servers: []acpsdk.McpServer{local}},
		{name: "http", servers: []acpsdk.McpServer{http("remote", "https://example.test", acpsdk.HttpHeader{Name: "Authorization", Value: "secret"})}, caps: acpsdk.McpCapabilities{Http: true}},
		{name: "sse", servers: []acpsdk.McpServer{sse("events", "https://example.test/sse")}, caps: acpsdk.McpCapabilities{Sse: true}},
		{name: "acp", servers: []acpsdk.McpServer{proxied("proxied", "mcp-1")}, caps: acpsdk.McpCapabilities{Acp: true}},
	}
	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateMCPServers(tc.servers, tc.caps); err != nil {
				t.Fatal(err)
			}
		})
	}

	invalid := []struct {
		name    string
		servers []acpsdk.McpServer
		caps    acpsdk.McpCapabilities
		want    string
	}{
		{name: "no transport", servers: []acpsdk.McpServer{{}}, want: "exactly one transport"},
		{name: "two transports", servers: []acpsdk.McpServer{{Stdio: local.Stdio, Http: &acpsdk.McpServerHttpInline{}}}, want: "exactly one transport"},
		{name: "blank name", servers: []acpsdk.McpServer{stdio(" ", "server")}, want: "has no name"},
		{name: "duplicate name", servers: []acpsdk.McpServer{local, local}, want: "duplicate MCP server name"},
		{name: "blank command", servers: []acpsdk.McpServer{stdio("local", " ")}, want: "stdio command is required"},
		{name: "blank env name", servers: []acpsdk.McpServer{stdio("local", "server", acpsdk.EnvVariable{Name: " "})}, want: "environment variable name is required"},
		{name: "duplicate env", servers: []acpsdk.McpServer{stdio("local", "server", acpsdk.EnvVariable{Name: "TOKEN"}, acpsdk.EnvVariable{Name: "TOKEN"})}, want: "duplicate environment variable"},
		{name: "http unsupported", servers: []acpsdk.McpServer{http("remote", "https://example.test")}, want: "HTTP transport is not supported"},
		{name: "blank http URL", servers: []acpsdk.McpServer{http("remote", " ")}, caps: acpsdk.McpCapabilities{Http: true}, want: "HTTP URL is required"},
		{name: "blank header", servers: []acpsdk.McpServer{http("remote", "https://example.test", acpsdk.HttpHeader{Name: " "})}, caps: acpsdk.McpCapabilities{Http: true}, want: "HTTP header name is required"},
		{name: "duplicate header case insensitive", servers: []acpsdk.McpServer{http("remote", "https://example.test", acpsdk.HttpHeader{Name: "Authorization"}, acpsdk.HttpHeader{Name: "authorization"})}, caps: acpsdk.McpCapabilities{Http: true}, want: "duplicate HTTP header"},
		{name: "sse unsupported", servers: []acpsdk.McpServer{sse("events", "https://example.test")}, want: "SSE transport is not supported"},
		{name: "blank sse URL", servers: []acpsdk.McpServer{sse("events", " ")}, caps: acpsdk.McpCapabilities{Sse: true}, want: "SSE URL is required"},
		{name: "acp unsupported", servers: []acpsdk.McpServer{proxied("proxied", "mcp-1")}, want: "ACP transport is not supported"},
		{name: "blank acp id", servers: []acpsdk.McpServer{proxied("proxied", "")}, caps: acpsdk.McpCapabilities{Acp: true}, want: "ACP id is required"},
		{
			name:    "duplicate acp id",
			servers: []acpsdk.McpServer{proxied("one", "same"), proxied("two", "same")},
			caps:    acpsdk.McpCapabilities{Acp: true},
			want:    "duplicate ACP MCP server id",
		},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateMCPServers(tc.servers, tc.caps)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestStableMCPServers(t *testing.T) {
	servers, err := StableMCPServers([]acpsdk.UnstableMcpServer{
		{Http: &acpsdk.UnstableMcpServerHttp{
			Name: "remote", Type: "http", Url: "https://example.test/mcp",
			Headers: []acpsdk.HttpHeader{{Name: "Authorization", Value: "secret"}},
		}},
	}, acpsdk.McpCapabilities{Http: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].Http == nil || servers[0].Http.Url != "https://example.test/mcp" || len(servers[0].Http.Headers) != 1 {
		t.Fatalf("converted servers = %#v", servers)
	}

	if _, err := StableMCPServers([]acpsdk.UnstableMcpServer{{
		Sse: &acpsdk.UnstableMcpServerSse{Name: "events", Url: "https://example.test/sse"},
	}}, acpsdk.McpCapabilities{}); err == nil {
		t.Fatal("unadvertised SSE transport was accepted")
	}
}
