package acp

import (
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
)

func TestValidateMCPServers(t *testing.T) {
	stdio := acpsdk.McpServer{Stdio: &acpsdk.McpServerStdio{Name: "local", Command: "server"}}
	if err := ValidateMCPServers([]acpsdk.McpServer{stdio}, acpsdk.McpCapabilities{}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMCPServers([]acpsdk.McpServer{{Http: &acpsdk.McpServerHttpInline{Name: "remote", Url: "https://example.test"}}}, acpsdk.McpCapabilities{}); err == nil {
		t.Fatal("unadvertised HTTP transport was accepted")
	}
	if err := ValidateMCPServers([]acpsdk.McpServer{stdio, stdio}, acpsdk.McpCapabilities{}); err == nil {
		t.Fatal("duplicate server name was accepted")
	}
	if err := ValidateMCPServers([]acpsdk.McpServer{{Stdio: &acpsdk.McpServerStdio{Name: "missing"}}}, acpsdk.McpCapabilities{}); err == nil {
		t.Fatal("empty stdio command was accepted")
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
