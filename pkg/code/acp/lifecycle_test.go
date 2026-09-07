package acp

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	acpsdk "github.com/coder/acp-go-sdk"
)

type lifecyclePeer struct {
	adapterProtocolAgent
	conn       *acpsdk.AgentSideConnection
	cancelled  chan struct{}
	cancelSeen atomic.Int32
}

func (p *lifecyclePeer) Cancel(context.Context, acpsdk.CancelNotification) error {
	if p.cancelSeen.Add(1) == 1 && p.cancelled != nil {
		close(p.cancelled)
	}
	return nil
}

func newLifecycleClient(t *testing.T, peer *lifecyclePeer) *Agent {
	t.Helper()
	a, err := NewInProcess(context.Background(), &code.Workspace{RootPath: t.TempDir()}, "test", peer,
		func(conn *acpsdk.AgentSideConnection) { peer.conn = conn }, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestCancelledTurnDrainsLateUpdatesBeforeNextPrompt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		peer := &lifecyclePeer{cancelled: make(chan struct{})}
		started, finish := make(chan struct{}), make(chan struct{})
		peer.promptFn = func(ctx context.Context, p acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
			if p.Prompt[0].Text.Text == "first" {
				close(started)
				<-peer.cancelled
				_ = peer.conn.SessionUpdate(context.WithoutCancel(ctx), acpsdk.SessionNotification{SessionId: p.SessionId, Update: acpsdk.UpdateAgentMessageText("stale")})
				<-finish
				return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonCancelled}, nil
			}
			_ = peer.conn.SessionUpdate(ctx, acpsdk.SessionNotification{SessionId: p.SessionId, Update: acpsdk.UpdateAgentMessageText("fresh")})
			return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
		}
		a := newLifecycleClient(t, peer)
		id, err := a.NewSession(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		stream, err := a.Send(ctx, id, []agent.Content{{Text: "first"}})
		if err != nil {
			t.Fatal(err)
		}
		completed := make(chan error, 1)
		go func() {
			var last error
			for _, err := range stream {
				last = err
			}
			completed <- last
		}()
		<-started
		cancel()
		<-peer.cancelled
		synctest.Wait()
		if _, err := a.Send(context.Background(), id, []agent.Content{{Text: "too soon"}}); !errors.Is(err, code.ErrTurnInProgress) {
			t.Fatalf("next prompt started before cancellation drained: %v", err)
		}
		close(finish)
		if err := <-completed; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel = %v", err)
		}
		next, err := a.Send(context.Background(), id, []agent.Content{{Text: "second"}})
		if err != nil {
			t.Fatal(err)
		}
		var text string
		for msg, err := range next {
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range msg.Content {
				text += c.Text
			}
		}
		if text != "fresh" {
			t.Fatalf("next turn received %q", text)
		}
		for _, m := range a.Messages(id) {
			for _, c := range m.Content {
				if strings.Contains(c.Text, "stale") {
					t.Fatal("late cancelled output entered history")
				}
			}
		}
		if peer.cancelSeen.Load() != 1 {
			t.Fatalf("cancel count = %d", peer.cancelSeen.Load())
		}
	})
}

func TestUnreadTurnCannotBlockAnotherSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		peer := &lifecyclePeer{}
		flooded := make(chan struct{})
		peer.promptFn = func(ctx context.Context, p acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
			if p.Prompt[0].Text.Text == "flood" {
				defer close(flooded)
				for range 600 {
					if err := peer.conn.SessionUpdate(context.WithoutCancel(ctx), acpsdk.SessionNotification{SessionId: p.SessionId, Update: acpsdk.UpdateAgentMessageText("x")}); err != nil {
						return acpsdk.PromptResponse{}, err
					}
				}
			} else {
				_ = peer.conn.SessionUpdate(ctx, acpsdk.SessionNotification{SessionId: p.SessionId, Update: acpsdk.UpdateAgentMessageText("ok")})
			}
			return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
		}
		a := newLifecycleClient(t, peer)
		id, err := a.NewSession(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		flood, err := a.Send(context.Background(), id, []agent.Content{{Text: "flood"}})
		if err != nil {
			t.Fatal(err)
		}
		<-flooded
		synctest.Wait()
		other, err := a.NewSession(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		stream, err := a.Send(context.Background(), other, []agent.Content{{Text: "other"}})
		if err != nil {
			t.Fatal(err)
		}
		var text string
		for msg, err := range stream {
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range msg.Content {
				text += c.Text
			}
		}
		if text != "ok" {
			t.Fatalf("other session = %q", text)
		}
		var failure error
		for _, err := range flood {
			if err != nil {
				failure = err
			}
		}
		if failure == nil || !strings.Contains(failure.Error(), "buffer exhausted") {
			t.Fatalf("overflow = %v", failure)
		}
	})
}

func TestCancelledInteractionsDiscardLateAnswersAndStaySessionScoped(t *testing.T) {
	for _, form := range []bool{false, true} {
		name := "permission"
		if form {
			name = "form"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				otherCtx, cancelOther := context.WithCancel(context.Background())
				defer cancelOther()
				a := &Agent{sessions: map[string]*sessionState{
					"a": {inflight: &turn{ctx: ctx, cancel: cancel}},
					"b": {inflight: &turn{ctx: otherCtx, cancel: cancelOther}},
				}}
				entered, release := make(chan context.Context, 1), make(chan struct{})
				var confirms atomic.Int32
				a.SetUI(&fakeUI{
					elicitContext: func(ctx context.Context, _ tool.ElicitRequest) (tool.ElicitResult, error) {
						entered <- ctx
						<-release
						return tool.ElicitResult{Action: tool.ElicitAccept, Content: map[string]any{"choice": "Allow"}}, nil
					},
					confirm: func(string) (bool, error) { confirms.Add(1); return true, nil },
				})
				result := make(chan bool, 1)
				go func() {
					if form {
						req := acpsdk.NewUnstableCreateElicitationRequestForm(acpsdk.UnstableElicitationSchema{Type: "object", Properties: map[string]any{}})
						req.Form.Meta = map[string]any{"sessionId": "a"}
						r, _ := a.UnstableCreateElicitation(context.Background(), req)
						result <- r.Cancel != nil
					} else {
						r, _ := a.RequestPermission(context.Background(), acpsdk.RequestPermissionRequest{SessionId: "a", Options: []acpsdk.PermissionOption{{OptionId: "allow", Name: "Allow", Kind: acpsdk.PermissionOptionKindAllowOnce}}})
						result <- r.Outcome.Cancelled != nil
					}
				}()
				uiCtx := <-entered
				a.Cancel("a")
				if !<-result {
					t.Error("cancelled interaction accepted a late response")
				}
				if uiCtx.Err() == nil {
					t.Error("dialog did not inherit turn cancellation")
				}
				if otherCtx.Err() != nil {
					t.Error("cancelled another session")
				}
				close(release)
				synctest.Wait()
				if confirms.Load() != 0 {
					t.Error("cancellation opened a second confirmation dialog")
				}
			})
		})
	}
}

func TestPreparedLoadsDoNotReplayHistoryTwice(t *testing.T) {
	peer := &adapterProtocolAgent{caps: acpsdk.AgentCapabilities{LoadSession: true}}
	a := newAdapterForTest(t, peer)
	first := a.LoadSessionStream(context.Background(), "saved")
	second := a.LoadSessionStream(context.Background(), "saved")
	for _, err := range first {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, err := range second {
		if err != nil {
			t.Fatal(err)
		}
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if peer.loadCalls != 1 {
		t.Fatalf("history replayed %d times", peer.loadCalls)
	}
}

func TestSessionUpdateCallbackCanIssueACPRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		peer := &lifecyclePeer{}
		peer.caps.SessionCapabilities.List = &acpsdk.SessionListCapabilities{}
		peer.promptFn = func(ctx context.Context, req acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
			title := "Updated"
			err := peer.conn.SessionUpdate(ctx, acpsdk.SessionNotification{SessionId: req.SessionId, Update: acpsdk.SessionUpdate{
				SessionInfoUpdate: &acpsdk.SessionSessionInfoUpdate{SessionUpdate: "session_info_update", Title: &title},
			}})
			return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, err
		}
		a := newLifecycleClient(t, peer)
		id, err := a.NewSession(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		called := make(chan error, 1)
		a.SetSessionUpdateHandler(func(string) { _, err := a.ListSessions(context.Background()); called <- err })
		stream, err := a.Send(context.Background(), id, []agent.Content{{Text: "go"}})
		if err != nil {
			t.Fatal(err)
		}
		for _, err := range stream {
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := <-called; err != nil {
			t.Fatalf("callback request = %v", err)
		}
	})
}

func TestUnsettledCancellationRetiresConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		peer := &lifecyclePeer{cancelled: make(chan struct{})}
		started, release := make(chan struct{}), make(chan struct{})
		defer close(release)
		peer.promptFn = func(context.Context, acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
			close(started)
			<-release
			return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
		}
		a := newLifecycleClient(t, peer)
		id, err := a.NewSession(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		stream, err := a.Send(ctx, id, []agent.Content{{Text: "unresponsive"}})
		if err != nil {
			t.Fatal(err)
		}
		<-started
		cancel()
		var failure error
		for _, err := range stream {
			if err != nil {
				failure = err
			}
		}
		if !errors.Is(failure, context.Canceled) {
			t.Fatalf("cancelled stream = %v", failure)
		}
		<-a.conn.Done()
		if _, err := a.NewSession(context.Background()); err == nil {
			t.Fatal("unsettled connection accepted a new session")
		}
	})
}

type zeroVersionPeer struct{ adapterProtocolAgent }

func (*zeroVersionPeer) Initialize(context.Context, acpsdk.InitializeRequest) (acpsdk.InitializeResponse, error) {
	return acpsdk.InitializeResponse{ProtocolVersion: 0}, nil
}

func TestInitializeRejectsUnimplementedOlderMajorVersion(t *testing.T) {
	a, err := NewInProcess(context.Background(), &code.Workspace{RootPath: t.TempDir()}, "old", &zeroVersionPeer{}, nil, nil)
	if a != nil {
		_ = a.Close()
	}
	if err == nil {
		t.Fatal("accepted an unsupported older major protocol version")
	}
}

func TestInvalidElicitationScopeDoesNotFallBackToAnotherSession(t *testing.T) {
	a := &Agent{sessions: map[string]*sessionState{"active": {inflight: &turn{ctx: context.Background()}}}}
	var calls atomic.Int32
	a.SetUI(&fakeUI{elicit: func(tool.ElicitRequest) (tool.ElicitResult, error) {
		calls.Add(1)
		return tool.ElicitResult{Action: tool.ElicitAccept}, nil
	}})
	req := acpsdk.NewUnstableCreateElicitationRequestForm(acpsdk.UnstableElicitationSchema{Type: "object", Properties: map[string]any{}})
	req.Form.Meta = map[string]any{"sessionId": "deleted"}
	r, err := a.UnstableCreateElicitation(context.Background(), req)
	if err != nil || r.Cancel == nil || calls.Load() != 0 {
		t.Fatalf("invalid session form = %#v, %v; UI calls=%d", r, err, calls.Load())
	}
}

func TestReasoningDeltasDoNotShareMutableHistory(t *testing.T) {
	a := &Agent{}
	sess, turn := &sessionState{}, &turn{}
	update := func(text string) acpsdk.SessionUpdate {
		return acpsdk.SessionUpdate{AgentThoughtChunk: &acpsdk.SessionUpdateAgentThoughtChunk{Content: acpsdk.TextBlock(text)}}
	}
	first, ok := a.translateUpdate(sess, turn, update("first"))
	if !ok {
		t.Fatal("first reasoning chunk was dropped")
	}
	_, _ = a.translateUpdate(sess, turn, update(" second"))
	if got := first.Content[0].Reasoning.Summary; got != "first" {
		t.Fatalf("previously emitted delta changed to %q", got)
	}
	first.Content[0].Reasoning.Summary = "client-owned"
	if got := turn.messages()[0].Content[0].Reasoning.Summary; got != "first second" {
		t.Fatalf("client mutation changed history to %q", got)
	}
}

func TestMalformedPromptStopReasonIsNotSuccess(t *testing.T) {
	for _, reason := range []acpsdk.StopReason{"", "unknown"} {
		t.Run(string(reason), func(t *testing.T) {
			peer := &adapterProtocolAgent{promptFn: func(context.Context, acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
				return acpsdk.PromptResponse{StopReason: reason}, nil
			}}
			a := newAdapterForTest(t, peer)
			id, err := a.NewSession(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			stream, err := a.Send(context.Background(), id, []agent.Content{{Text: "go"}})
			if err != nil {
				t.Fatal(err)
			}
			var failure error
			for _, err := range stream {
				if err != nil {
					failure = err
				}
			}
			if failure == nil {
				t.Fatalf("invalid stop reason %q was reported as success", reason)
			}
		})
	}
}
