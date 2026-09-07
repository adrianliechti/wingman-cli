package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/remote"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func connectTestRemote(t *testing.T, s *Server, relayURL string, credentials remote.Credentials) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(s.ctx)
	ready, done := make(chan struct{}), make(chan struct{})
	var once sync.Once
	s.background.Go(func() {
		defer close(done)
		err := remote.Serve(ctx, remote.ClientOptions{Relay: relayURL, Token: "test-token", Credentials: credentials, OnStatus: func(connected bool, _ error) {
			if connected {
				once.Do(func() { close(ready) })
			}
		}}, s)
		if err != nil && ctx.Err() == nil {
			t.Errorf("remote transport: %v", err)
		}
	})
	stop := func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("remote transport did not stop")
		}
	}
	t.Cleanup(stop)
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("remote transport did not connect")
	}
	return stop
}

func pairTestRemote(t *testing.T, client *http.Client, relayURL string, credentials remote.Credentials) {
	t.Helper()
	body, _ := json.Marshal(credentials)
	response, err := client.Post(relayURL+"/pair", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("pairing: %d", response.StatusCode)
	}
}

func remoteCommand(t *testing.T, client *http.Client, relayURL, instance string, command Command) (int, string) {
	t.Helper()
	body, _ := json.Marshal(command)
	req, _ := http.NewRequestWithContext(t.Context(), "POST", relayURL+"/api/v2/backends/one/sessions/saved/commands", bytes.NewReader(body))
	req.Header.Set(instanceHeader, instance)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", relayURL)
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	result, _ := io.ReadAll(response.Body)
	return response.StatusCode, string(result)
}

func remoteSnapshot(t *testing.T, client *http.Client, relayURL string, c *sessionController) sessionEvent {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	origin, _ := url.Parse(relayURL)
	headers := http.Header{"Origin": {relayURL}}
	for _, cookie := range client.Jar.Cookies(origin) {
		headers.Add("Cookie", cookie.String())
	}
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(relayURL, "http")+"/api/v2/events?instance="+c.backend.scope.InstanceID, &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := wsjson.Write(ctx, conn, subscriptionRequest{Type: "subscribe", SubscriptionID: "remote", Ref: c.ref}); err != nil {
		t.Fatal(err)
	}
	for {
		var event sessionEvent
		if err := wsjson.Read(ctx, conn, &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == "session.snapshot" {
			return event
		}
	}
}

func TestRemoteUsesExistingReceiptsAndRecoversLiveSessionAfterTunnelRestart(t *testing.T) {
	s, b, a := newProtocolServer(t)
	a.gate = make(chan struct{})
	c := b.session("saved")
	c.load()
	relay := remote.NewRelay("test-token")
	web := httptest.NewServer(relay)
	t.Cleanup(func() { relay.Close(); web.Close() })
	credentials := remote.NewCredentials()
	stop := connectTestRemote(t, s, web.URL, credentials)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	pairTestRemote(t, client, web.URL, credentials)
	command := Command{ID: "remote-input", Type: "send", Epoch: c.epoch, Text: "hello"}
	if status, _ := remoteCommand(t, client, web.URL, "old-instance", command); status != 409 {
		t.Fatalf("old instance accepted: %d", status)
	}
	status, first := remoteCommand(t, client, web.URL, s.scope.InstanceID, command)
	if status != 200 {
		t.Fatalf("send: %d %s", status, first)
	}
	status, duplicate := remoteCommand(t, client, web.URL, s.scope.InstanceID, command)
	if status != 200 || first != duplicate {
		t.Fatalf("receipt changed on retry: %d %s", status, duplicate)
	}
	waitProtocol(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.entries) > 1 && c.entries[len(c.entries)-1].Content == "live prefix"
	})
	before := remoteSnapshot(t, client, web.URL, c)
	stop()
	if s.ctx.Err() != nil {
		t.Fatal("losing the tunnel closed the workspace")
	}
	connectTestRemote(t, s, web.URL, credentials)
	after := remoteSnapshot(t, client, web.URL, c)
	if after.Epoch != before.Epoch || after.Revision != before.Revision || after.Entries[len(after.Entries)-1].Content != "live prefix" {
		t.Fatalf("reconnect did not recover the running session: %+v", after)
	}
	if a.sent.Load() != 1 {
		t.Fatalf("model invoked %d times", a.sent.Load())
	}
	close(a.gate)
}

func TestRemotePairingSwitchCannotRedirectAnOldWorkspaceCommand(t *testing.T) {
	first, _, _ := newProtocolServer(t)
	second, b, a := newProtocolServer(t)
	second.scope = WorkspaceScope{WorkspaceID: "second-workspace", InstanceID: "second-instance"}
	c := b.session("saved")
	c.load()
	relay := remote.NewRelay("test-token")
	web := httptest.NewServer(relay)
	t.Cleanup(func() { relay.Close(); web.Close() })
	firstCredentials, secondCredentials := remote.NewCredentials(), remote.NewCredentials()
	connectTestRemote(t, first, web.URL, firstCredentials)
	connectTestRemote(t, second, web.URL, secondCredentials)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	pairTestRemote(t, client, web.URL, firstCredentials)
	pairTestRemote(t, client, web.URL, secondCredentials)
	status, _ := remoteCommand(t, client, web.URL, first.scope.InstanceID, Command{ID: "old-page", Epoch: c.epoch, Type: "send", Text: "must not run here"})
	if status != 409 || a.sent.Load() != 0 {
		t.Fatalf("old browser mutated newly paired workspace: status=%d sends=%d", status, a.sent.Load())
	}
	first.Close()
	response, err := client.Get(web.URL + "/api/v2/bootstrap")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("closing another workspace broke this tunnel: %d", response.StatusCode)
	}
}
