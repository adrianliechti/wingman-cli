package codex

import (
	"context"
	"os"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/acp/internal/acptest"
)

func TestLiveSuite(t *testing.T) {
	acptest.RunLive(t, acptest.LiveConfig{
		Gate:   "CODEX_ACP_LIVE",
		Binary: "codex",
		Factory: func(t *testing.T, _ string) acptest.Agent {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			agent, err := Spawn(ctx, Options{Env: os.Environ()})
			if err != nil {
				t.Fatalf("spawn codex: %v", err)
			}
			return agent
		},
		Subagents: acptest.LiveSubagents{
			Prompt:        "Use your multi-agent collaboration tools: spawn exactly one subagent with the prompt \"Reply with the word hi\". Wait for it to finish, close it, then reply with exactly: DONE",
			Marker:        "DONE",
			WantToolTitle: "subagent",
		},
		PlanPrompt:      "Use your plan tool to create a plan with exactly two steps: 'first' and 'second'. Mark both completed, then reply with exactly: DONE",
		MCPHelperTest:   "TestMCPServerHelper",
		WantUsageUpdate: true,
	})
}

func TestMCPServerHelper(t *testing.T) {
	acptest.MCPServerHelper(t)
}
