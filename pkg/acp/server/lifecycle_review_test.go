package server

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	acpcommon "github.com/adrianliechti/wingman-agent/pkg/acp"
	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	codeagent "github.com/adrianliechti/wingman-agent/pkg/code/agent"
	acpsdk "github.com/coder/acp-go-sdk"
)

type scopedFormClient struct {
	recordingClient
	entered chan scopedFormCall
	release map[string]chan struct{}
}

type scopedFormCall struct {
	sid string
	ctx context.Context
}

func (c *scopedFormClient) UnstableCreateElicitation(ctx context.Context, req acpsdk.UnstableCreateElicitationRequest) (acpsdk.UnstableCreateElicitationResponse, error) {
	sid, _ := req.Form.Meta["sessionId"].(string)
	c.entered <- scopedFormCall{sid, ctx}
	if release := c.release[sid]; release != nil {
		<-release
	}
	return acpsdk.UnstableCreateElicitationResponse{Accept: &acpsdk.UnstableCreateElicitationAccept{Action: "accept", Content: map[string]any{"ok": true}}}, nil
}

func TestConcurrentFormsPreserveSessionScopeAndCancellation(t *testing.T) {
	t.Setenv("WINGMAN_ELICITATION", "")
	synctest.Test(t, func(t *testing.T) {
		serverIO, clientIO := net.Pipe()
		defer serverIO.Close()
		defer clientIO.Close()
		s := &Server{}
		s.formElicitation.Store(true)
		s.conn = acpsdk.NewAgentSideConnection(s, serverIO, serverIO)
		client := &scopedFormClient{entered: make(chan scopedFormCall, 2), release: map[string]chan struct{}{"a": make(chan struct{}), "b": make(chan struct{})}}
		_ = acpsdk.NewClientSideConnection(client, clientIO, clientIO)
		ctx, cancel := context.WithCancel(code.WithSessionID(context.Background(), "a"))
		defer cancel()
		first, second := make(chan tool.ElicitResult, 1), make(chan tool.ElicitResult, 1)
		req := tool.ElicitRequest{Message: "Continue?", Fields: []tool.ElicitField{{Name: "ok", Type: "boolean"}}}
		go func() { r, _ := s.Elicit(ctx, req); first <- r }()
		go func() { r, _ := s.Elicit(code.WithSessionID(context.Background(), "b"), req); second <- r }()
		calls := map[string]context.Context{}
		for range 2 {
			call := <-client.entered
			calls[call.sid] = call.ctx
		}
		if calls["a"] == nil || calls["b"] == nil {
			t.Fatalf("form session scope = %#v", calls)
		}
		cancel()
		if r := <-first; r.Action != tool.ElicitCancel {
			t.Fatalf("cancelled form = %#v", r)
		}
		synctest.Wait()
		if calls["b"].Err() != nil {
			t.Fatal("cancellation affected the other session")
		}
		close(client.release["a"])
		close(client.release["b"])
		if r := <-second; r.Action != tool.ElicitAccept {
			t.Fatalf("other form = %#v", r)
		}
	})
}

func TestConcurrentClosePreservesRetainedWorkspaceUntilPromptEnds(t *testing.T) {
	const id = acpsdk.SessionId("session")
	// One independent reference keeps the zero-resource workspace usable here.
	w := &workspaceEntry{refs: 2, key: "workspace"}
	s := &Server{sessions: map[acpsdk.SessionId]*sessionEntry{id: {id: id, workspace: w}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, finish, err := s.retainSession(ctx, id, cancel)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() { _, _ = s.CloseSession(context.Background(), acpsdk.CloseSessionRequest{SessionId: id}) })
	}
	wg.Wait()
	if ctx.Err() == nil {
		t.Fatal("close did not cancel the active prompt")
	}
	if w.refs != 2 {
		t.Fatalf("workspace prematurely released: refs=%d", w.refs)
	}
	if _, _, err := s.retainSession(context.Background(), id, func() {}); err == nil {
		t.Fatal("closing session accepted another prompt")
	}
	finish()
	s.releaseWorkspace(w)
	if w.refs != 1 || s.lookupSession(id) != nil {
		t.Fatal("prompt cleanup leaked a session or workspace reference")
	}
}

func TestCancelledPromptCannotAcquireIdleSession(t *testing.T) {
	w := &workspaceEntry{refs: 1}
	s := &Server{sessions: map[acpsdk.SessionId]*sessionEntry{"s": {workspace: w}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := s.retainSession(ctx, "s", func() {}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled prompt = %v", err)
	}
	if w.refs != 1 || s.sessions["s"].cancel != nil {
		t.Fatal("cancelled prompt changed session ownership")
	}
}

func TestClosingServerRejectsLateSessionRegistration(t *testing.T) {
	s := &Server{closing: true, sessions: map[acpsdk.SessionId]*sessionEntry{}}
	w := &workspaceEntry{agent: &codeagent.Agent{}, refs: 1}
	if err := s.registerSession("new", w); err == nil {
		t.Fatal("registered session after shutdown")
	}
	if err := s.replaceLoadedSession("old", w); err == nil {
		t.Fatal("attached loaded session after shutdown")
	}
	if len(s.sessions) != 0 || w.refs != 1 {
		t.Fatal("rejected registration changed ownership")
	}
}

func TestHistoryReplaySurfacesStalledClientWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		local, peer := net.Pipe()
		defer local.Close()
		defer peer.Close()
		writer := acpcommon.NewConnectionWriter(local, time.Second)
		defer writer.Close()
		s := &Server{}
		s.conn = acpsdk.NewAgentSideConnection(s, writer, local)
		err := s.replayMessages(context.Background(), "s", []agent.Message{{Role: agent.RoleAssistant, Content: []agent.Content{{Text: "history"}}}})
		if err == nil {
			t.Fatal("history replay reported success after a failed write")
		}
		if writer.Err() == nil {
			t.Fatal("stalled transport remained open")
		}
	})
}
