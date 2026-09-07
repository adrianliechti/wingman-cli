package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/coder/acp-go-sdk"
)

func faultBackend(t *testing.T, respond func(net.Conn, rpcMessage)) (*session, *codexClient) {
	t.Helper()
	agentIO, serverIO := net.Pipe()
	t.Cleanup(func() {
		_ = agentIO.Close()
		_ = serverIO.Close()
	})
	rpc := newRPCClient(agentIO, agentIO)
	client := newCodexClient(rpc)
	rpc.start()
	go func() {
		scanner := bufio.NewScanner(serverIO)
		if scanner.Scan() {
			var request rpcMessage
			if json.Unmarshal(scanner.Bytes(), &request) == nil {
				respond(serverIO, request)
			}
		}
	}()
	return newSession("thread-1", "default", "default", nil), client
}

func replyTurnStarted(conn net.Conn, request rpcMessage) {
	writeRPCMessage(conn, rpcMessage{ID: request.ID, Result: json.RawMessage(`{"turn":{"id":"new-turn","status":"inProgress"}}`)})
}

func TestLateCompletionMustNotFinishNextPrompt(t *testing.T) {
	for _, order := range []string{"before-start-reply", "after-start-reply"} {
		t.Run(order, func(t *testing.T) {
			session, client := faultBackend(t, func(conn net.Conn, request rpcMessage) {
				if order == "after-start-reply" {
					replyTurnStarted(conn, request)
				}
				// The previous interrupted turn ends after a fresh prompt installs
				// its handler. This is legal on a reused thread.
				writeRPCMessage(conn, rpcMessage{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread-1","turn":{"id":"old-turn","status":"interrupted"}}`)})
				if order == "before-start-reply" {
					replyTurnStarted(conn, request)
				}
				writeRPCMessage(conn, rpcMessage{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread-1","turn":{"id":"new-turn","status":"completed"}}`)})
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			stop, _, err := session.runTurn(ctx, nil, client, acp.ClientCapabilities{}, nil, []acp.ContentBlock{acp.TextBlock("next prompt")})
			if err != nil || stop != acp.StopReasonEndTurn {
				t.Fatalf("new prompt consumed old turn's completion: stop=%q err=%v; want end_turn for new-turn", stop, err)
			}
		})
	}
}

func TestBackendEOFMustSettlePrompt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		eof := make(chan struct{})
		session, client := faultBackend(t, func(conn net.Conn, request rpcMessage) {
			replyTurnStarted(conn, request)
			<-eof
			_ = conn.Close()
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			_, _, err := session.runTurn(ctx, nil, client, acp.ClientCapabilities{}, nil, []acp.ContentBlock{acp.TextBlock("hello")})
			done <- err
		}()
		// Wait until turn/start has completed and the prompt is awaiting events.
		synctest.Wait()
		close(eof)
		synctest.Wait()
		select {
		case err := <-done:
			if !errors.Is(err, errRPCClosed) {
				t.Fatalf("backend EOF returned %v; want connection-closed error", err)
			}
		default:
			t.Fatal("backend EOF left the active prompt pending until external cancellation")
		}
	})
}

func TestFailedTurnMustReturnError(t *testing.T) {
	session, client := faultBackend(t, func(conn net.Conn, request rpcMessage) {
		replyTurnStarted(conn, request)
		writeRPCMessage(conn, rpcMessage{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread-1","turn":{"id":"new-turn","status":"failed","error":{"message":"stream disconnected; retries exhausted","codexErrorInfo":"responseTooManyFailedAttempts"}}}`)})
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stop, _, err := session.runTurn(ctx, nil, client, acp.ClientCapabilities{}, nil, []acp.ContentBlock{acp.TextBlock("hello")})
	if err == nil || !strings.Contains(err.Error(), "stream disconnected") {
		t.Fatalf("backend failure was reported as stop=%q err=%v; expected the turn error", stop, err)
	}
}

type blockedClientWriter struct {
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (w *blockedClientWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	return len(p), nil
}

func TestBlockedIDEWriteMustNotBlockCodexReplies(t *testing.T) {
	agentIO, serverIO := net.Pipe()
	acpRead, acpInput := io.Pipe()
	writer := &blockedClientWriter{entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() {
		close(writer.release)
		_ = agentIO.Close()
		_ = serverIO.Close()
		_ = acpRead.Close()
		_ = acpInput.Close()
	})
	conn := acp.NewAgentSideConnection(nil, writer, acpRead)
	rpc := newRPCClient(agentIO, agentIO)
	client := newCodexClient(rpc)
	rpc.start()
	go func() {
		scanner := bufio.NewScanner(serverIO)
		for scanner.Scan() {
			var request rpcMessage
			_ = json.Unmarshal(scanner.Bytes(), &request)
			switch request.Method {
			case "turn/start":
				replyTurnStarted(serverIO, request)
				writeRPCMessage(serverIO, rpcMessage{Method: "item/agentMessage/delta", Params: json.RawMessage(`{"threadId":"thread-1","turnId":"new-turn","itemId":"answer","delta":"hello"}`)})
			case "turn/interrupt":
				writeRPCMessage(serverIO, rpcMessage{ID: request.ID, Result: json.RawMessage(`{}`)})
			}
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := newSession("thread-1", "default", "default", nil)
	done := make(chan acp.StopReason, 1)
	go func() {
		stop, _, _ := session.runTurn(ctx, conn, client, acp.ClientCapabilities{}, nil, []acp.ContentBlock{acp.TextBlock("hello")})
		done <- stop
	}()
	select {
	case <-writer.entered:
	case <-time.After(time.Second):
		t.Fatal("did not reach the blocked IDE write")
	}
	interruptCtx, cancelInterrupt := context.WithTimeout(context.Background(), time.Second)
	defer cancelInterrupt()
	if err := client.turnInterrupt(interruptCtx, turnInterruptParams{ThreadID: "thread-1", TurnID: "new-turn"}); err != nil {
		t.Fatalf("Codex reply blocked behind IDE notification: %v", err)
	}
	cancel()
	select {
	case stop := <-done:
		if stop != acp.StopReasonCancelled {
			t.Fatalf("cancelled prompt returned %q", stop)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation waited for the blocked IDE write")
	}
}

type countingClientWriter struct {
	bytes int
}

func (w *countingClientWriter) Write(p []byte) (int, error) {
	w.bytes += len(p)
	return len(p), nil
}

func TestCommandOutputTraffic(t *testing.T) {
	reader, input := io.Pipe()
	t.Cleanup(func() {
		_ = reader.Close()
		_ = input.Close()
	})
	writer := &countingClientWriter{}
	conn := acp.NewAgentSideConnection(nil, writer, reader)
	dispatcher := newEventDispatcher(context.Background(), conn, "thread-1")
	const chunks, chunkSize = 256, 4096
	for range chunks {
		dispatcher.appendToolOut("command-1", strings.Repeat("x", chunkSize))
	}
	dispatcher.handleItemCompleted(json.RawMessage(`{"item":{"id":"command-1","type":"commandExecution","status":"completed"}}`))
	if writer.bytes < chunks*chunkSize || writer.bytes > chunks*chunkSize+1024 {
		t.Fatalf("expected one complete output snapshot; got %d bytes", writer.bytes)
	}
	t.Logf("%d bytes of command output -> %d bytes of ACP traffic (%.1fx amplification)",
		chunks*chunkSize, writer.bytes, float64(writer.bytes)/float64(chunks*chunkSize))
}

func TestRunMustReportUnexpectedBackendExit(t *testing.T) {
	for _, mode := range []string{"1", "inherited-stdout"} {
		t.Run(mode, func(t *testing.T) {
			reader, input := io.Pipe()
			t.Cleanup(func() {
				_ = reader.Close()
				_ = input.Close()
			})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			err := Run(ctx, Options{
				Path: os.Args[0], ExtraArgs: []string{"-test.run=^TestExitedBackendHelper$"},
				Env: append(os.Environ(), "WINGMAN_ACP_EXIT_HELPER="+mode), Stderr: io.Discard,
			}, reader, io.Discard, nil)
			if err == nil || !strings.Contains(err.Error(), "exit status 23") {
				t.Fatalf("backend exited with status 23, but ACP Run returned %v", err)
			}
		})
	}
}

func TestExitedBackendHelper(t *testing.T) {
	switch os.Getenv("WINGMAN_ACP_EXIT_HELPER") {
	case "hold-stdout":
		time.Sleep(3 * time.Second)
		os.Exit(0)
	case "inherited-stdout":
		child := exec.Command(os.Args[0], "-test.run=^TestExitedBackendHelper$")
		child.Env = append(os.Environ(), "WINGMAN_ACP_EXIT_HELPER=hold-stdout")
		child.Stdout = os.Stdout
		if child.Start() != nil {
			os.Exit(24)
		}
		os.Exit(23)
	case "1":
		os.Exit(23)
	}
}
