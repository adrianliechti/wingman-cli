package browser

import (
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/mcp"
)

func TestConfiguredProvidersRecognizesOfficialServers(t *testing.T) {
	configured := configuredProviders(map[string]mcp.ServerConfig{
		"custom-chrome": {
			Command: "npx",
			Args:    []string{"-y", "chrome-devtools-mcp@latest", "--isolated"},
		},
		"custom-safari": {
			Command: "/usr/bin/safaridriver",
			Args:    []string{"--mcp"},
		},
	})
	if configured[ProviderChrome] != "custom-chrome" {
		t.Fatalf("Chrome server = %q", configured[ProviderChrome])
	}
	if configured[ProviderSafari] != "custom-safari" {
		t.Fatalf("Safari server = %q", configured[ProviderSafari])
	}
}

func TestCaptureDataURL(t *testing.T) {
	capture := Capture{MIMEType: "image/png", Data: []byte("png")}
	if got, want := capture.DataURL(), "data:image/png;base64,cG5n"; got != want {
		t.Fatalf("DataURL = %q, want %q", got, want)
	}
}
