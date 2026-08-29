package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/adrianliechti/wingman-agent/pkg/acp/internal/acptest"
)

func TestACPContract(t *testing.T) {
	acptest.Run(t, newContractAgent)
}

func newContractAgent(t *testing.T) acptest.Agent {
	t.Helper()
	agentIO, serverIO := net.Pipe()
	server := &contractAppServer{conn: serverIO}
	go server.run()
	t.Cleanup(func() {
		_ = serverIO.Close()
		_ = agentIO.Close()
	})

	rpc := newRPCClient(agentIO, agentIO)
	client := newCodexClient(rpc)
	rpc.start()
	return newAgent(client, "default", "")
}

type contractAppServer struct {
	conn net.Conn
	mu   sync.Mutex
}

func (s *contractAppServer) run() {
	scanner := bufio.NewScanner(s.conn)
	for scanner.Scan() {
		var msg rpcMessage
		if json.Unmarshal(scanner.Bytes(), &msg) != nil || msg.Method == "" || len(msg.ID) == 0 {
			continue
		}
		s.handle(msg)
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "codex-contract-server: scan error: %v\n", err)
	}
}

func (s *contractAppServer) handle(msg rpcMessage) {
	switch msg.Method {
	case "initialize", "thread/unsubscribe", "thread/archive", "thread/settings/update":
		s.respond(msg, map[string]any{})
	case "model/list":
		s.respond(msg, map[string]any{"data": []any{map[string]any{
			"id": "contract-model", "displayName": "Contract Model", "description": "ACP contract model",
			"supportedReasoningEfforts": []any{map[string]any{"reasoningEffort": "medium", "description": "Balanced"}},
			"isDefault":                 true,
		}}})
	case "thread/start":
		s.respond(msg, map[string]any{
			"thread": map[string]any{"id": uuid.NewString(), "cwd": "/contract"},
			"model":  "contract-model", "reasoningEffort": "medium",
		})
	case "thread/fork":
		s.respond(msg, map[string]any{
			"thread": map[string]any{"id": uuid.NewString(), "cwd": "/contract"},
			"model":  "contract-model", "reasoningEffort": "medium",
		})
	case "turn/start":
		var p struct {
			ThreadID string `json:"threadId"`
			Input    []struct {
				Text string `json:"text"`
			} `json:"input"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		turnID := uuid.NewString()
		s.respond(msg, map[string]any{"turn": map[string]any{"id": turnID, "status": "inProgress"}})
		s.notify("thread/tokenUsage/updated", usageParams(p.ThreadID))
		itemID := uuid.NewString()
		s.notify("item/started", map[string]any{"threadId": p.ThreadID, "item": map[string]any{"id": itemID, "type": "agentMessage", "phase": "final_answer", "text": ""}})
		s.notify("item/agentMessage/delta", map[string]any{"threadId": p.ThreadID, "itemId": itemID, "delta": ""})
		text := strings.Join(inputTexts(p.Input), " ")
		if strings.Contains(text, acptest.CancelPrompt) {
			s.notify("item/agentMessage/delta", map[string]any{"threadId": p.ThreadID, "itemId": itemID, "delta": acptest.CancelText})
			return
		}
		s.notify("item/agentMessage/delta", map[string]any{"threadId": p.ThreadID, "itemId": itemID, "delta": acptest.NormalText})
		toolID := uuid.NewString()
		s.notify("item/started", map[string]any{"threadId": p.ThreadID, "item": map[string]any{
			"id": toolID, "type": "commandExecution", "command": "pwd", "cwd": "/contract", "status": "inProgress",
		}})
		s.notify("item/commandExecution/outputDelta", map[string]any{"threadId": p.ThreadID, "itemId": toolID, "delta": "/contract\n"})
		s.notify("item/completed", map[string]any{"threadId": p.ThreadID, "item": map[string]any{
			"id": toolID, "type": "commandExecution", "command": "pwd", "cwd": "/contract", "status": "completed", "aggregatedOutput": "/contract\n",
		}})
		s.notify("item/completed", map[string]any{"threadId": p.ThreadID, "item": map[string]any{"id": itemID, "type": "agentMessage", "phase": "final_answer", "text": acptest.NormalText}})
		s.notify("turn/completed", map[string]any{"threadId": p.ThreadID, "turn": map[string]any{"id": turnID, "status": "completed"}})
	case "turn/interrupt":
		s.respond(msg, map[string]any{})
	case "thread/list":
		s.respond(msg, map[string]any{"data": []any{}})
	case "config/read":
		s.respond(msg, map[string]any{"config": map[string]any{"model_provider": "contract"}})
	case "thread/resume":
		s.respond(msg, map[string]any{"thread": map[string]any{"id": uuid.NewString(), "cwd": "/contract"}, "model": "contract-model", "reasoningEffort": "medium"})
	case "thread/read":
		s.respond(msg, map[string]any{"thread": map[string]any{"id": uuid.NewString(), "cwd": "/contract", "turns": []any{}}})
	default:
		s.respondError(msg, -32601, "method not found")
	}
}

func inputTexts(input []struct {
	Text string `json:"text"`
}) []string {
	out := make([]string, 0, len(input))
	for _, item := range input {
		out = append(out, item.Text)
	}
	return out
}

func usageParams(threadID string) map[string]any {
	return map[string]any{
		"threadId": threadID,
		"tokenUsage": map[string]any{
			"last": map[string]any{
				"totalTokens": 20, "inputTokens": 15, "cachedInputTokens": 5,
				"outputTokens": 4, "reasoningOutputTokens": 1,
			},
			"modelContextWindow": 1000,
		},
	}
}

func (s *contractAppServer) respond(req rpcMessage, result any) {
	b, _ := json.Marshal(result)
	s.send(rpcMessage{Jsonrpc: "2.0", ID: req.ID, Result: b})
}

func (s *contractAppServer) respondError(req rpcMessage, code int, message string) {
	s.send(rpcMessage{Jsonrpc: "2.0", ID: req.ID, Error: &rpcError{Code: code, Message: message}})
}

func (s *contractAppServer) notify(method string, params any) {
	b, _ := json.Marshal(params)
	s.send(rpcMessage{Jsonrpc: "2.0", Method: method, Params: b})
}

func (s *contractAppServer) send(msg rpcMessage) {
	b, _ := json.Marshal(msg)
	s.mu.Lock()
	_, _ = s.conn.Write(append(b, '\n'))
	s.mu.Unlock()
}
