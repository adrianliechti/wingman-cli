package pi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/acp/internal/acptest"
)

func TestLiveSuite(t *testing.T) {
	acptest.RunLive(t, acptest.LiveConfig{
		Gate:          "PI_ACP_LIVE",
		Binary:        "pi",
		MCPHelperTest: "TestMCPServerHelper",
		Factory: func(t *testing.T, path string) acptest.Agent {
			opts := Options{Path: path, Env: os.Environ()}
			// Standalone pi (no wingman env redirection) persists sessions
			// under its native agent directory.
			if home, err := os.UserHomeDir(); err == nil {
				opts.SessionsDir = filepath.Join(home, ".pi", "agent", "sessions")
			}
			return New(opts)
		},
	})
}

func TestMCPServerHelper(t *testing.T) {
	acptest.MCPServerHelper(t)
}
