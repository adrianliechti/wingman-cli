package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
)

func TestCancelInterruptsTurnThatStartsAfterCancellation(t *testing.T) {
	agentIO, serverIO := net.Pipe()
	t.Cleanup(func() {
		_ = agentIO.Close()
		_ = serverIO.Close()
	})

	startReceived := make(chan struct{})
	releaseStart := make(chan struct{})
	interrupted := make(chan turnInterruptParams, 1)
	go func() {
		scanner := bufio.NewScanner(serverIO)
		for scanner.Scan() {
			var msg rpcMessage
			if json.Unmarshal(scanner.Bytes(), &msg) != nil {
				continue
			}
			switch msg.Method {
			case "turn/start":
				close(startReceived)
				<-releaseStart
				result, _ := json.Marshal(turnStartResponse{Turn: turn{ID: "late-turn", Status: "inProgress"}})
				writeRPCMessage(serverIO, rpcMessage{Jsonrpc: "2.0", ID: msg.ID, Result: result})
			case "turn/interrupt":
				var params turnInterruptParams
				_ = json.Unmarshal(msg.Params, &params)
				interrupted <- params
				writeRPCMessage(serverIO, rpcMessage{Jsonrpc: "2.0", ID: msg.ID, Result: json.RawMessage(`{}`)})
			}
		}
	}()

	rpc := newRPCClient(agentIO, agentIO)
	client := newCodexClient(rpc)
	rpc.start()
	sess := newSession("thread-1", "default", "default", nil)

	type promptResult struct {
		reason acp.StopReason
		err    error
	}
	done := make(chan promptResult, 1)
	go func() {
		reason, _, err := sess.runTurn(context.Background(), nil, client, acp.ClientCapabilities{}, nil, []acp.ContentBlock{acp.TextBlock("hello")})
		done <- promptResult{reason: reason, err: err}
	}()

	select {
	case <-startReceived:
	case <-time.After(time.Second):
		t.Fatal("turn/start was not received")
	}
	sess.interrupt(context.Background(), client)
	select {
	case result := <-done:
		if result.err != nil || result.reason != acp.StopReasonCancelled {
			t.Fatalf("prompt result = %q, %v", result.reason, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel waited for the delayed turn/start response")
	}

	close(releaseStart)
	select {
	case params := <-interrupted:
		if params.ThreadID != "thread-1" || params.TurnID != "late-turn" {
			t.Fatalf("interrupt params = %+v", params)
		}
	case <-time.After(time.Second):
		t.Fatal("late-started turn was not interrupted")
	}
}

func writeRPCMessage(conn net.Conn, msg rpcMessage) {
	b, _ := json.Marshal(msg)
	_, _ = conn.Write(append(b, '\n'))
}
