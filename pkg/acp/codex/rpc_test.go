package codex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestRPCCancellationUnblocksWriteAndRetiresConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		agentIO, backendIO := net.Pipe()
		defer agentIO.Close()
		defer backendIO.Close()
		rpc := newRPCClient(agentIO, agentIO)
		rpc.start()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- rpc.call(ctx, "turn/interrupt", nil, nil) }()
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("blocked write returned %v", err)
		}
		if err := rpc.call(context.Background(), "thread/read", nil, nil); !errors.Is(err, errRPCClosed) {
			t.Fatalf("partially written transport was reused: %v", err)
		}
		rpc.pending.Range(func(key, value any) bool {
			t.Errorf("request %v was left pending", key)
			return true
		})
	})
}

func TestRPCPreservesReplyWhenEOFBeatsWriteAcknowledgement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		writer := &blockedClientWriter{entered: make(chan struct{}), release: make(chan struct{})}
		defer close(writer.release)
		rpc := newRPCClient(writer, strings.NewReader("{\"id\":1,\"result\":{\"ok\":true}}\n"))
		done := make(chan error, 1)
		var response struct {
			OK bool `json:"ok"`
		}
		go func() { done <- rpc.call(context.Background(), "thread/read", nil, &response) }()
		<-writer.entered
		rpc.start()
		if err := <-done; err != nil || !response.OK {
			t.Fatalf("reply lost on EOF: response=%+v err=%v", response, err)
		}
	})
}

func TestRPCReadErrorsAreReported(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"malformed", "{broken}\n", "decode app-server message"},
		{"oversized", strings.Repeat("x", 16*1024*1024+1), "token too long"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rpc := newRPCClient(io.Discard, strings.NewReader(tc.input))
			rpc.start()
			select {
			case <-rpc.done:
			case <-time.After(time.Second):
				t.Fatal("reader did not close after a framing error")
			}
			err := rpc.closedError()
			if !errors.Is(err, errRPCClosed) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("stream error = %v", err)
			}
		})
	}
}

func TestRPCRequestsHaveAResponseDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reader, input := io.Pipe()
		defer reader.Close()
		defer input.Close()
		rpc := newRPCClient(io.Discard, reader)
		rpc.start()
		started := time.Now()
		if err := rpc.call(context.Background(), "model/list", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("unanswered RPC returned %v", err)
		}
		if elapsed := time.Since(started); elapsed != rpcRequestTimeout {
			t.Fatalf("response deadline took %s", elapsed)
		}
	})
}

func TestClientWriterBoundsStalledWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		writerIO, readerIO := net.Pipe()
		defer readerIO.Close()
		writer := newClientWriter(writerIO)
		started := time.Now()
		if _, err := writer.Write([]byte("notification\n")); err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("stalled client write returned %v", err)
		}
		if elapsed := time.Since(started); elapsed != clientWriteTimeout {
			t.Fatalf("stalled write took %s", elapsed)
		}
		if _, err := writer.Write([]byte("next\n")); err == nil {
			t.Fatal("broken client transport accepted another write")
		}
	})
}

func TestResolvedRequestCancelsOnlyItsOwnHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		agentIO, backendIO := net.Pipe()
		defer agentIO.Close()
		defer backendIO.Close()
		rpc := newRPCClient(agentIO, agentIO)
		entered := make(chan context.Context, 2)
		rpc.onRequest = func(ctx context.Context, _ string, _ json.RawMessage) (any, *rpcError) {
			entered <- ctx
			<-ctx.Done()
			return map[string]any{"decision": "cancel"}, nil
		}
		rpc.start()
		writeRPCMessage(backendIO, rpcMessage{ID: json.RawMessage(`"first"`), Method: "approval", Params: json.RawMessage(`{"threadId":"thread"}`)})
		first := <-entered
		writeRPCMessage(backendIO, rpcMessage{ID: json.RawMessage(`2`), Method: "approval", Params: json.RawMessage(`{"threadId":"thread"}`)})
		second := <-entered
		writeRPCMessage(backendIO, rpcMessage{Method: "serverRequest/resolved", Params: json.RawMessage(`{"threadId":"another-thread","requestId":"first"}`)})
		synctest.Wait()
		if first.Err() != nil || second.Err() != nil {
			t.Fatal("resolution from another thread cancelled an approval")
		}
		writeRPCMessage(backendIO, rpcMessage{Method: "serverRequest/resolved", Params: json.RawMessage(`{"threadId":"thread","requestId":"first"}`)})
		synctest.Wait()
		if first.Err() == nil || second.Err() != nil {
			t.Fatalf("request resolution was not isolated: first=%v second=%v", first.Err(), second.Err())
		}
		rpc.close(io.EOF)
		if second.Err() == nil {
			t.Fatal("backend EOF left a client interaction pending")
		}
	})
}
