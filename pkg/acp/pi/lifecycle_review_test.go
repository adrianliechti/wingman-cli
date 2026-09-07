package pi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"testing/synctest"

	"github.com/coder/acp-go-sdk"
)

func TestRequestCancellationRetiresBlockedWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		local, peer := net.Pipe()
		defer local.Close()
		defer peer.Close()
		p := &process{stdin: local, done: make(chan struct{}), pending: map[string]chan rpcResponse{}}
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { _, err := p.request(ctx, map[string]any{"type": "get_state"}); result <- err }()
		synctest.Wait()
		cancel()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("blocked write = %v", err)
		}
		if err := p.writeLine(map[string]any{"type": "prompt"}); !errors.Is(err, errProcessClosed) {
			t.Fatalf("retired transport = %v", err)
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		if len(p.pending) != 0 {
			t.Fatalf("leaked %d pending requests", len(p.pending))
		}
	})
}

func TestCancelledRequestDoesNotWriteToBackend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A cancelled request must return before touching even an absent transport.
	if _, err := (&process{}).request(ctx, map[string]any{"type": "prompt"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request = %v", err)
	}
}

func TestAbortAcknowledgementDoesNotReleaseTurnBeforeSettled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		local, backend := net.Pipe()
		defer local.Close()
		defer backend.Close()
		p := &process{stdin: local, done: make(chan struct{}), pending: map[string]chan rpcResponse{}}
		go p.readLoop(local)
		started, abortAck, settled := make(chan struct{}), make(chan struct{}), make(chan struct{})
		go func() {
			scanner := bufio.NewScanner(backend)
			enc := json.NewEncoder(backend)
			for scanner.Scan() {
				var req struct {
					ID   string `json:"id"`
					Type string `json:"type"`
				}
				_ = json.Unmarshal(scanner.Bytes(), &req)
				_ = enc.Encode(rpcResponse{ID: req.ID, Type: "response", Success: true})
				if req.Type == "prompt" {
					close(started)
				}
				if req.Type == "abort" {
					close(abortAck)
					<-settled
					_ = enc.Encode(map[string]any{"type": "agent_settled"})
				}
			}
		}()
		s := newSession("s", t.TempDir(), p)
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan acp.StopReason, 1)
		go func() {
			stop, err := s.runTurn(ctx, nil, []acp.ContentBlock{acp.TextBlock("start")})
			if err != nil {
				t.Error(err)
			}
			result <- stop
		}()
		<-started
		cancel()
		<-abortAck
		synctest.Wait()
		select {
		case <-result:
			t.Fatal("abort acknowledgement released the turn")
		default:
		}
		close(settled)
		if stop := <-result; stop != acp.StopReasonCancelled {
			t.Fatalf("stop = %s", stop)
		}
		if s.isClosed() {
			t.Fatal("successfully drained cancellation closed the session")
		}
	})
}

func TestQueuedPromptCancellationDoesNotWaitForRunningPrompt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := New(Options{})
		s := newSession("s", t.TempDir(), nil)
		a.sessions[s.id] = s
		if err := s.promptMu.Lock(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer s.promptMu.Unlock()
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan acp.StopReason, 1)
		go func() { r, _ := a.Prompt(ctx, acp.PromptRequest{SessionId: s.id}); result <- r.StopReason }()
		synctest.Wait()
		cancel()
		if stop := <-result; stop != acp.StopReasonCancelled {
			t.Fatalf("queued prompt = %s", stop)
		}
	})
}

func TestSettledTurnSurvivesImmediateBackendEOF(t *testing.T) {
	for range 20 {
		synctest.Test(t, func(t *testing.T) {
			local, backend := net.Pipe()
			defer local.Close()
			defer backend.Close()
			p := &process{stdin: local, done: make(chan struct{}), pending: map[string]chan rpcResponse{}}
			go p.readLoop(local)
			go func() {
				var req struct {
					ID string `json:"id"`
				}
				_ = json.NewDecoder(backend).Decode(&req)
				enc := json.NewEncoder(backend)
				_ = enc.Encode(rpcResponse{Type: "response", ID: req.ID, Success: true})
				_ = enc.Encode(map[string]any{"type": "agent_settled"})
				_ = backend.Close()
			}()
			s := newSession("s", t.TempDir(), p)
			stop, err := s.runTurn(context.Background(), nil, []acp.ContentBlock{acp.TextBlock("go")})
			if err != nil || stop != acp.StopReasonEndTurn {
				t.Fatalf("final settled event lost at EOF: %s, %v", stop, err)
			}
		})
	}
}
