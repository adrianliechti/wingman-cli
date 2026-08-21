package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adrianliechti/wingman-agent/pkg/browser"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	wingmcp "github.com/adrianliechti/wingman-agent/pkg/mcp"
)

func TestBrowserHandlersProjectMCPProvider(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "chrome", Version: "1"}, nil)

	type navigateInput struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	}
	sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "navigate_page"},
		func(_ context.Context, _ *sdkmcp.CallToolRequest, input navigateInput) (*sdkmcp.CallToolResult, any, error) {
			return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "opened " + input.URL}}}, nil, nil
		})
	sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "list_pages"},
		func(context.Context, *sdkmcp.CallToolRequest, struct{}) (*sdkmcp.CallToolResult, any, error) {
			return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "1: http://localhost:5173"}}}, nil, nil
		})
	sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "take_snapshot"},
		func(context.Context, *sdkmcp.CallToolRequest, struct{}) (*sdkmcp.CallToolResult, any, error) {
			return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "uid=1 button Save"}}}, nil, nil
		})
	sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "take_screenshot"},
		func(context.Context, *sdkmcp.CallToolRequest, struct{}) (*sdkmcp.CallToolResult, any, error) {
			return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.ImageContent{MIMEType: "image/png", Data: []byte("png")}}}, nil, nil
		})

	clientTransport, serverTransport := sdkmcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "wingman", Version: "1"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()

	manager := wingmcp.NewManager(&wingmcp.Config{Servers: map[string]wingmcp.ServerConfig{
		"custom_chrome": {Command: "chrome-devtools-mcp"},
	}})
	manager.AddSession("custom_chrome", clientSession)
	service := browser.NewService(manager, t.TempDir())
	app := &Server{workspace: &code.Workspace{Browser: service}}

	status := httptest.NewRecorder()
	app.handleBrowserStatus(status, httptest.NewRequest(http.MethodGet, "/api/browser/", nil))
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"connected":true`) {
		t.Fatalf("status = %d %s", status.Code, status.Body.String())
	}

	open := httptest.NewRecorder()
	app.handleBrowserOpen(open, httptest.NewRequest(http.MethodPost, "/api/browser/open", strings.NewReader(`{"provider":"chrome","url":"http://localhost:5173"}`)))
	if open.Code != http.StatusOK || !strings.Contains(open.Body.String(), "opened http://localhost:5173") {
		t.Fatalf("open = %d %s", open.Code, open.Body.String())
	}

	screenshot := httptest.NewRecorder()
	app.handleBrowserScreenshot(screenshot, httptest.NewRequest(http.MethodGet, "/api/browser/screenshot?provider=chrome", nil))
	if screenshot.Code != http.StatusOK || screenshot.Header().Get("Content-Type") != "image/png" || !bytes.Equal(screenshot.Body.Bytes(), []byte("png")) {
		t.Fatalf("screenshot = %d %q %q", screenshot.Code, screenshot.Header().Get("Content-Type"), screenshot.Body.Bytes())
	}
}
