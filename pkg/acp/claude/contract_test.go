package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/adrianliechti/wingman-agent/pkg/acp/internal/acptest"
)

func TestACPContract(t *testing.T) {
	acptest.Run(t, newContractAgent)
}

func newContractAgent(t *testing.T) acptest.Agent {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	script, dir, env := acptest.CommandHelper(t, "TestClaudeContractHelper", "CLAUDE_CONTRACT_HELPER")
	return New(Options{Path: script, Cwd: dir, Env: env})
}

func TestClaudeContractHelper(t *testing.T) {
	if os.Getenv("CLAUDE_CONTRACT_HELPER") != "1" {
		return
	}
	runClaudeContractHelper()
	os.Exit(0)
}

func runClaudeContractHelper() {
	ensureClaudeContractHistory()
	scanner := bufio.NewScanner(os.Stdin)
	pendingCancel := false
	for scanner.Scan() {
		line := scanner.Bytes()
		var env struct {
			Type      string `json:"type"`
			RequestID string `json:"request_id"`
			Request   struct {
				Subtype string `json:"subtype"`
			} `json:"request"`
			Message struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &env) != nil {
			continue
		}
		switch {
		case env.Type == "control_request" && env.Request.Subtype == "initialize":
			writeClaudeContract(map[string]any{
				"type": "control_response",
				"response": map[string]any{
					"subtype": "success", "request_id": env.RequestID,
					"response": map[string]any{
						"models": []any{map[string]any{
							"value": "default", "displayName": "Default", "description": "ACP contract model",
							"resolvedModel": "claude-contract", "supportsEffort": true,
							"supportedEffortLevels": []string{"medium"},
						}},
						"commands": []any{},
					},
				},
			})
		case env.Type == "user":
			var text strings.Builder
			for _, block := range env.Message.Content {
				if block.Type == "text" {
					text.WriteString(block.Text)
				}
			}
			writeClaudeDelta("")
			if strings.Contains(text.String(), acptest.CancelPrompt) {
				pendingCancel = true
				writeClaudeDelta(acptest.CancelText)
				continue
			}
			writeClaudeDelta(acptest.NormalText)
			writeClaudeToolFlow()
			writeClaudeResult()
		case env.Type == "control_request" && env.Request.Subtype == "interrupt" && pendingCancel:
			pendingCancel = false
			writeClaudeResult()
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "claude-contract-helper: scan error: %v\n", err)
	}
}

func ensureClaudeContractHistory() {
	var id string
	for i, arg := range os.Args {
		if arg == "--session-id" && i+1 < len(os.Args) {
			id = os.Args[i+1]
		}
	}
	if id == "" {
		return
	}
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	dir := projectDirFor(cwd)
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	history := map[string]any{
		"type": "user", "cwd": cwd,
		"message": map[string]any{"role": "user", "content": acptest.NormalPrompt},
	}
	b, _ := json.Marshal(history)
	_ = os.WriteFile(filepath.Join(dir, id+".jsonl"), append(b, '\n'), 0o600)
}

func writeClaudeToolFlow() {
	id := uuid.NewString()
	writeClaudeContract(map[string]any{
		"type": "assistant",
		"message": map[string]any{"content": []any{map[string]any{
			"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{"command": "pwd"},
		}}},
	})
	writeClaudeContract(map[string]any{
		"type": "user",
		"message": map[string]any{"content": []any{map[string]any{
			"type": "tool_result", "tool_use_id": id, "content": "contract output",
		}}},
	})
}

func writeClaudeDelta(text string) {
	writeClaudeContract(map[string]any{
		"type": "stream_event",
		"event": map[string]any{
			"type":  "content_block_delta",
			"delta": map[string]any{"type": "text_delta", "text": text},
		},
	})
}

func writeClaudeResult() {
	writeClaudeContract(map[string]any{
		"type": "result", "subtype": "success", "result": "done",
		"usage": map[string]any{
			"input_tokens": 10, "output_tokens": 4,
			"cache_read_input_tokens": 3, "cache_creation_input_tokens": 2,
		},
		"modelUsage": map[string]any{"claude-contract": map[string]any{"contextWindow": 1000}},
	})
}

func writeClaudeContract(value any) {
	b, _ := json.Marshal(value)
	_, _ = os.Stdout.Write(append(b, '\n'))
}
