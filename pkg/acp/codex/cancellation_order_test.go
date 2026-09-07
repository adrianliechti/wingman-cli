package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"testing"
	"testing/synctest"
	"time"

	"github.com/coder/acp-go-sdk"
)

func TestNextPromptWaitsForCancelledBackendTurnCleanup(t *testing.T) {
	for _, lateStart := range []bool{false, true} {
		name := "active-turn"
		if lateStart {
			name = "pending-start"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				appIO, backendIO := net.Pipe()
				defer appIO.Close()
				defer backendIO.Close()
				rpc := newRPCClient(appIO, appIO)
				agent := newAgent(newCodexClient(rpc), "default", "")
				agent.sessions["thread-1"] = newSession("thread-1", "default", "", nil)
				rpc.start()
				startRelease, interruptRelease := make(chan struct{}), make(chan struct{})
				starts, interrupts := make(chan struct{}, 2), make(chan struct{}, 2)
				if !lateStart {
					close(startRelease)
				}
				server := &contractAppServer{conn: backendIO}
				go func() {
					scanner := bufio.NewScanner(backendIO)
					count := 0
					for scanner.Scan() {
						var request rpcMessage
						_ = json.Unmarshal(scanner.Bytes(), &request)
						switch request.Method {
						case "turn/start":
							count++
							starts <- struct{}{}
							if count == 1 {
								go func() {
									<-startRelease
									server.respond(request, turnStartResponse{Turn: turn{ID: "first", Status: "inProgress"}})
								}()
							} else {
								server.respond(request, turnStartResponse{Turn: turn{ID: "second", Status: "inProgress"}})
								server.send(rpcMessage{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread-1","turn":{"id":"second","status":"completed"}}`)})
							}
						case "turn/interrupt":
							interrupts <- struct{}{}
							go func() {
								<-interruptRelease
								server.respond(request, map[string]any{})
							}()
						}
					}
				}()
				type result struct {
					response acp.PromptResponse
					err      error
				}
				first, second := make(chan result, 1), make(chan result, 1)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				go func() {
					response, err := agent.Prompt(ctx, acp.PromptRequest{SessionId: "thread-1"})
					first <- result{response, err}
				}()
				<-starts
				synctest.Wait()
				cancel()
				synctest.Wait()
				if lateStart {
					if len(first) != 1 {
						t.Fatal("cancel waited for an unacknowledged turn/start")
					}
				} else if len(first) != 0 {
					t.Fatal("cancel completed before the known turn's interrupt acknowledgement")
				}
				go func() {
					response, err := agent.Prompt(context.Background(), acp.PromptRequest{SessionId: "thread-1"})
					second <- result{response, err}
				}()
				synctest.Wait()
				if len(starts) != 0 {
					t.Fatal("next turn started while cancellation cleanup was pending")
				}
				if lateStart {
					close(startRelease)
					synctest.Wait()
				}
				if len(interrupts) != 1 || len(starts) != 0 {
					t.Fatalf("expected one interrupt before the next start: interrupts=%d starts=%d", len(interrupts), len(starts))
				}
				close(interruptRelease)
				synctest.Wait()
				if result := <-first; result.err != nil || result.response.StopReason != acp.StopReasonCancelled {
					t.Fatalf("cancelled prompt = %+v", result)
				}
				if result := <-second; result.err != nil || result.response.StopReason != acp.StopReasonEndTurn {
					t.Fatalf("next prompt = %+v", result)
				}
			})
		})
	}
}

func TestUnacknowledgedTurnStartRetiresBackend(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		session, client := faultBackend(t, func(net.Conn, rpcMessage) {})
		_, _, err := session.runTurn(context.Background(), nil, client, acp.ClientCapabilities{}, nil, nil)
		if err == nil {
			t.Fatal("unacknowledged turn/start succeeded")
		}
		select {
		case <-client.rpc.done:
		default:
			t.Fatal("backend with an unidentified, possibly active turn was reused")
		}
	})
}

func TestUnacknowledgedInterruptIsBoundedAndRetiresBackend(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reader, input := io.Pipe()
		defer reader.Close()
		defer input.Close()
		rpc := newRPCClient(io.Discard, reader)
		client := newCodexClient(rpc)
		rpc.start()
		started := time.Now()
		interruptTurnSoon(client, "thread", "turn")
		if elapsed := time.Since(started); elapsed != 2*time.Second {
			t.Fatalf("interrupt timeout took %s", elapsed)
		}
		select {
		case <-rpc.done:
		default:
			t.Fatal("unacknowledged interrupt left its backend reusable")
		}
	})
}
