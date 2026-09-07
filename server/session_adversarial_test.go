package server

import (
	"context"
	"iter"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/code"
)

type lifecycleAgent struct {
	*protocolAgent
	cancelled         chan struct{}
	release           chan struct{}
	deleted           atomic.Bool
	writesAfterDelete atomic.Int32
	load              func(context.Context, string) error
	list              func(context.Context) ([]code.SessionInfo, error)
	closeAgent        func() error
	setModel          func(context.Context, string, string) error
}

func (a *lifecycleAgent) Send(ctx context.Context, _ string, _ []agent.Content) (iter.Seq2[agent.Message, error], error) {
	return func(yield func(agent.Message, error) bool) {
		yield(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{Text: "working"}}}, nil)
		<-ctx.Done()
		close(a.cancelled)
		<-a.release
		yield(agent.Message{}, ctx.Err())
	}, nil
}
func (a *lifecycleAgent) DeleteSession(context.Context, string) error {
	a.deleted.Store(true)
	return nil
}
func (a *lifecycleAgent) SaveTurnQueue(id string, state code.TurnQueueState) error {
	if a.deleted.Load() {
		a.writesAfterDelete.Add(1)
	}
	return a.protocolAgent.SaveTurnQueue(id, state)
}
func (a *lifecycleAgent) LoadSession(ctx context.Context, id string) error {
	if a.load != nil {
		return a.load(ctx, id)
	}
	return a.protocolAgent.LoadSession(ctx, id)
}
func (a *lifecycleAgent) ListSessions(ctx context.Context) ([]code.SessionInfo, error) {
	if a.list != nil {
		return a.list(ctx)
	}
	return a.protocolAgent.ListSessions(ctx)
}
func (a *lifecycleAgent) Close() error {
	if a.closeAgent != nil {
		return a.closeAgent()
	}
	return nil
}
func (a *lifecycleAgent) SetModel(ctx context.Context, id, model string) error {
	if a.setModel != nil {
		return a.setModel(ctx, id, model)
	}
	return a.protocolAgent.SetModel(ctx, id, model)
}

func installProtocolAgent(s *Server, a code.Agent) *backendRuntime {
	s.runtimes["one"].turns.Close()
	b := s.bindBackend("one", a)
	s.runtimes["one"] = b
	return b
}

func TestSessionDeleteWaitsForFinalQueueWrite(t *testing.T) {
	s, _, base := newProtocolServer(t)
	a := &lifecycleAgent{protocolAgent: base, cancelled: make(chan struct{}), release: make(chan struct{})}
	b := installProtocolAgent(s, a)
	c := b.session("saved")
	c.load()
	path := "/api/v2/backends/one/sessions/saved/commands"
	if rec := protocolRequest(t, s, path, Command{ID: "send", Epoch: c.epoch, Type: "send", Text: "hello"}); rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	waitProtocol(t, func() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.state.Phase == "streaming" })
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- protocolRequest(t, s, path, Command{ID: "delete", Epoch: c.epoch, Type: "delete"}) }()
	<-a.cancelled
	var rec *httptest.ResponseRecorder
	select {
	case rec = <-done:
		t.Error("deletion completed before the old turn stopped writing")
	case <-time.After(20 * time.Millisecond):
	}
	close(a.release)
	if rec == nil {
		select {
		case rec = <-done:
		case <-time.After(time.Second):
			t.Fatal("deletion did not finish")
		}
	}
	if rec.Code != 200 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	b.turns.Close()
	if a.writesAfterDelete.Load() != 0 {
		t.Fatal("terminal queue write recreated deleted session storage")
	}
}

func TestServerCloseJoinsSessionLoadsBeforeClosingAgent(t *testing.T) {
	s, _, base := newProtocolServer(t)
	started, cancelled, release, loaded := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var premature atomic.Bool
	a := &lifecycleAgent{protocolAgent: base}
	a.load = func(ctx context.Context, _ string) error {
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-release
		close(loaded)
		return ctx.Err()
	}
	a.closeAgent = func() error {
		select {
		case <-loaded:
		default:
			premature.Store(true)
		}
		return nil
	}
	b := installProtocolAgent(s, a)
	go b.session("saved").load()
	<-started
	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	<-cancelled
	select {
	case <-closed:
		t.Error("server closed while backend load still used its resources")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("server did not close")
	}
	<-loaded
	if premature.Load() {
		t.Fatal("agent closed before LoadSession returned")
	}
}

func TestSessionRequestIDIsSharedByPromptsAndInFlightCommands(t *testing.T) {
	s, _, base := newProtocolServer(t)
	a := &lifecycleAgent{protocolAgent: base}
	b := installProtocolAgent(s, a)
	c := b.session("saved")
	c.load()
	a.setModel = func(ctx context.Context, _, _ string) error { _, err := b.Confirm(ctx, "Change model?"); return err }
	path := "/api/v2/backends/one/sessions/saved/commands"
	model := "new-model"
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- protocolRequest(t, s, path, Command{ID: "shared", Epoch: c.epoch, Type: "settings", Model: &model})
	}()
	waitProtocol(t, func() bool { c.mu.Lock(); defer c.mu.Unlock(); return len(c.prompts) == 1 })
	c.mu.Lock()
	prompt := c.state.Prompts[0].ID
	c.mu.Unlock()
	answer := Command{ID: "shared", Epoch: c.epoch, Type: "prompt_response", PromptID: prompt, Action: "accept"}
	if rec := protocolRequest(t, s, path, answer); rec.Code != 409 {
		t.Errorf("request ID reused across command types: %d", rec.Code)
	}
	answer.ID = "answer"
	if rec := protocolRequest(t, s, path, answer); rec.Code != 200 {
		t.Errorf("correlated prompt response: %d %s", rec.Code, rec.Body)
	}
	select {
	case rec := <-done:
		if rec.Code != 200 {
			t.Fatal(rec.Body)
		}
	case <-time.After(time.Second):
		t.Fatal("prompt deadlocked with settings")
	}
	// A completed prompt receipt must also reserve its ID against ordinary commands.
	if rec := protocolRequest(t, s, path, Command{ID: "answer", Epoch: c.epoch, Type: "queue_clear"}); rec.Code != 409 {
		t.Errorf("completed prompt ID reused: %d", rec.Code)
	}
}

func TestBackendReadsCancelWithEitherRequestOrWorkspace(t *testing.T) {
	for _, source := range []string{"workspace", "request"} {
		t.Run(source, func(t *testing.T) {
			s, _, base := newProtocolServer(t)
			started, cancelled := make(chan struct{}), make(chan struct{})
			a := &lifecycleAgent{protocolAgent: base}
			a.list = func(ctx context.Context) ([]code.SessionInfo, error) {
				close(started)
				<-ctx.Done()
				close(cancelled)
				return nil, ctx.Err()
			}
			installProtocolAgent(s, a)
			ctx, cancelRequest := context.WithCancel(context.Background())
			defer cancelRequest()
			req := httptest.NewRequest("GET", "/api/v2/backends/one/sessions", nil).WithContext(ctx)
			req.Header.Set(instanceHeader, s.scope.InstanceID)
			done := make(chan struct{})
			go func() { s.ServeHTTP(httptest.NewRecorder(), req); close(done) }()
			<-started
			closed := make(chan struct{})
			if source == "workspace" {
				go func() { s.Close(); close(closed) }()
			} else {
				cancelRequest()
				close(closed)
			}
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Error("backend read did not observe " + source + " cancellation")
				cancelRequest()
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("backend read did not return")
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("workspace shutdown did not finish")
			}
		})
	}
}

func TestSessionMultipleSubscriptionsOnOneSocketAreIndependent(t *testing.T) {
	_, b, _ := newProtocolServer(t)
	c, client := b.session("saved"), newWSClient(nil)
	c.subscribe(client, "first")
	_ = receiveEvent(t, client)
	c.subscribe(client, "second")
	_ = receiveEvent(t, client)
	c.unsubscribe(client, "second")
	c.apply(Frame{Type: EvtTextDelta, ID: "text", Text: "hello"})
	event := receiveEvent(t, client)
	if event.SubscriptionID != "first" || event.Type != "session.update" {
		t.Fatalf("remaining subscription: %+v", event)
	}
}
