package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/adrianliechti/wingman-agent/pkg/model"
	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
)

type protocolAgent struct {
	code.Agent
	mu       sync.Mutex
	messages []agent.Message
	queue    code.TurnQueueState
	model    string
	failSave bool
	created  atomic.Int32
	sent     atomic.Int32
	loaded   atomic.Int32
	gate     chan struct{}
}

func (a *protocolAgent) Name() string { return "test" }
func (a *protocolAgent) Models(string) ([]model.Model, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return nil, a.model
}
func (a *protocolAgent) Effort(string) (string, []string)   { return "auto", []string{"auto", "high"} }
func (a *protocolAgent) Modes(string) ([]code.Mode, string) { return nil, "" }
func (a *protocolAgent) Usage(string) agent.Usage           { return agent.Usage{} }
func (a *protocolAgent) SetModel(_ context.Context, _, value string) error {
	a.mu.Lock()
	a.model = value
	a.mu.Unlock()
	return nil
}
func (a *protocolAgent) NewSession(context.Context) (string, error) {
	return fmt.Sprintf("session-%d", a.created.Add(1)), nil
}
func (a *protocolAgent) LoadSession(_ context.Context, id string) error {
	a.loaded.Add(1)
	if id == "missing" {
		return errors.New("session not found")
	}
	return nil
}
func (a *protocolAgent) DeleteSession(context.Context, string) error              { return nil }
func (a *protocolAgent) ListSessions(context.Context) ([]code.SessionInfo, error) { return nil, nil }
func (a *protocolAgent) Messages(string) []agent.Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]agent.Message(nil), a.messages...)
}
func (a *protocolAgent) LoadTurnQueue(string) (code.TurnQueueState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.queue, nil
}
func (a *protocolAgent) SaveTurnQueue(_ string, queue code.TurnQueueState) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failSave {
		return errors.New("disk full")
	}
	a.queue = queue
	return nil
}
func (a *protocolAgent) Cancel(string) {}
func (a *protocolAgent) Close() error  { return nil }
func (a *protocolAgent) Send(ctx context.Context, _ string, input []agent.Content) (iter.Seq2[agent.Message, error], error) {
	a.sent.Add(1)
	a.mu.Lock()
	a.messages = append(a.messages, agent.Message{Role: agent.RoleUser, Content: agent.CloneContent(input)})
	a.mu.Unlock()
	return func(yield func(agent.Message, error) bool) {
		if !yield(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{Text: "live prefix", TextID: "answer"}}}, nil) {
			return
		}
		if a.gate != nil {
			select {
			case <-a.gate:
			case <-ctx.Done():
				yield(agent.Message{}, ctx.Err())
				return
			}
		}
		a.mu.Lock()
		a.messages = append(a.messages, agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{Text: "live prefix", TextID: "answer"}}})
		a.mu.Unlock()
	}, nil
}

func newProtocolServer(t *testing.T) (*Server, *backendRuntime, *protocolAgent) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	s := &Server{ctx: ctx, cancel: cancel, scope: WorkspaceScope{"workspace", "instance"}, runtimes: map[string]*backendRuntime{}, wsConns: map[*websocket.Conn]*wsClient{}}
	a := &protocolAgent{}
	b := s.bindBackend("one", a)
	s.runtimes[b.id] = b
	s.mux = chi.NewRouter()
	s.mux.Route("/api", s.registerSessionRoutes)
	s.handler = s.checkInstance(s.mux)
	t.Cleanup(s.Close)
	return s, b, a
}
func protocolRequest(t *testing.T, s *Server, path string, command Command) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set(instanceHeader, s.scope.InstanceID)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}
func receiveEvent(t *testing.T, client *wsClient) sessionEvent {
	t.Helper()
	select {
	case data := <-client.outbox:
		var event sessionEvent
		if err := json.Unmarshal(data, &event); err != nil {
			t.Fatal(err)
		}
		return event
	case <-time.After(time.Second):
		t.Fatal("missing session event")
		return sessionEvent{}
	}
}
func waitProtocol(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestSessionSnapshotRetainsLiveTextAndHasTargetedSubscriptions(t *testing.T) {
	_, b, a := newProtocolServer(t)
	a.gate = make(chan struct{})
	c := b.session("saved")
	c.load()
	first := newWSClient(nil)
	other := newWSClient(nil)
	c.subscribe(first, "first")
	unrelated := b.session("unrelated")
	unrelated.subscribe(other, "other")
	_ = receiveEvent(t, other)
	_, err := b.turns.Submit(b.ctx, "saved", code.TurnInput{ID: "input", Content: []agent.Content{{Text: "question"}}})
	if err != nil {
		t.Fatal(err)
	}
	waitProtocol(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.entries) > 1 && c.entries[len(c.entries)-1].Content == "live prefix"
	})
	reconnect := newWSClient(nil)
	c.subscribe(reconnect, "reconnected")
	snapshot := receiveEvent(t, reconnect)
	if snapshot.Type != "session.snapshot" || snapshot.Entries[len(snapshot.Entries)-1].Content != "live prefix" {
		t.Fatalf("snapshot: %+v", snapshot)
	}
	if len(snapshot.State.PendingInputs) != 0 {
		t.Fatalf("active transcript input also appeared in queue: %+v", snapshot.State.PendingInputs)
	}
	if a.loaded.Load() != 1 {
		t.Fatalf("loads = %d", a.loaded.Load())
	}
	select {
	case data := <-other.outbox:
		t.Fatalf("unrelated subscriber received %s", data)
	default:
	}
	close(a.gate)
}

func TestSessionRestoresEditableQueueAndRejectsMissingHistory(t *testing.T) {
	s, b, a := newProtocolServer(t)
	a.queue = code.TurnQueueState{Inputs: []code.TurnInput{{ID: "saved-input", Intent: code.TurnInputFollowUp, Content: []agent.Content{{Text: "original"}, {Text: "[File: main.go]"}, {Text: "secret", Hidden: true}}}}}
	c := b.session("saved")
	c.load()
	client := newWSClient(nil)
	c.subscribe(client, "sub")
	snapshot := receiveEvent(t, client)
	if len(snapshot.State.PendingInputs) != 1 || snapshot.State.PendingInputs[0].ID != "saved-input" || snapshot.State.PendingInputs[0].Text != "original" || !snapshot.State.QueuePaused {
		t.Fatalf("restored queue: %+v", snapshot.State)
	}
	rec := protocolRequest(t, s, "/api/v2/backends/one/sessions/saved/commands", Command{ID: "edit", Epoch: c.epoch, Type: "queue_update", InputID: "saved-input", Text: "edited", Files: []string{"next.go"}})
	if rec.Code != 200 {
		t.Fatalf("edit: %d %s", rec.Code, rec.Body)
	}
	restored := b.turns.Snapshot("saved").Inputs[0].Input
	if restored.Display == nil || restored.Display.Text != "edited" || restored.Display.Files[0] != "next.go" {
		t.Fatalf("input: %+v", restored)
	}
	missing := b.session("missing")
	missing.load()
	missing.mu.Lock()
	defer missing.mu.Unlock()
	if missing.state.Status != "error" || missing.state.Error == nil {
		t.Fatalf("missing history was accepted: %+v", missing.state)
	}
}

func TestSessionReceiptsDeduplicateAfterCompletionAndCreation(t *testing.T) {
	s, b, a := newProtocolServer(t)
	var created Receipt
	for range 2 {
		rec := protocolRequest(t, s, "/api/v2/backends/one/sessions", Command{ID: "draft-request", Type: "create"})
		if rec.Code != 200 {
			t.Fatalf("create: %s", rec.Body)
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
			t.Fatal(err)
		}
	}
	if a.created.Load() != 1 {
		t.Fatalf("created %d sessions", a.created.Load())
	}
	path := "/api/v2/backends/one/sessions/" + created.Ref.SessionID + "/commands"
	command := Command{ID: "send-request", Epoch: created.Epoch, Type: "send", Text: "hello"}
	rec := protocolRequest(t, s, path, command)
	if rec.Code != 200 {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	waitProtocol(t, func() bool { return a.sent.Load() == 1 && !snapshotHasActive(b.turns.Snapshot(created.Ref.SessionID)) })
	rec = protocolRequest(t, s, path, command)
	if rec.Code != 200 || a.sent.Load() != 1 {
		t.Fatalf("retry executed again: %d sends %d", rec.Code, a.sent.Load())
	}
	command.Text = "changed"
	if rec := protocolRequest(t, s, path, command); rec.Code != 409 {
		t.Fatalf("changed request accepted: %d", rec.Code)
	}
	command.ID = "new"
	command.Epoch = "old-epoch"
	if rec := protocolRequest(t, s, path, command); rec.Code != 409 {
		t.Fatalf("old epoch accepted: %d", rec.Code)
	}
}

func TestSessionSnapshotsCannotBeOvertakenByUpdates(t *testing.T) {
	_, b, _ := newProtocolServer(t)
	c := b.session("saved")
	c.load()
	for range 50 {
		client := newWSClient(nil)
		var wg sync.WaitGroup
		wg.Go(func() { c.subscribe(client, "s") })
		wg.Go(func() { c.apply(Frame{Type: EvtTextDelta, ID: "a", Text: "x"}) })
		wg.Wait()
		event := receiveEvent(t, client)
		if event.Type != "session.snapshot" {
			t.Fatalf("first frame: %+v", event)
		}
		select {
		case data := <-client.outbox:
			var update sessionEvent
			_ = json.Unmarshal(data, &update)
			if update.PreviousRevision != event.Revision {
				t.Fatalf("gap: snapshot %d, update %d", event.Revision, update.PreviousRevision)
			}
		default:
		}
		c.unsubscribe(client, "s")
	}
}

func TestSessionPromptResponsesRemainAvailableDuringLoadAndHaveOneWinner(t *testing.T) {
	s, b, _ := newProtocolServer(t)
	c := b.session("saved")
	c.opMu.Lock() // A backend load/settings operation is waiting for UI.
	defer c.opMu.Unlock()
	ctx, cancel := context.WithCancel(code.WithSessionID(b.ctx, "saved"))
	defer cancel()
	done := make(chan bool, 1)
	go func() { approved, _ := b.Confirm(ctx, "Proceed?"); done <- approved }()
	waitProtocol(t, func() bool { c.mu.Lock(); defer c.mu.Unlock(); return len(c.prompts) == 1 })
	c.mu.Lock()
	id := c.state.Prompts[0].ID
	c.mu.Unlock()
	path := "/api/v2/backends/one/sessions/saved/commands"
	command := Command{ID: "answer", Epoch: c.epoch, Type: "prompt_response", PromptID: id, Action: "accept"}
	rec := protocolRequest(t, s, path, command)
	if rec.Code != 200 {
		t.Fatalf("response: %d %s", rec.Code, rec.Body)
	}
	select {
	case approved := <-done:
		if !approved {
			t.Fatal("not approved")
		}
	case <-time.After(time.Second):
		t.Fatal("prompt response blocked behind load")
	}
	command.ID = "other-client"
	command.Action = "decline"
	if rec := protocolRequest(t, s, path, command); rec.Code != 409 {
		t.Fatal("second response won")
	}
	client := newWSClient(nil)
	c.subscribe(client, "reconnect")
	snapshot := receiveEvent(t, client)
	if len(snapshot.State.Prompts) != 0 {
		t.Fatal("resolved prompt survived reconnect")
	}
}

func TestSessionBackendsOwnIdenticalNativeIDsAndDeletionIsTerminal(t *testing.T) {
	s, one, a := newProtocolServer(t)
	otherAgent := &protocolAgent{}
	two := s.bindBackend("two", otherAgent)
	s.runtimes["two"] = two
	c := one.session("same")
	c.load()
	other := two.session("same")
	other.load()
	if c.ref == other.ref || c.epoch == other.epoch {
		t.Fatal("session identity collided")
	}
	value := "model-b"
	rec := protocolRequest(t, s, "/api/v2/backends/two/sessions/same/commands", Command{ID: "settings", Epoch: other.epoch, Type: "settings", Model: &value})
	if rec.Code != 200 {
		t.Fatalf("settings: %s", rec.Body)
	}
	if _, model := a.Models("same"); model != "" {
		t.Fatal("settings crossed backends")
	}
	rec = protocolRequest(t, s, "/api/v2/backends/one/sessions/same/commands", Command{ID: "delete", Epoch: c.epoch, Type: "delete"})
	if rec.Code != 200 {
		t.Fatalf("delete: %s", rec.Body)
	}
	c.apply(Frame{Type: EvtTextDelta, Text: "late"})
	rec = protocolRequest(t, s, "/api/v2/backends/one/sessions/same/commands", Command{ID: "late", Epoch: c.epoch, Type: "send", Text: "late"})
	if rec.Code == 200 || a.sent.Load() != 0 {
		t.Fatal("deleted session accepted input")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.Status != "deleted" || len(c.entries) > 0 {
		t.Fatal("late callback resurrected deletion")
	}
}

func TestSessionQueuePersistenceFailureDoesNotAcknowledgeClearing(t *testing.T) {
	s, b, a := newProtocolServer(t)
	a.queue = code.TurnQueueState{Inputs: []code.TurnInput{{ID: "queued", Content: []agent.Content{{Text: "keep me"}}}}}
	c := b.session("saved")
	c.load()
	a.mu.Lock()
	a.failSave = true
	a.mu.Unlock()
	for _, kind := range []string{"queue_clear", "cancel"} {
		rec := protocolRequest(t, s, "/api/v2/backends/one/sessions/saved/commands", Command{ID: kind, Epoch: c.epoch, Type: kind, ClearQueue: true})
		if rec.Code != 409 {
			t.Fatalf("%s silently succeeded: %d", kind, rec.Code)
		}
	}
	if len(b.turns.Snapshot("saved").Inputs) != 1 {
		t.Fatal("failed persistence removed the input")
	}
}

func TestSessionWebSocketInstanceAndClose(t *testing.T) {
	s, b, _ := newProtocolServer(t)
	web := httptest.NewServer(s)
	defer web.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	endpoint := "ws" + web.URL[4:] + "/api/v2/events?instance="
	if conn, response, err := websocket.Dial(ctx, endpoint+"old", nil); err == nil {
		conn.CloseNow()
		t.Fatal("old workspace socket accepted")
	} else if response == nil || response.StatusCode != 409 {
		t.Fatalf("unexpected rejection: %v", err)
	}
	conn, _, err := websocket.Dial(ctx, endpoint+s.scope.InstanceID, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	data, _ := json.Marshal(subscriptionRequest{Type: "subscribe", SubscriptionID: "socket", Ref: b.session("saved").ref})
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
	_, data, err = conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot sessionEvent
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Type != "session.snapshot" {
		t.Fatalf("first frame: %s", data)
	}
	s.Close()
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			break
		}
	}
	if ctx.Err() != nil {
		t.Fatal("workspace close did not close the websocket")
	}
}

func TestSessionEntryIdentitySurvivesCommitAndLateSteering(t *testing.T) {
	_, b, a := newProtocolServer(t)
	c := b.session("saved")
	c.load()
	input := code.TurnInput{ID: "input", Content: []agent.Content{{Text: "guidance"}}}
	entry := turnQueueEntry(input, code.TurnInputSteered, 0)
	c.apply(Frame{Type: EvtTurnInput, Input: &entry})
	c.apply(Frame{Type: EvtTextDelta, ID: "text", Text: "answer"})
	c.mu.Lock()
	userID, textID := c.entries[0].ID, c.entries[1].ID
	c.mu.Unlock()
	a.mu.Lock()
	a.messages = []agent.Message{{Role: agent.RoleUser, InputID: "input", Content: input.Content}, {Role: agent.RoleAssistant, Content: []agent.Content{{Text: "answer", TextID: "text"}}}}
	a.mu.Unlock()
	c.replaceHistory()
	c.apply(Frame{Type: EvtTurnInput, Input: &entry}) // accepted steer notification crossed the commit boundary
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) != 2 || c.entries[0].ID != userID || c.entries[1].ID != textID {
		t.Fatalf("commit changed identity or duplicated steering: %+v", c.entries)
	}
}
