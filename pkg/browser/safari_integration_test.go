package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/mcp"
)

// This is opt-in because it starts the OS-provided Safari MCP server and may
// show Safari's one-time remote-automation permission prompt.
func TestSafariMCPIntegration(t *testing.T) {
	if os.Getenv("WINGMAN_SAFARI_MCP_E2E") != "1" {
		t.Skip("set WINGMAN_SAFARI_MCP_E2E=1 to exercise the installed Safari MCP server")
	}
	if provider := discoverSafari(); !provider.available {
		t.Fatalf("Safari MCP unavailable: %s", provider.setup)
	}

	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><title>Wingman Safari MCP</title><main><h1>Browser evidence is live</h1><button>Verify</button></main>`))
	}))
	defer page.Close()

	manager := mcp.NewManager(&mcp.Config{Servers: map[string]mcp.ServerConfig{}})
	defer manager.Close()
	service := NewService(manager, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if err := service.Enable(ctx, ProviderSafari); err != nil {
		t.Fatalf("connect Safari MCP: %v", err)
	}
	if _, err := service.Open(ctx, ProviderSafari, page.URL); err != nil {
		t.Fatalf("navigate Safari: %v", err)
	}
	info, err := service.PageInfo(ctx, ProviderSafari)
	if err != nil {
		t.Fatalf("read Safari page info: %v", err)
	}
	if !strings.Contains(info, "Wingman Safari MCP") {
		t.Fatalf("page info did not describe the test page: %q", info)
	}
	content, err := service.Snapshot(ctx, ProviderSafari)
	if err != nil {
		t.Fatalf("read Safari page content: %v", err)
	}
	if !strings.Contains(content, "Browser evidence is live") {
		t.Fatalf("page content did not include the test page: %q", content)
	}
	capture, err := service.Screenshot(ctx, ProviderSafari, "")
	if err != nil {
		t.Fatalf("capture Safari screenshot: %v", err)
	}
	if capture.MIMEType != "image/png" || len(capture.Data) == 0 {
		t.Fatalf("screenshot = %q, %d bytes", capture.MIMEType, len(capture.Data))
	}
}
