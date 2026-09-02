package pi

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/acp/internal/acptest"
	"github.com/coder/acp-go-sdk"
)

const (
	contractSessionID = "00000000-0000-4000-8000-000000000003"
	contractForkID    = "00000000-0000-4000-8000-000000000005"
)

func TestACPContract(t *testing.T) {
	acptest.Run(t, newContractAgent)
}

func newContractAgent(t *testing.T) acptest.Agent {
	t.Helper()
	script, _, env := acptest.CommandHelper(t, "TestPiContractHelper", "PI_CONTRACT_HELPER")
	return New(Options{Path: script, Env: env})
}

func TestPiContractHelper(t *testing.T) {
	if os.Getenv("PI_CONTRACT_HELPER") != "1" {
		return
	}
	runPiContractHelper()
	os.Exit(0)
}

func runPiContractHelper() {
	scanner := bufio.NewScanner(os.Stdin)
	cloned := false
	for scanner.Scan() {
		var req struct {
			Type    string `json:"type"`
			ID      string `json:"id"`
			Message string `json:"message"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil || req.ID == "" {
			continue
		}
		switch req.Type {
		case "get_available_models":
			writePiResponse(req.ID, map[string]any{"models": []any{map[string]any{"provider": "contract", "id": "model", "name": "Contract Model"}}})
		case "get_state":
			sessionID := contractSessionID
			if cloned {
				sessionID = contractForkID
			}
			writePiResponse(req.ID, map[string]any{
				"sessionId":     sessionID,
				"thinkingLevel": "medium",
				"model":         map[string]any{"provider": "contract", "id": "model"},
			})
		case "get_available_thinking_levels":
			writePiResponse(req.ID, map[string]any{"levels": []string{"off", "medium", "max"}})
		case "set_model", "set_thinking_level", "abort":
			writePiResponse(req.ID, map[string]any{})
		case "clone":
			cloned = true
			writePiResponse(req.ID, map[string]any{"cancelled": false})
		case "prompt":
			writePiDelta("")
			if strings.Contains(req.Message, acptest.CancelPrompt) {
				writePiDelta(acptest.CancelText)
				continue
			}
			writePiDelta(acptest.NormalText)
			writePiContract(map[string]any{
				"type": "tool_execution_start", "toolCallId": "00000000-0000-4000-8000-000000000004",
				"toolName": "bash", "args": map[string]any{"command": "pwd"},
			})
			writePiContract(map[string]any{
				"type": "tool_execution_end", "toolCallId": "00000000-0000-4000-8000-000000000004",
				"result": "contract output", "isError": false,
			})
			writePiContract(map[string]any{"type": "agent_end"})
			writePiContract(map[string]any{"type": "agent_settled"})
			writePiResponse(req.ID, map[string]any{})
		case "get_messages":
			writePiResponse(req.ID, map[string]any{"messages": []any{}})
		default:
			writePiContract(map[string]any{"type": "response", "id": req.ID, "success": false, "error": "unsupported"})
		}
	}
}

func TestForkSessionUsesPiCloneWithoutReplacingSource(t *testing.T) {
	script, dir, env := acptest.CommandHelper(t, "TestPiContractHelper", "PI_CONTRACT_HELPER")
	sessionsDir := t.TempDir()
	source := filepath.Join(sessionsDir, "source.jsonl")
	data := `{"type":"session","id":"` + contractSessionID + `","cwd":"` + dir + `"}` + "\n" +
		`{"type":"message","id":"entry","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"hello"}}` + "\n"
	if err := os.WriteFile(source, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	agent := New(Options{Path: script, Env: env, SessionsDir: sessionsDir})
	defer agent.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := agent.UnstableForkSession(ctx, acp.UnstableForkSessionRequest{
		SessionId: contractSessionID,
		Cwd:       dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.SessionId != contractForkID {
		t.Fatalf("fork session id = %q, want %q", resp.SessionId, contractForkID)
	}
	if agent.lookup(contractSessionID) != nil {
		t.Fatal("fork unexpectedly created or replaced an in-memory source session")
	}
	if agent.lookup(contractForkID) == nil {
		t.Fatal("forked session was not registered")
	}
}

func TestLiveForkSessionWithInstalledPi(t *testing.T) {
	if os.Getenv("PI_RPC_LIVE") == "" {
		t.Skip("set PI_RPC_LIVE=1 to exercise clone with installed Pi")
	}
	piPath, err := exec.LookPath("pi")
	if err != nil {
		t.Skipf("pi not found: %v", err)
	}

	agentDir := t.TempDir()
	models := `{"providers":{"contract":{"baseUrl":"http://127.0.0.1:1/v1","api":"openai-completions","apiKey":"test","models":[{"id":"model","reasoning":false}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	sessionsDir := filepath.Join(agentDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(sessionsDir, "source.jsonl")
	header := map[string]any{
		"type": "session", "version": 3, "id": contractSessionID,
		"timestamp": "2026-01-01T00:00:00Z", "cwd": cwd,
	}
	entry := map[string]any{
		"type": "message", "id": "entry-1", "parentId": nil, "timestamp": "2026-01-01T00:00:01Z",
		"message": map[string]any{"role": "user", "content": "remember", "timestamp": 1767225601000},
	}
	assistant := map[string]any{
		"type": "message", "id": "entry-2", "parentId": "entry-1", "timestamp": "2026-01-01T00:00:02Z",
		"message": map[string]any{
			"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "saved"}},
			"provider": "contract", "model": "model", "api": "openai-completions",
			"usage":      map[string]any{"input": 1, "output": 1, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 2, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}},
			"stopReason": "stop", "timestamp": 1767225602000,
		},
	}
	headerJSON, _ := json.Marshal(header)
	entryJSON, _ := json.Marshal(entry)
	assistantJSON, _ := json.Marshal(assistant)
	data := append(append(append(headerJSON, '\n'), append(entryJSON, '\n')...), append(assistantJSON, '\n')...)
	if err := os.WriteFile(source, data, 0o600); err != nil {
		t.Fatal(err)
	}

	env := setEnvValue(os.Environ(), "PI_CODING_AGENT_DIR", agentDir)
	agent := New(Options{Path: piPath, Env: env, SessionsDir: sessionsDir})
	defer agent.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	serverPath, _, _ := acptest.CommandHelper(t, "TestMCPServerHelper", "ACP_LIVE_MCP_SERVER")
	resp, err := agent.UnstableForkSession(ctx, acp.UnstableForkSessionRequest{
		SessionId: contractSessionID,
		Cwd:       cwd,
		McpServers: []acp.UnstableMcpServer{{Stdio: &acp.McpServerStdio{
			Name: "fork-live", Command: serverPath, Args: []string{},
			Env: []acp.EnvVariable{{Name: "ACP_LIVE_MCP_SERVER", Value: "1"}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.SessionId == "" || resp.SessionId == contractSessionID {
		t.Fatalf("invalid cloned session id %q", resp.SessionId)
	}
	if _, ok := findSessionFile(sessionsDir, string(resp.SessionId)); !ok {
		t.Fatalf("Pi did not persist cloned session %q", resp.SessionId)
	}
}

func writePiDelta(text string) {
	writePiContract(map[string]any{
		"type":                  "message_update",
		"assistantMessageEvent": map[string]any{"type": "text_delta", "delta": text},
	})
}

func writePiResponse(id string, data any) {
	writePiContract(map[string]any{"type": "response", "id": id, "success": true, "data": data})
}

func writePiContract(value any) {
	b, _ := json.Marshal(value)
	_, _ = os.Stdout.Write(append(b, '\n'))
}
