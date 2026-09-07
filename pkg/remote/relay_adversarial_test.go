package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRegistrationCannotReplaceAnotherKey(t *testing.T) {
	relay := NewRelay("secret")
	server := httptest.NewServer(relay)
	defer server.Close()
	defer relay.Close()
	owner := startTestWorkspace(t, server.URL, "secret")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	attacker := NewCredentials()
	attacker.ID = owner.ID
	err := Serve(ctx, ClientOptions{Relay: server.URL, Token: "secret", Credentials: attacker}, testHandler())
	if !errors.Is(err, errRejected) {
		t.Fatalf("competing key: %v", err)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	pairBrowser(t, client, server.URL, owner)
	response, err := client.Get(server.URL + "/hello")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if string(body) != "hi" {
		t.Fatalf("owner was disconnected: %s", body)
	}
}

func TestRelayPairingRejectsCrossOriginAndKeepsSecretOutOfPage(t *testing.T) {
	relay := NewRelay("secret")
	defer relay.Close()
	for _, path := range []string{pairPath, unpairPath} {
		req := httptest.NewRequest("POST", "https://relay.example.com"+path, strings.NewReader(`{}`))
		req.Header.Set("Origin", "https://other.example.com")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		response := httptest.NewRecorder()
		relay.ServeHTTP(response, req)
		if response.Code != http.StatusForbidden {
			t.Fatalf("cross-origin %s: %d", path, response.Code)
		}
	}
	response := httptest.NewRecorder()
	relay.ServeHTTP(response, httptest.NewRequest("GET", "https://relay.example.com/pair", nil))
	if response.Code != 200 || response.Header().Get("Referrer-Policy") != "no-referrer" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("pairing page: %d %v", response.Code, response.Header())
	}
	creds := NewCredentials()
	link, _ := PairURL("relay.example.com", creds)
	request, err := http.NewRequest("GET", link, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(request.URL.RequestURI(), creds.Key) {
		t.Fatal("pairing secret is in the HTTP path")
	}
	if strings.Contains(response.Body.String(), creds.Key) {
		t.Fatal("pairing secret is in the page")
	}
}

func TestPairingCookieBehindGateway(t *testing.T) {
	relay := NewRelay("secret")
	defer relay.Close()
	creds := NewCredentials()
	// Pairing only needs an authenticated registration; no proxy call is made.
	relay.tunnels[creds.ID] = &relayTunnel{key: creds.Key}
	defer delete(relay.tunnels, creds.ID)
	body, _ := json.Marshal(creds)
	for _, proto := range []string{"", "https"} {
		t.Run("forwarded-proto="+proto, func(t *testing.T) {
			req := httptest.NewRequest("POST", "http://relay.example.com/pair", bytes.NewReader(body))
			req.Header.Set("X-Forwarded-Proto", proto)
			req.Header.Set("Origin", "https://relay.example.com")
			response := httptest.NewRecorder()
			relay.ServeHTTP(response, req)
			cookies := response.Result().Cookies()
			if response.Code != http.StatusNoContent || len(cookies) != 1 {
				t.Fatalf("pairing: %d %v", response.Code, cookies)
			}
			cookie := cookies[0]
			if cookie.Secure != (proto == "https") || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
				t.Fatalf("pairing cookie: %+v", cookie)
			}
		})
	}
}

func TestTunnelCancellationCancelsRequestsAndJoinsServe(t *testing.T) {
	relay := NewRelay("secret")
	server := httptest.NewServer(relay)
	defer server.Close()
	defer relay.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	connected, started, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{})
	creds := NewCredentials()
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ClientOptions{Relay: server.URL, Token: "secret", Credentials: creds, OnStatus: func(ok bool, _ error) {
			if ok {
				close(connected)
			}
		}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.Header.Get("Cookie"), cookieName) {
				t.Error("pairing cookie reached workspace")
			}
			close(started)
			<-r.Context().Done()
			close(stopped)
		}))
	}()
	select {
	case <-connected:
	case <-time.After(5 * time.Second):
		t.Fatal("not connected")
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	pairBrowser(t, client, server.URL, creds)
	var requests sync.WaitGroup
	requests.Go(func() {
		response, err := client.Get(server.URL + "/slow")
		if err == nil {
			response.Body.Close()
		}
	})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("tunnel left request context alive")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not return")
	}
	requests.Wait()
}

func TestClosedRelayRejectsNewRegistration(t *testing.T) {
	relay := NewRelay("secret")
	relay.Close()
	server := httptest.NewServer(relay)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := Serve(ctx, ClientOptions{Relay: server.URL, Token: "secret"}, testHandler()); !errors.Is(err, errRejected) {
		t.Fatalf("registration after Close: %v", err)
	}
}

func TestTunnelReconnectsWithoutPairingAgain(t *testing.T) {
	relay := NewRelay("secret")
	server := httptest.NewServer(relay)
	defer server.Close()
	defer relay.Close()
	credentials := startTestWorkspace(t, server.URL, "secret")
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	pairBrowser(t, client, server.URL, credentials)
	previous := relay.lookup(credentials.ID, credentials.Key)
	if previous == nil {
		t.Fatal("connected before registration was ready")
	}
	// Drop the network transport, keeping both workspace and relay alive.
	_ = previous.mux.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		current := relay.lookup(credentials.ID, credentials.Key)
		if current != nil && current != previous {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("tunnel did not reconnect automatically")
		case <-time.After(10 * time.Millisecond):
		}
	}
	response, err := client.Get(server.URL + "/hello")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || string(body) != "hi" {
		t.Fatalf("existing browser lost access after reconnect: %d %s", response.StatusCode, body)
	}
}

func TestReplacementSurvivesPreviousConnectionCleanup(t *testing.T) {
	relay := NewRelay("secret")
	server := httptest.NewServer(relay)
	defer server.Close()
	defer relay.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	credentials := NewCredentials()
	headers := http.Header{
		"Authorization": {"Bearer secret"},
		headerTunnel:    {credentials.ID},
		headerKey:       {credentials.Key},
	}
	connect := func(label string) <-chan struct{} {
		ready, done := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(done)
			_ = serveOnce(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+connectPath, headers,
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, label) }),
				func(bool, error) { close(ready) })
		}()
		t.Cleanup(func() {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("connection did not stop")
			}
		})
		select {
		case <-ready:
		case <-done:
			t.Fatal("registration failed")
		case <-time.After(5 * time.Second):
			t.Fatal("registration blocked")
		}
		return done
	}
	previous := connect("previous")
	connect("replacement")
	select {
	case <-previous:
	case <-time.After(5 * time.Second):
		t.Fatal("old connection was not closed")
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	pairBrowser(t, client, server.URL, credentials)
	response, err := client.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || string(body) != "replacement" {
		t.Fatalf("old cleanup removed replacement: %d %s", response.StatusCode, body)
	}
}

func TestListenFailureClosesRelayWithoutWaitingForCallerCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	relay := NewRelay("secret")
	if err := relay.ListenAndServe(t.Context(), listener.Addr().(*net.TCPAddr).Port); err == nil {
		t.Fatal("listener succeeded on an occupied port")
	}
	select {
	case <-relay.ctx.Done():
	default:
		t.Fatal("failed listener left relay running")
	}
}

func TestRelayMultiplexesSlowStreamAndConcurrentBodies(t *testing.T) {
	relay := NewRelay("secret")
	server := httptest.NewServer(relay)
	defer server.Close()
	defer relay.Close()
	blocked, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	credentials := startRemoteHandler(t, server.URL, "secret", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/slow" {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			close(blocked)
			select {
			case <-release:
			case <-req.Context().Done():
			}
			return
		}
		// Read the complete upload before writing the response. HTTP/1 handlers
		// must explicitly enable full duplex if they interleave reads and writes.
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Error(err)
			http.Error(w, "upload failed", http.StatusBadRequest)
			return
		}
		w.Header().Set("X-Instance", req.Header.Get("X-Wingman-Instance"))
		_, _ = w.Write(body)
	}))
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	pairBrowser(t, client, server.URL, credentials)
	response, err := client.Get(server.URL + "/slow")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	<-blocked
	var work sync.WaitGroup
	for index := range 20 {
		work.Go(func() {
			body := bytes.Repeat([]byte{byte(index), 0, 255, 42}, 128<<10)
			req, _ := http.NewRequestWithContext(t.Context(), "POST", server.URL+"/echo", bytes.NewReader(body))
			req.Header.Set("X-Wingman-Instance", "same-instance")
			result, err := client.Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			defer result.Body.Close()
			got, err := io.ReadAll(result.Body)
			if err != nil || result.StatusCode != http.StatusOK || !bytes.Equal(got, body) || result.Header.Get("X-Instance") != "same-instance" {
				t.Errorf("stream %d was corrupted or blocked: status=%d bytes=%d err=%v", index, result.StatusCode, len(got), err)
			}
		})
	}
	work.Wait()
}
