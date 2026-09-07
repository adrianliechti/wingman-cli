package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
)

func TestLoadSessionReadsCompleteHistoryOrReportsFailure(t *testing.T) {
	for _, mode := range []string{"paginated", "legacy", "older-backend", "read-error", "repeated-cursor", "page-error"} {
		t.Run(mode, func(t *testing.T) {
			appIO, backendIO := net.Pipe()
			reader, input := io.Pipe()
			t.Cleanup(func() {
				_ = appIO.Close()
				_ = backendIO.Close()
				_ = reader.Close()
				_ = input.Close()
			})
			var output strings.Builder
			rpc := newRPCClient(appIO, appIO)
			agent := newAgent(newCodexClient(rpc), "default", "")
			agent.SetAgentConnection(acp.NewAgentSideConnection(agent, &output, reader))
			rpc.start()
			go func() {
				scanner := bufio.NewScanner(backendIO)
				for scanner.Scan() {
					var request rpcMessage
					_ = json.Unmarshal(scanner.Bytes(), &request)
					response := rpcMessage{ID: request.ID, Result: json.RawMessage(`{}`)}
					switch request.Method {
					case "config/read":
						response.Result = json.RawMessage(`{"config":{}}`)
					case "thread/resume":
						response.Result = json.RawMessage(`{"thread":{"id":"history","turns":[]},"model":"default"}`)
					case "thread/read":
						var params threadReadParams
						_ = json.Unmarshal(request.Params, &params)
						if mode == "read-error" {
							response.Error = &rpcError{Code: -32000, Message: "history unavailable"}
						} else if params.IncludeTurns {
							if mode != "legacy" && mode != "older-backend" {
								response.Error = &rpcError{Code: -32602, Message: "full-history hydration is deprecated"}
							} else {
								response.Result = json.RawMessage(`{"thread":{"id":"history","turns":[{"id":"old","items":[{"id":"a","type":"agentMessage","text":"FIRST"},{"id":"b","type":"agentMessage","text":"SECOND"}]}]}}`)
							}
						} else if mode == "older-backend" {
							response.Result = json.RawMessage(`{"thread":{"id":"history"}}`)
						} else if mode == "legacy" {
							response.Result = json.RawMessage(`{"thread":{"id":"history","historyMode":"legacy"}}`)
						} else {
							response.Result = json.RawMessage(`{"thread":{"id":"history","historyMode":"paginated"}}`)
						}
					case "thread/turns/list":
						var params struct {
							Cursor        string `json:"cursor"`
							ItemsView     string `json:"itemsView"`
							SortDirection string `json:"sortDirection"`
						}
						_ = json.Unmarshal(request.Params, &params)
						if params.ItemsView != "full" || params.SortDirection != "desc" {
							t.Errorf("history pagination must request full turns: %s", request.Params)
						}
						if mode == "page-error" {
							response.Error = &rpcError{Code: -32000, Message: "history unavailable"}
						} else if mode == "repeated-cursor" {
							response.Result = json.RawMessage(`{"data":[],"nextCursor":"same"}`)
						} else if params.Cursor == "" {
							response.Result = json.RawMessage(`{"data":[{"id":"recent","items":[{"id":"c","type":"agentMessage","text":"THIRD"},{"id":"d","type":"agentMessage","text":"FOURTH"}]}],"nextCursor":"older"}`)
						} else {
							response.Result = json.RawMessage(`{"data":[{"id":"old","items":[{"id":"a","type":"agentMessage","text":"FIRST"},{"id":"b","type":"agentMessage","text":"SECOND"}]}],"nextCursor":null}`)
						}
					default:
						t.Errorf("unexpected history request: %s", request.Method)
					}
					if response.Error != nil {
						response.Result = nil
					}
					writeRPCMessage(backendIO, response)
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err := agent.LoadSession(ctx, acp.LoadSessionRequest{SessionId: "history", Cwd: "/contract", McpServers: []acp.McpServer{}})
			if mode == "read-error" || mode == "page-error" || mode == "repeated-cursor" {
				if err == nil || ctx.Err() != nil {
					t.Fatalf("incomplete history silently succeeded or looped: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"FIRST", "SECOND"}
			if mode == "paginated" {
				want = append(want, "THIRD", "FOURTH")
			}
			last := -1
			for _, text := range want {
				index := strings.Index(output.String(), text)
				if index <= last {
					t.Fatalf("history missing or out of order at %s: %s", text, output.String())
				}
				last = index
			}
		})
	}
}
