package server

import (
	"encoding/json"
	"net/http"

	"github.com/coder/websocket"
)

type subscriptionRequest struct {
	Type           string     `json:"type"`
	SubscriptionID string     `json:"subscriptionId"`
	Ref            SessionRef `json:"ref"`
}

type sessionEvent struct {
	Type             string            `json:"type"`
	SubscriptionID   string            `json:"subscriptionId"`
	Ref              SessionRef        `json:"ref"`
	Epoch            string            `json:"epoch"`
	PreviousRevision uint64            `json:"previousRevision"`
	Revision         uint64            `json:"revision"`
	Entries          []TranscriptEntry `json:"entries,omitempty"`
	State            *SessionState     `json:"state,omitempty"`
	Changes          []SessionChange   `json:"changes,omitempty"`
	Message          string            `json:"message,omitempty"`
}

func (c *wsClient) deliver(value any) {
	data, err := json.Marshal(value)
	if err != nil || !c.enqueue(data) {
		if c.conn != nil {
			_ = c.conn.CloseNow()
		}
	}
}

type sessionSubscription struct {
	client *wsClient
	id     string
}

func (c *sessionController) subscribe(client *wsClient, id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Registration and snapshot enqueue share the publication lock: an update
	// can never overtake the snapshot or disappear between reading and subscribing.
	c.subscribers[sessionSubscription{client, id}] = struct{}{}
	client.deliver(sessionEvent{Type: "session.snapshot", SubscriptionID: id, Ref: c.ref, Epoch: c.epoch, Revision: c.revision, Entries: c.entries, State: &c.state})
}

func (c *sessionController) unsubscribe(client *wsClient, id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.subscribers, sessionSubscription{client, id})
}

func (c *sessionController) publishLocked(changes ...SessionChange) {
	previous := c.revision
	c.revision++
	for subscription := range c.subscribers {
		subscription.client.deliver(sessionEvent{Type: "session.update", SubscriptionID: subscription.id, Ref: c.ref, Epoch: c.epoch, PreviousRevision: previous, Revision: c.revision, Changes: changes})
	}
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(16 << 10)
	client := newWSClient(conn)
	s.wsMu.Lock()
	if s.ctx.Err() != nil {
		s.wsMu.Unlock()
		return
	}
	s.wsConns[conn] = client
	s.wsMu.Unlock()
	writerDone := make(chan struct{})
	go func() { defer close(writerDone); client.run() }()
	subscriptions := map[string]*sessionController{}
	defer func() {
		for id, c := range subscriptions {
			c.unsubscribe(client, id)
		}
		s.wsMu.Lock()
		delete(s.wsConns, conn)
		s.wsMu.Unlock()
		client.close()
		_ = conn.CloseNow()
		<-writerDone
	}()
	for {
		_, data, err := conn.Read(s.ctx)
		if err != nil {
			return
		}
		var msg subscriptionRequest
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		switch msg.Type {
		case "subscribe":
			if msg.SubscriptionID == "" || msg.Ref.SessionID == "" || msg.Ref.WorkspaceID != s.scope.WorkspaceID {
				continue
			}
			if previous := subscriptions[msg.SubscriptionID]; previous != nil {
				previous.unsubscribe(client, msg.SubscriptionID)
				delete(subscriptions, msg.SubscriptionID)
			}
			if len(subscriptions) >= 64 {
				client.deliver(sessionEvent{Type: "session.error", SubscriptionID: msg.SubscriptionID, Ref: msg.Ref, Message: "too many session subscriptions"})
				continue
			}
			b, err := s.backend(msg.Ref.BackendID)
			if err != nil {
				client.deliver(sessionEvent{Type: "session.error", SubscriptionID: msg.SubscriptionID, Ref: msg.Ref, Message: err.Error()})
				continue
			}
			c := b.session(msg.Ref.SessionID)
			subscriptions[msg.SubscriptionID] = c
			c.subscribe(client, msg.SubscriptionID)
			go c.load()
		case "unsubscribe":
			if c := subscriptions[msg.SubscriptionID]; c != nil {
				c.unsubscribe(client, msg.SubscriptionID)
				delete(subscriptions, msg.SubscriptionID)
			}
		case "focus":
			if s.files != nil {
				s.files.Notify()
			}
		}
	}
}
