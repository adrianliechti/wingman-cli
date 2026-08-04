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
