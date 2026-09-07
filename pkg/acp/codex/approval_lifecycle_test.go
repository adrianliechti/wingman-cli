package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
)

type delayedApprovalClient struct {
	liveClient
	entered chan context.Context
	release chan struct{}
}

type observeClientWrites func([]byte) (int, error)

func (w observeClientWrites) Write(p []byte) (int, error) { return w(p) }

func (c *delayedApprovalClient) RequestPermission(ctx context.Context, request acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	c.entered <- ctx
	<-c.release // Deliberately return an approval after cancellation.
	return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
		Selected: &acp.RequestPermissionOutcomeSelected{OptionId: optionAllowOnce},
	}}, nil
}

func TestCancelAndSteerDuringApprovalAllowNextPrompt(t *testing.T) {
	agentIO, clientIO := net.Pipe()
	appIO, backendIO := net.Pipe()
	viewer := &delayedApprovalClient{entered: make(chan context.Context, 1), release: make(chan struct{})}
	releaseApproval := sync.OnceFunc(func() { close(viewer.release) })
	t.Cleanup(releaseApproval)
	t.Cleanup(func() {
		_ = agentIO.Close()
		_ = clientIO.Close()
		_ = appIO.Close()
		_ = backendIO.Close()
	})
	rpc := newRPCClient(appIO, appIO)
	client := newCodexClient(rpc)
	a := newAgent(client, "default", "")
	a.sessions["thread-1"] = newSession("thread-1", "default", "", nil)
	var wireMu sync.Mutex
	var frames []rpcMessage
	writer := observeClientWrites(func(data []byte) (int, error) {
		var frame rpcMessage
		_ = json.Unmarshal(data, &frame)
		wireMu.Lock()
		frames = append(frames, frame)
		wireMu.Unlock()
		return agentIO.Write(data)
	})
	a.SetAgentConnection(acp.NewAgentSideConnection(a, writer, agentIO))
	_ = acp.NewClientSideConnection(viewer, clientIO, clientIO)
	rpc.start()
	approval := make(chan execApprovalResponse, 1)
	go func() {
		scanner := bufio.NewScanner(backendIO)
		starts := 0
		for scanner.Scan() {
			var message rpcMessage
			_ = json.Unmarshal(scanner.Bytes(), &message)
			switch message.Method {
			case "turn/start":
				starts++
				if starts == 1 {
					writeRPCMessage(backendIO, rpcMessage{ID: message.ID, Result: json.RawMessage(`{"turn":{"id":"first","status":"inProgress"}}`)})
					writeRPCMessage(backendIO, rpcMessage{Method: "item/started", Params: json.RawMessage(`{"threadId":"thread-1","turnId":"first","item":{"id":"command","type":"commandExecution","command":"echo hi","status":"inProgress"}}`)})
					writeRPCMessage(backendIO, rpcMessage{ID: json.RawMessage(`100`), Method: "item/commandExecution/requestApproval", Params: json.RawMessage(`{"threadId":"thread-1","turnId":"first","itemId":"command","command":"echo hi"}`)})
				} else {
					writeRPCMessage(backendIO, rpcMessage{ID: message.ID, Result: json.RawMessage(`{"turn":{"id":"second","status":"inProgress"}}`)})
					writeRPCMessage(backendIO, rpcMessage{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread-1","turn":{"id":"first","status":"interrupted"}}`)})
					writeRPCMessage(backendIO, rpcMessage{Method: "item/agentMessage/delta", Params: json.RawMessage(`{"threadId":"thread-1","turnId":"second","itemId":"answer","delta":"next answer"}`)})
					writeRPCMessage(backendIO, rpcMessage{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread-1","turn":{"id":"second","status":"completed"}}`)})
				}
			case "turn/steer":
				writeRPCMessage(backendIO, rpcMessage{ID: message.ID, Result: json.RawMessage(`{"turnId":"first"}`)})
			case "turn/interrupt":
				writeRPCMessage(backendIO, rpcMessage{ID: message.ID, Result: json.RawMessage(`{}`)})
			case "":
				if string(message.ID) == "100" {
					var response execApprovalResponse
					_ = json.Unmarshal(message.Result, &response)
					approval <- response
				}
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	first := make(chan acp.PromptResponse, 1)
	go func() {
		response, _ := a.Prompt(ctx, acp.PromptRequest{SessionId: "thread-1", Prompt: []acp.ContentBlock{acp.TextBlock("first prompt")}})
		first <- response
	}()
	var permissionCtx context.Context
	select {
	case permissionCtx = <-viewer.entered:
	case <-ctx.Done():
		t.Fatal("approval was not requested")
	}
	wireMu.Lock()
	toolIndex, permissionIndex := -1, -1
	for index, frame := range frames {
		if frame.Method == "session/update" && strings.Contains(string(frame.Params), `"sessionUpdate":"tool_call"`) {
			toolIndex = index
		}
		if frame.Method == "session/request_permission" {
			permissionIndex = index
		}
	}
	wireMu.Unlock()
	if toolIndex < 0 || permissionIndex <= toolIndex {
		t.Fatalf("permission preceded its tool-start update: tool=%d permission=%d", toolIndex, permissionIndex)
	}
	if err := a.Steer(ctx, "thread-1", []acp.ContentBlock{acp.TextBlock("also check tests")}, "steer-1"); err != nil {
		t.Fatal(err)
	}
	if permissionCtx.Err() != nil {
		t.Fatal("steering cancelled the open approval")
	}
	if err := a.Cancel(ctx, acp.CancelNotification{SessionId: "thread-1"}); err != nil {
		t.Fatal(err)
	}
	select {
	case response := <-first:
		if response.StopReason != acp.StopReasonCancelled {
			t.Fatalf("cancel = %+v", response)
		}
	case <-ctx.Done():
		t.Fatal("prompt waited for the unanswered approval")
	}
	releaseApproval()
	select {
	case response := <-approval:
		if response.Decision != "cancel" {
			t.Fatalf("late approval was accepted: %+v", response)
		}
	case <-ctx.Done():
		t.Fatal("backend approval request was not settled")
	}
	response, err := a.Prompt(ctx, acp.PromptRequest{SessionId: "thread-1", Prompt: []acp.ContentBlock{acp.TextBlock("next prompt")}})
	if err != nil || response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("next prompt = %+v, %v", response, err)
	}
	text, _ := viewer.snapshot()
	// SessionUpdate is sent before the prompt returns, but the client's handler
	// runs asynchronously when calling Agent.Prompt directly.
	for !strings.Contains(text, "next answer") && ctx.Err() == nil {
		time.Sleep(time.Millisecond)
		text, _ = viewer.snapshot()
	}
	if !strings.Contains(text, "next answer") {
		t.Fatalf("follow-up answer = %q", text)
	}
}

func TestCancelledQueuedPromptDoesNotWaitForActivePrompt(t *testing.T) {
	a := newAgent(&codexClient{}, "default", "")
	s := newSession("thread", "default", "", nil)
	a.sessions[s.id] = s
	s.promptGate <- struct{}{}
	defer func() { <-s.promptGate }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan acp.PromptResponse, 1)
	go func() {
		response, _ := a.Prompt(ctx, acp.PromptRequest{SessionId: s.id, MessageId: new("queued")})
		done <- response
	}()
	select {
	case response := <-done:
		if response.StopReason != acp.StopReasonCancelled || response.UserMessageId == nil || *response.UserMessageId != "queued" {
			t.Fatalf("queued prompt = %+v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled queued prompt waited for the active prompt")
	}
}

type planReviewClient struct {
	liveClient
	block      bool
	requested  chan struct{}
	configured chan struct{}
}

func (c *planReviewClient) RequestPermission(ctx context.Context, p acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	close(c.requested)
	if c.block {
		<-ctx.Done()
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}}, nil
	}
	return c.liveClient.RequestPermission(ctx, p)
}

func (c *planReviewClient) SessionUpdate(ctx context.Context, n acp.SessionNotification) error {
	if n.Update.ConfigOptionUpdate != nil {
		c.configured <- struct{}{}
	}
	return c.liveClient.SessionUpdate(ctx, n)
}

func TestPlanApprovalStartsANewTurnAndHonorsCancellation(t *testing.T) {
	for _, cancelPlan := range []bool{false, true} {
		name := "approve"
		if cancelPlan {
			name = "cancel"
		}
		t.Run(name, func(t *testing.T) {
			agentIO, viewerIO := net.Pipe()
			appIO, backendIO := net.Pipe()
			t.Cleanup(func() {
				_ = agentIO.Close()
				_ = viewerIO.Close()
				_ = appIO.Close()
				_ = backendIO.Close()
			})
			viewer := &planReviewClient{block: cancelPlan, requested: make(chan struct{}), configured: make(chan struct{}, 1)}
			conn := acp.NewAgentSideConnection(nil, agentIO, agentIO)
			_ = acp.NewClientSideConnection(viewer, viewerIO, viewerIO)
			rpc := newRPCClient(appIO, appIO)
			client := newCodexClient(rpc)
			rpc.start()
			starts := make(chan struct{}, 2)
			go func() {
				scanner := bufio.NewScanner(backendIO)
				for scanner.Scan() {
					var request rpcMessage
					_ = json.Unmarshal(scanner.Bytes(), &request)
					switch request.Method {
					case "turn/start":
						starts <- struct{}{}
						if len(starts) == 1 {
							writeRPCMessage(backendIO, rpcMessage{ID: request.ID, Result: json.RawMessage(`{"turn":{"id":"plan","status":"inProgress"}}`)})
							writeRPCMessage(backendIO, rpcMessage{Method: "item/completed", Params: json.RawMessage(`{"threadId":"thread-1","turnId":"plan","item":{"id":"plan-item","type":"plan","text":"Check the code."}}`)})
							writeRPCMessage(backendIO, rpcMessage{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread-1","turn":{"id":"plan","status":"completed"}}`)})
						} else {
							writeRPCMessage(backendIO, rpcMessage{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread-1","turn":{"id":"plan","status":"completed"}}`)})
							replyTurnStarted(backendIO, request)
							writeRPCMessage(backendIO, rpcMessage{Method: "item/agentMessage/delta", Params: json.RawMessage(`{"threadId":"thread-1","turnId":"new-turn","itemId":"answer","delta":"implemented"}`)})
							writeRPCMessage(backendIO, rpcMessage{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread-1","turn":{"id":"new-turn","status":"completed"}}`)})
						}
					case "thread/settings/update":
						writeRPCMessage(backendIO, rpcMessage{ID: request.ID, Result: json.RawMessage(`{}`)})
					}
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			s := newSession("thread-1", "default", "", nil)
			s.collaborationMode = planCollaborationMode
			type result struct {
				stop acp.StopReason
				err  error
			}
			done := make(chan result, 1)
			go func() {
				stop, _, err := s.runTurn(ctx, conn, client, acp.ClientCapabilities{}, nil, []acp.ContentBlock{acp.TextBlock("plan")})
				done <- result{stop, err}
			}()
			select {
			case <-viewer.requested:
			case <-ctx.Done():
				t.Fatal("plan approval was not requested")
			}
			want, wantStarts := acp.StopReasonEndTurn, 2
			if cancelPlan {
				cancel()
				want, wantStarts = acp.StopReasonCancelled, 1
			}
			select {
			case got := <-done:
				if got.err != nil || got.stop != want || len(starts) != wantStarts {
					t.Fatalf("plan result = %+v, started turns = %d", got, len(starts))
				}
			case <-time.After(time.Second):
				t.Fatal("plan approval did not settle")
			}
			if !cancelPlan {
				select {
				case <-viewer.configured:
				case <-time.After(time.Second):
					t.Fatal("implementation mode update was lost after the plan turn ended")
				}
			}
		})
	}
}
