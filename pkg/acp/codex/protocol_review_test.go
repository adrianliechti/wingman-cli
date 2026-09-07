package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/adrianliechti/wingman-agent/pkg/acp/internal/acptest"
)

// Exercise the actual JSON-RPC envelopes, including a v2 initialize shape that
// cannot be represented by the v1 SDK's InitializeRequest.
type protocolPeer struct {
	io     net.Conn
	frames chan rpcMessage
}

func newProtocolPeer(t *testing.T, agent *Agent) *protocolPeer {
	t.Helper()
	agentIO, clientIO := net.Pipe()
	t.Cleanup(func() {
		_ = agentIO.Close()
		_ = clientIO.Close()
	})
	agent.SetAgentConnection(acp.NewAgentSideConnection(agent, agentIO, agentIO))
	peer := &protocolPeer{io: clientIO, frames: make(chan rpcMessage, 64)}
	go func() {
		defer close(peer.frames)
		scanner := bufio.NewScanner(clientIO)
		for scanner.Scan() {
			var frame rpcMessage
			if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
				t.Errorf("invalid ACP frame: %v", err)
				return
			}
			peer.frames <- frame
		}
	}()
	return peer
}

func (p *protocolPeer) send(t *testing.T, id, method, params string) {
	t.Helper()
	_ = p.io.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if err := json.NewEncoder(p.io).Encode(rpcMessage{
		Jsonrpc: "2.0", ID: json.RawMessage(id), Method: method, Params: json.RawMessage(params),
	}); err != nil {
		t.Fatal(err)
	}
}

func (p *protocolPeer) next(t *testing.T) rpcMessage {
	t.Helper()
	select {
	case frame, ok := <-p.frames:
		if !ok {
			t.Fatal("ACP peer disconnected")
		}
		if frame.Jsonrpc != "2.0" {
			t.Fatalf("invalid JSON-RPC version: %q", frame.Jsonrpc)
		}
		return frame
	case <-time.After(3 * time.Second):
		t.Fatal("ACP peer did not answer")
		return rpcMessage{}
	}
}

func (p *protocolPeer) reply(t *testing.T, id json.RawMessage, result string) {
	t.Helper()
	_ = p.io.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if err := json.NewEncoder(p.io).Encode(rpcMessage{Jsonrpc: "2.0", ID: id, Result: json.RawMessage(result)}); err != nil {
		t.Fatal(err)
	}
}

func (p *protocolPeer) response(t *testing.T, id string) rpcMessage {
	t.Helper()
	for {
		frame := p.next(t)
		if string(frame.ID) == id {
			return frame
		}
		if frame.Method != "session/update" {
			t.Fatalf("unexpected frame while waiting for %s: %+v", id, frame)
		}
	}
}

func TestACPVersionNegotiationAndV1Cancellation(t *testing.T) {
	for _, init := range []struct{ name, params string }{
		{"v1", `{"protocolVersion":1,"clientCapabilities":{}}`},
		{"v2-fallback", `{"protocolVersion":2,"info":{"name":"review-client","version":"1"},"capabilities":{}}`},
	} {
		for _, cancellation := range []string{"session/cancel", "$/cancel_request"} {
			t.Run(init.name+"/"+cancellation, func(t *testing.T) {
				peer := newProtocolPeer(t, newContractAgent(t).(*Agent))
				peer.send(t, `"init"`, "initialize", init.params)
				initialized := peer.response(t, `"init"`)
				var result map[string]json.RawMessage
				if err := json.Unmarshal(initialized.Result, &result); err != nil || initialized.Error != nil {
					t.Fatalf("initialize = %+v, %v", initialized, err)
				}
				if string(result["protocolVersion"]) != "1" || result["agentCapabilities"] == nil || result["info"] != nil || string(result["authMethods"]) != "[]" {
					t.Fatalf("expected a v1 negotiation response: %s", initialized.Result)
				}
				peer.send(t, `"new"`, "session/new", `{"cwd":"/contract","mcpServers":[]}`)
				created := peer.response(t, `"new"`)
				var session acp.NewSessionResponse
				if err := json.Unmarshal(created.Result, &session); err != nil || created.Error != nil || session.SessionId == "" {
					t.Fatalf("session/new = %+v, %v", created, err)
				}
				params, _ := json.Marshal(acp.PromptRequest{SessionId: session.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock(acptest.CancelPrompt)}})
				peer.send(t, `"prompt"`, "session/prompt", string(params))
				for {
					frame := peer.next(t)
					if len(frame.ID) > 0 {
						t.Fatalf("v1 prompt acknowledged before completion: %+v", frame)
					}
					if strings.Contains(string(frame.Params), acptest.CancelText) {
						break
					}
				}
				cancelParams, _ := json.Marshal(map[string]any{"sessionId": session.SessionId})
				if cancellation == "$/cancel_request" {
					cancelParams = []byte(`{"requestId":"prompt"}`)
				}
				peer.send(t, "", cancellation, string(cancelParams))
				cancelled := peer.response(t, `"prompt"`)
				var response acp.PromptResponse
				if err := json.Unmarshal(cancelled.Result, &response); err != nil || cancelled.Error != nil || response.StopReason != acp.StopReasonCancelled {
					t.Fatalf("cancellation must be a stop reason: %+v, %v", cancelled, err)
				}
				params, _ = json.Marshal(acp.PromptRequest{SessionId: session.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("hello")}})
				peer.send(t, `"next"`, "session/prompt", string(params))
				completed := peer.response(t, `"next"`)
				if err := json.Unmarshal(completed.Result, &response); err != nil || completed.Error != nil || response.StopReason != acp.StopReasonEndTurn {
					t.Fatalf("next prompt failed: %+v, %v", completed, err)
				}
			})
		}
	}
}

func TestMalformedCompletionSettlesThePrompt(t *testing.T) {
	for _, body := range []string{
		`{"id":"new-turn","status":42}`,
		`{"id":"new-turn","status":"failed","error":"invalid error shape"}`,
		`{"status":"completed"}`,
		`null`,
	} {
		t.Run(body, func(t *testing.T) {
			session, client := faultBackend(t, func(conn net.Conn, request rpcMessage) {
				replyTurnStarted(conn, request)
				writeRPCMessage(conn, rpcMessage{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread-1","turn":` + body + `}`)})
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			stop, _, err := session.runTurn(ctx, nil, client, acp.ClientCapabilities{}, nil, nil)
			if err == nil || ctx.Err() != nil || !strings.Contains(err.Error(), "turn/completed") {
				t.Fatalf("malformed completion left the prompt waiting: stop=%q err=%v", stop, err)
			}
		})
	}
}

func TestElicitationUsesItsBackendTurnID(t *testing.T) {
	for _, turnID := range []string{"old-turn", "new-turn", ""} {
		t.Run(turnID, func(t *testing.T) {
			answered := make(chan elicitationResponse, 1)
			session, client := faultBackend(t, func(conn net.Conn, request rpcMessage) {
				replyTurnStarted(conn, request)
				params, _ := json.Marshal(map[string]any{
					"threadId": "thread-1", "turnId": turnID, "serverName": "test", "mode": "form",
					"requestedSchema": map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}},
				})
				writeRPCMessage(conn, rpcMessage{ID: json.RawMessage(`"elicitation"`), Method: "mcpServer/elicitation/request", Params: params})
				var reply rpcMessage
				_ = json.NewDecoder(conn).Decode(&reply)
				var response elicitationResponse
				_ = json.Unmarshal(reply.Result, &response)
				answered <- response
				writeRPCMessage(conn, rpcMessage{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread-1","turn":{"id":"new-turn","status":"completed"}}`)})
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			stop, _, err := session.runTurn(ctx, nil, client, acp.ClientCapabilities{}, nil, nil)
			if err != nil || stop != acp.StopReasonEndTurn {
				t.Fatalf("turn = %q, %v", stop, err)
			}
			want := "decline" // No form capability; gracefully decline a current request.
			if turnID == "old-turn" {
				want = "cancel"
			}
			if response := <-answered; response.Action != want {
				t.Fatalf("elicitation from %q = %+v, want %s", turnID, response, want)
			}
		})
	}
}

func TestURLConsentDoesNotCompleteExternalInteractions(t *testing.T) {
	session, client := faultBackend(t, func(conn net.Conn, request rpcMessage) {
		replyTurnStarted(conn, request)
		for _, requestID := range []string{`100`, `101`} {
			writeRPCMessage(conn, rpcMessage{ID: json.RawMessage(requestID), Method: "mcpServer/elicitation/request", Params: json.RawMessage(`{"threadId":"thread-1","turnId":"new-turn","serverName":"mcp","mode":"url","elicitationId":"server-local-id","message":"Connect account","url":"https://example.com/connect"}`)})
			var reply rpcMessage
			_ = json.NewDecoder(conn).Decode(&reply)
			var response elicitationResponse
			_ = json.Unmarshal(reply.Result, &response)
			if response.Action != "accept" || response.Content != nil {
				t.Errorf("URL consent forwarded unexpected content: %+v", response)
			}
			writeRPCMessage(conn, rpcMessage{Method: "serverRequest/resolved", Params: json.RawMessage(`{"threadId":"thread-1","requestId":` + requestID + `}`)})
		}
		writeRPCMessage(conn, rpcMessage{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread-1","turn":{"id":"new-turn","status":"completed"}}`)})
	})
	agent := newAgent(client, "default", "")
	agent.sessions[session.id] = session
	agent.clientCapabilities.Elicitation = &acp.ElicitationCapabilities{Url: &acp.ElicitationUrlCapabilities{}}
	peer := newProtocolPeer(t, agent)
	peer.send(t, `"prompt"`, "session/prompt", `{"sessionId":"thread-1","prompt":[{"type":"text","text":"Connect"}]}`)
	ids := make(map[string]bool)
	for {
		frame := peer.next(t)
		switch frame.Method {
		case "elicitation/create":
			var params struct {
				ElicitationID string `json:"elicitationId"`
			}
			_ = json.Unmarshal(frame.Params, &params)
			if params.ElicitationID == "" || ids[params.ElicitationID] {
				t.Fatalf("outstanding URL interactions reused an ID: %s", frame.Params)
			}
			ids[params.ElicitationID] = true
			peer.reply(t, frame.ID, `{"action":"accept","content":{"unexpected":"do not forward"}}`)
		case "session/update":
		case "":
			if string(frame.ID) != `"prompt"` || frame.Error != nil || !strings.Contains(string(frame.Result), `"end_turn"`) || len(ids) != 2 {
				t.Fatalf("prompt result = %+v, interactions=%d", frame, len(ids))
			}
			return
		default:
			t.Fatalf("URL acceptance must not imply completion: %+v", frame)
		}
	}
}

func TestBackendResolvedPermissionDismissesDialogAndContinues(t *testing.T) {
	resolve := make(chan struct{})
	session, client := faultBackend(t, func(conn net.Conn, request rpcMessage) {
		replyTurnStarted(conn, request)
		writeRPCMessage(conn, rpcMessage{Method: "item/started", Params: json.RawMessage(`{"threadId":"thread-1","turnId":"new-turn","item":{"id":"command","type":"commandExecution","command":"echo hi","status":"inProgress"}}`)})
		writeRPCMessage(conn, rpcMessage{ID: json.RawMessage(`100`), Method: "item/commandExecution/requestApproval", Params: json.RawMessage(`{"threadId":"thread-1","turnId":"new-turn","itemId":"command","command":"echo hi"}`)})
		<-resolve
		writeRPCMessage(conn, rpcMessage{Method: "serverRequest/resolved", Params: json.RawMessage(`{"threadId":"thread-1","requestId":100}`)})
		var reply rpcMessage
		_ = json.NewDecoder(conn).Decode(&reply)
		var response execApprovalResponse
		_ = json.Unmarshal(reply.Result, &response)
		if response.Decision != "cancel" {
			t.Errorf("resolved approval accepted a late answer: %+v", response)
		}
		writeRPCMessage(conn, rpcMessage{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread-1","turn":{"id":"new-turn","status":"completed"}}`)})
	})
	agent := newAgent(client, "default", "")
	agent.sessions[session.id] = session
	peer := newProtocolPeer(t, agent)
	peer.send(t, `"prompt"`, "session/prompt", `{"sessionId":"thread-1","prompt":[{"type":"text","text":"Run"}]}`)
	var permissionID json.RawMessage
	for permissionID == nil {
		frame := peer.next(t)
		if frame.Method == "session/request_permission" {
			permissionID = frame.ID
		} else if frame.Method != "session/update" {
			t.Fatalf("expected permission request: %+v", frame)
		}
	}
	close(resolve)
	completed, dismissed := false, false
	for !completed || !dismissed {
		frame := peer.next(t)
		switch frame.Method {
		case "$/cancel_request":
			var params struct {
				RequestID json.RawMessage `json:"requestId"`
			}
			_ = json.Unmarshal(frame.Params, &params)
			if string(params.RequestID) != string(permissionID) {
				t.Fatalf("wrong dialog dismissed: %s", frame.Params)
			}
			dismissed = true
			// A late response must still be harmless to this connection.
			peer.reply(t, permissionID, `{"outcome":{"outcome":"selected","optionId":"allow-once"}}`)
		case "session/update":
		case "":
			if string(frame.ID) != `"prompt"` || frame.Error != nil || !strings.Contains(string(frame.Result), `"end_turn"`) {
				t.Fatalf("resolving one approval cancelled the turn: %+v", frame)
			}
			completed = true
		default:
			t.Fatalf("unexpected frame: %+v", frame)
		}
	}
}
