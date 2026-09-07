package codex

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
)

func TestEarlyEventsKeepTheirTurnAndTheirOrder(t *testing.T) {
	reader, input := io.Pipe()
	defer reader.Close()
	defer input.Close()
	var output strings.Builder
	conn := acp.NewAgentSideConnection(nil, &output, reader)
	session, client := faultBackend(t, func(backend net.Conn, request rpcMessage) {
		for _, event := range []rpcMessage{
			{Method: "item/agentMessage/delta", Params: json.RawMessage(`{"threadId":"thread-1","turnId":"old-turn","itemId":"old","delta":"STALE"}`)},
			{Method: "item/agentMessage/delta", Params: json.RawMessage(`{"threadId":"thread-1","turnId":"new-turn","itemId":"new","delta":"first "}`)},
			{Method: "item/agentMessage/delta", Params: json.RawMessage(`{"threadId":"thread-1","turnId":"new-turn","itemId":"new","delta":"second"}`)},
		} {
			writeRPCMessage(backend, event)
		}
		replyTurnStarted(backend, request)
		writeRPCMessage(backend, rpcMessage{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread-1","turn":{"id":"new-turn","status":"completed"}}`)})
		_ = backend.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stop, _, err := session.runTurn(ctx, conn, client, acp.ClientCapabilities{}, nil, []acp.ContentBlock{acp.TextBlock("hello")})
	if err != nil || stop != acp.StopReasonEndTurn {
		t.Fatalf("early events/EOF lost the result: stop=%q err=%v", stop, err)
	}
	text := output.String()
	if strings.Contains(text, "STALE") || !strings.Contains(text, "first ") || strings.Index(text, "first ") > strings.Index(text, "second") {
		t.Fatalf("events were lost, reordered or attributed to the wrong turn: %s", text)
	}
}

func TestTurnQueueFailsVisiblyWhenDeliveryCannotKeepUp(t *testing.T) {
	for _, size := range []int{1, maxQueuedTurnBytes + 1} {
		stream := newTurnStream(context.Background())
		if size == 1 {
			for range maxQueuedTurnEvents + 1 {
				stream.enqueue("event", json.RawMessage(`{}`))
			}
		} else {
			stream.enqueue("event", json.RawMessage(strings.Repeat("x", size)))
		}
		if _, err := stream.next(nil); err == nil {
			t.Fatal("overflow silently dropped events")
		}
		if stream.bytes.Load() > maxQueuedTurnBytes || len(stream.events) > maxQueuedTurnEvents {
			t.Fatal("delivery queue exceeded its bounds")
		}
	}
}

func TestBurstCommandOutputDoesNotOverflowDeliveryQueue(t *testing.T) {
	reader, input := io.Pipe()
	defer reader.Close()
	defer input.Close()
	writer := &countingClientWriter{}
	conn := acp.NewAgentSideConnection(nil, writer, reader)
	session, client := faultBackend(t, func(backend net.Conn, request rpcMessage) {
		replyTurnStarted(backend, request)
		params, _ := json.Marshal(map[string]any{"threadId": "thread-1", "turnId": "new-turn", "itemId": "command", "delta": strings.Repeat("x", 4096)})
		for range 2048 {
			writeRPCMessage(backend, rpcMessage{Method: "item/commandExecution/outputDelta", Params: params})
		}
		writeRPCMessage(backend, rpcMessage{Method: "item/completed", Params: json.RawMessage(`{"threadId":"thread-1","turnId":"new-turn","item":{"id":"command","type":"commandExecution","status":"completed"}}`)})
		writeRPCMessage(backend, rpcMessage{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread-1","turn":{"id":"new-turn","status":"completed"}}`)})
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stop, _, err := session.runTurn(ctx, conn, client, acp.ClientCapabilities{}, nil, []acp.ContentBlock{acp.TextBlock("large command")})
	if err != nil || stop != acp.StopReasonEndTurn {
		t.Fatalf("output burst interrupted a healthy turn: stop=%q err=%v", stop, err)
	}
	if writer.bytes > maxToolOutputBytes+1024 {
		t.Fatalf("output burst sent %d bytes to the client", writer.bytes)
	}
}

func TestOldSessionCleanupCannotRemoveReplacementHandlers(t *testing.T) {
	client := &codexClient{handlers: make(map[string]*threadHandlers)}
	old, current := &threadHandlers{}, &threadHandlers{}
	client.setThreadHandlers("thread", old)
	client.setThreadHandlers("thread", current)
	client.removeThreadHandlers("thread", old)
	if client.handlersFor("thread") != current {
		t.Fatal("old prompt removed the new prompt's handlers")
	}
}

func TestTurnErrorsDistinguishRetriesFromTerminalFailures(t *testing.T) {
	for _, tc := range []struct{ name, event, completion, wantError string }{
		{"retry", `{"turnId":"new-turn","willRetry":true,"error":{"message":"retrying","codexErrorInfo":"rateLimitExceeded"}}`, `{"id":"new-turn","status":"completed"}`, ""},
		{"terminal", `{"turnId":"new-turn","willRetry":false,"error":{"message":"retries exhausted"}}`, "", "retries exhausted"},
		{"legacy", `{"turnId":"new-turn","error":{"message":"connection failed"}}`, `{"id":"new-turn","status":"failed"}`, "connection failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader, input := io.Pipe()
			defer reader.Close()
			defer input.Close()
			conn := acp.NewAgentSideConnection(nil, io.Discard, reader)
			session, client := faultBackend(t, func(backend net.Conn, request rpcMessage) {
				replyTurnStarted(backend, request)
				writeRPCMessage(backend, rpcMessage{Method: "error", Params: json.RawMessage(`{"threadId":"thread-1",` + tc.event[1:])})
				if tc.completion != "" {
					writeRPCMessage(backend, rpcMessage{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread-1","turn":` + tc.completion + `}`)})
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			stop, _, err := session.runTurn(ctx, conn, client, acp.ClientCapabilities{}, nil, []acp.ContentBlock{acp.TextBlock("hello")})
			if tc.wantError == "" {
				if err != nil || stop != acp.StopReasonEndTurn {
					t.Fatalf("retry ended the turn: stop=%q err=%v", stop, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("terminal failure not reported: stop=%q err=%v", stop, err)
			}
		})
	}
}
