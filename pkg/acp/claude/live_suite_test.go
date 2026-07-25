package claude

import (
	"os"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/acp/internal/acptest"
)

func TestLiveSuite(t *testing.T) {
	acptest.RunLive(t, acptest.LiveConfig{
		Gate:   "CLAUDE_ACP_LIVE",
		Binary: "claude",
		Factory: func(t *testing.T, path string) acptest.Agent {
			return New(Options{Env: os.Environ(), Path: path})
		},
		Subagents: acptest.LiveSubagents{
			Prompt: "Launch a Task subagent (general-purpose) with this prompt: \"State the word WATERMELON and write two sentences about melons.\" When it finishes, do not repeat or summarize anything it said. Reply with exactly: FINISHED",
			Marker: "FINISHED",
			Leak:   "WATERMELON",
		},
		PlanPrompt:      "Use your task tracking tools to create two tasks named 'first' and 'second'. Mark both completed, then reply with exactly: DONE",
		MCPHelperTest:   "TestMCPServerHelper",
		WantUsageUpdate: true,
		WantCommands:    true,
	})
}

func TestMCPServerHelper(t *testing.T) {
	acptest.MCPServerHelper(t)
}
