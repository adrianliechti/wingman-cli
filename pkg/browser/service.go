// Package browser integrates maintained browser MCP servers with a Wingman
// workspace. It deliberately does not implement browser automation itself:
// Chrome DevTools MCP and Safari's safaridriver own browser semantics, while
// this package owns discovery, lifecycle, and a small UI-facing projection.
package browser

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adrianliechti/wingman-agent/pkg/mcp"
)

const (
	ProviderChrome = "chrome"
	ProviderSafari = "safari"

	chromeServerName = "browser_chrome"
	safariServerName = "browser_safari"
)

type Provider struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Available        bool   `json:"available"`
	Connected        bool   `json:"connected"`
	Configured       bool   `json:"configured"`
	RequiresDownload bool   `json:"requires_download,omitempty"`
	Description      string `json:"description"`
	Setup            string `json:"setup,omitempty"`
	Server           string `json:"server,omitempty"`
}

type Capture struct {
	MIMEType string `json:"mime_type"`
	Data     []byte `json:"-"`
	Text     string `json:"text,omitempty"`
}

func (c Capture) DataURL() string {
	if len(c.Data) == 0 || c.MIMEType == "" {
		return ""
	}
	return "data:" + c.MIMEType + ";base64," + base64.StdEncoding.EncodeToString(c.Data)
}

type Service struct {
	manager *mcp.Manager
	workDir string

	mu        sync.Mutex
	discovery map[string]discoveredProvider
	selected  string
	onChange  func()
}

type discoveredProvider struct {
	available        bool
	command          string
	args             []string
	requiresDownload bool
	setup            string
}

func NewService(manager *mcp.Manager, workDir string) *Service {
	return &Service{
		manager: manager,
		workDir: workDir,
		discovery: map[string]discoveredProvider{
			ProviderChrome: discoverChrome(),
			ProviderSafari: discoverSafari(),
		},
	}
}

func (s *Service) SetChangeHandler(handler func()) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.onChange = handler
	s.mu.Unlock()
}

func (s *Service) Providers() []Provider {
	if s == nil || s.manager == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	configured := configuredProviders(s.manager.ServerConfigs())
	providers := []Provider{
		s.providerLocked(ProviderChrome, "Chrome DevTools", "Puppeteer automation, accessibility snapshots, console, network, performance, and screenshots.", configured),
		s.providerLocked(ProviderSafari, "Safari", "Native Safari DOM, console, network, compatibility, accessibility, and screenshot inspection.", configured),
	}
	return providers
}

func (s *Service) providerLocked(id, name, description string, configured map[string]string) Provider {
	discovered := s.discovery[id]
	server := configured[id]
	available := discovered.available || server != ""
	connected := server != "" && s.manager.Session(server) != nil
	if connected && s.selected == "" {
		s.selected = id
	}
	return Provider{
		ID: id, Name: name, Description: description,
		Available: available, Connected: connected, Configured: server != "",
		RequiresDownload: discovered.requiresDownload && server == "",
		Setup:            discovered.setup, Server: server,
	}
}

func (s *Service) Selected() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.selected != "" {
		return s.selected
	}
	configured := configuredProviders(s.manager.ServerConfigs())
	for _, id := range []string{ProviderChrome, ProviderSafari} {
		if server := configured[id]; server != "" && s.manager.Session(server) != nil {
			s.selected = id
			return id
		}
	}
	return ""
}

func (s *Service) Select(id string) error {
	if id != ProviderChrome && id != ProviderSafari {
		return fmt.Errorf("unknown browser provider %q", id)
	}
	provider, ok := s.provider(id)
	if !ok || !provider.Connected {
		return fmt.Errorf("browser provider %q is not connected", id)
	}
	s.mu.Lock()
	s.selected = id
	handler := s.onChange
	s.mu.Unlock()
	if handler != nil {
		handler()
	}
	return nil
}

func (s *Service) Enable(ctx context.Context, id string) error {
	if s == nil || s.manager == nil {
		return errors.New("browser integrations are unavailable")
	}
	if id != ProviderChrome && id != ProviderSafari {
		return fmt.Errorf("unknown browser provider %q", id)
	}
	if provider, ok := s.provider(id); ok && provider.Connected {
		return s.Select(id)
	}
	// A user-configured provider may connect to a remote/existing browser and
	// therefore must not depend on Wingman's local Chrome/Safari discovery.
	// Retry its own config after an earlier startup failure instead of replacing
	// it with the built-in definition.
	servers := s.manager.ServerConfigs()
	if configuredName := configuredProviders(servers)[id]; configuredName != "" {
		config := servers[configuredName]
		if err := s.manager.AddServer(ctx, configuredName, config); err != nil {
			return err
		}
		s.mu.Lock()
		s.selected = id
		handler := s.onChange
		s.mu.Unlock()
		if handler != nil {
			handler()
		}
		return nil
	}

	s.mu.Lock()
	discovered := s.discovery[id]
	s.mu.Unlock()
	if !discovered.available {
		if discovered.setup != "" {
			return errors.New(discovered.setup)
		}
		return fmt.Errorf("%s browser integration is unavailable", id)
	}
	name := chromeServerName
	if id == ProviderSafari {
		name = safariServerName
	}
	config := mcp.ServerConfig{
		Command: discovered.command,
		Args:    append([]string(nil), discovered.args...),
		Dir:     s.workDir,
	}
	if id == ProviderChrome {
		config.Env = map[string]string{
			"CHROME_DEVTOOLS_MCP_NO_USAGE_STATISTICS": "1",
			"CHROME_DEVTOOLS_MCP_NO_UPDATE_CHECKS":    "1",
		}
	}
	if err := s.manager.AddServer(ctx, name, config); err != nil {
		return err
	}
	s.mu.Lock()
	s.selected = id
	handler := s.onChange
	s.mu.Unlock()
	if handler != nil {
		handler()
	}
	return nil
}

func (s *Service) Open(ctx context.Context, providerID, rawURL string) (string, error) {
	providerID, session, err := s.session(providerID)
	if err != nil {
		return "", err
	}
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", errors.New("URL is required")
	}
	var toolName string
	var args map[string]any
	switch providerID {
	case ProviderChrome:
		toolName = "navigate_page"
		args = map[string]any{"type": "url", "url": rawURL}
	case ProviderSafari:
		toolName = "navigate_to_url"
		args = map[string]any{"url": rawURL}
	}
	result, err := callTool(ctx, session, toolName, args)
	if err == nil {
		s.changed()
	}
	return resultText(result), err
}

func (s *Service) Snapshot(ctx context.Context, providerID string) (string, error) {
	providerID, session, err := s.session(providerID)
	if err != nil {
		return "", err
	}
	toolName := "take_snapshot"
	if providerID == ProviderSafari {
		toolName = "get_page_content"
	}
	result, err := callTool(ctx, session, toolName, map[string]any{})
	if err != nil {
		return "", err
	}
	return resultText(result), nil
}

func (s *Service) Screenshot(ctx context.Context, providerID, elementUID string) (Capture, error) {
	providerID, session, err := s.session(providerID)
	if err != nil {
		return Capture{}, err
	}
	toolName := "take_screenshot"
	args := map[string]any{}
	if providerID == ProviderSafari {
		if elementUID != "" {
			return Capture{}, errors.New("element screenshots are currently available with Chrome DevTools MCP")
		}
		toolName = "screenshot"
	} else if elementUID != "" {
		args["uid"] = elementUID
	}
	result, err := callTool(ctx, session, toolName, args)
	if err != nil {
		return Capture{}, err
	}
	capture := captureResult(result)
	if len(capture.Data) == 0 && providerID == ProviderSafari {
		data, loadErr := loadSafariScreenshot(capture.Text)
		if loadErr != nil {
			return Capture{}, loadErr
		}
		capture.MIMEType = "image/png"
		capture.Data = data
	}
	if len(capture.Data) == 0 {
		return Capture{}, fmt.Errorf("%s did not return an attached screenshot%s", providerID, textSuffix(capture.Text))
	}
	return capture, nil
}

func (s *Service) PageInfo(ctx context.Context, providerID string) (string, error) {
	providerID, session, err := s.session(providerID)
	if err != nil {
		return "", err
	}
	toolName := "list_pages"
	if providerID == ProviderSafari {
		toolName = "page_info"
	}
	result, err := callTool(ctx, session, toolName, map[string]any{})
	if err != nil {
		return "", err
	}
	return resultText(result), nil
}

func (s *Service) session(providerID string) (string, *sdkmcp.ClientSession, error) {
	if s == nil || s.manager == nil {
		return "", nil, errors.New("browser integrations are unavailable")
	}
	if providerID == "" {
		providerID = s.Selected()
	}
	provider, ok := s.provider(providerID)
	if !ok || !provider.Connected || provider.Server == "" {
		return "", nil, fmt.Errorf("%s browser provider is not connected", providerID)
	}
	session := s.manager.Session(provider.Server)
	if session == nil {
		return "", nil, fmt.Errorf("%s browser provider disconnected", providerID)
	}
	return providerID, session, nil
}

func (s *Service) provider(id string) (Provider, bool) {
	for _, provider := range s.Providers() {
		if provider.ID == id {
			return provider, true
		}
	}
	return Provider{}, false
}

func (s *Service) changed() {
	s.mu.Lock()
	handler := s.onChange
	s.mu.Unlock()
	if handler != nil {
		handler()
	}
}

func callTool(ctx context.Context, session *sdkmcp.ClientSession, name string, args map[string]any) (*sdkmcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, fmt.Errorf("browser tool %s failed: %w", name, err)
	}
	if result.IsError {
		message := resultText(result)
		if message == "" {
			message = "no error details"
		}
		return nil, fmt.Errorf("browser tool %s: %s", name, message)
	}
	return result, nil
}

func captureResult(result *sdkmcp.CallToolResult) Capture {
	var capture Capture
	if result == nil {
		return capture
	}
	for _, content := range result.Content {
		switch value := content.(type) {
		case *sdkmcp.ImageContent:
			if len(capture.Data) == 0 {
				capture.MIMEType = value.MIMEType
				capture.Data = append([]byte(nil), value.Data...)
			}
		case *sdkmcp.TextContent:
			if value.Text != "" {
				if capture.Text != "" {
					capture.Text += "\n"
				}
				capture.Text += value.Text
			}
		}
	}
	return capture
}

func resultText(result *sdkmcp.CallToolResult) string {
	return captureResult(result).Text
}

func textSuffix(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) > 240 {
		value = value[:240] + "…"
	}
	return ": " + value
}

func configuredProviders(servers map[string]mcp.ServerConfig) map[string]string {
	configured := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(servers)) {
		server := servers[name]
		joined := strings.ToLower(server.Command + "\x00" + strings.Join(server.Args, "\x00"))
		if configured[ProviderChrome] == "" && strings.Contains(joined, "chrome-devtools-mcp") {
			configured[ProviderChrome] = name
		}
		if configured[ProviderSafari] == "" && strings.Contains(joined, "safaridriver") && slices.Contains(server.Args, "--mcp") {
			configured[ProviderSafari] = name
		}
	}
	return configured
}
