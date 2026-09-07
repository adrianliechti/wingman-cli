package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func testHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "root "+r.Host)
	})
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "hi")
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		typ, data, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		_ = conn.Write(r.Context(), typ, append([]byte("echo:"), data...))
		conn.Close(websocket.StatusNormalClosure, "")
	})
	return mux
}

func startTestWorkspace(t *testing.T, relayURL, token string) Credentials {
	return startRemoteHandler(t, relayURL, token, testHandler())
}

func startRemoteHandler(t *testing.T, relayURL, token string, handler http.Handler) Credentials {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("remote transport did not stop")
		}
	})

	creds := NewCredentials()
	connected := make(chan struct{}, 1)
	go func() {
		done <- Serve(ctx, ClientOptions{
			Relay:       relayURL,
			Token:       token,
			Credentials: creds,
			OnStatus: func(ok bool, _ error) {
				if ok {
					select {
					case connected <- struct{}{}:
					default:
					}
				}
			},
		}, handler)
	}()

	select {
	case <-connected:
	case <-time.After(5 * time.Second):
		t.Fatal("workspace did not connect to relay")
	}
	return creds
}

func TestRelayRoundTrip(t *testing.T) {
	relay := NewRelay("secret")
	defer relay.Close()
	ts := httptest.NewServer(relay)
	defer ts.Close()

	creds := startTestWorkspace(t, ts.URL, "secret")

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	resp, err := client.Get(ts.URL + "/hello")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unpaired request: got %d, want 401", resp.StatusCode)
	}

	pairBrowser(t, client, ts.URL, creds)
	resp, err = client.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(string(body), "root ") {
		t.Fatalf("pair redirect: got %d %q", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), strings.TrimPrefix(ts.URL, "http://")) {
		t.Fatalf("host header not forwarded: %q", body)
	}

	resp, err = client.Get(ts.URL + "/hello")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "hi" {
		t.Fatalf("proxied request: got %q", body)
	}

	var cookie string
	for _, c := range jar.Cookies(resp.Request.URL) {
		if c.Name == cookieName {
			cookie = c.Value
		}
	}
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": {cookieName + "=" + cookie}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := conn.Write(ctx, websocket.MessageText, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "echo:ping" {
		t.Fatalf("websocket echo: got %q", data)
	}

	resp, err = client.Post(ts.URL+"/unpair", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	resp, err = client.Get(ts.URL + "/hello")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("after unpair: got %d, want 401", resp.StatusCode)
	}
}

func TestRelayRejectsBadKeyAndToken(t *testing.T) {
	relay := NewRelay("secret")
	defer relay.Close()
	ts := httptest.NewServer(relay)
	defer ts.Close()

	creds := startTestWorkspace(t, ts.URL, "secret")

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	body, _ := json.Marshal(Credentials{ID: creds.ID, Key: strings.Repeat("x", 52)})
	resp, err := client.Post(ts.URL+pairPath, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bad key: got %d, want 404", resp.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = Serve(ctx, ClientOptions{Relay: ts.URL, Token: "wrong"}, testHandler())
	if !errors.Is(err, errRejected) {
		t.Fatalf("wrong token: got %v, want rejection", err)
	}
}

func TestParseRelayURL(t *testing.T) {
	for _, raw := range []string{"relay.example.com", "https://relay.example.com/", "wss://relay.example.com"} {
		u, err := parseRelayURL(raw)
		if err != nil || u.String() != "wss://relay.example.com" {
			t.Fatalf("%q: got %v %v", raw, u, err)
		}
	}
	if _, err := parseRelayURL("ws://relay.example.com"); err == nil {
		t.Fatal("plain ws to a public host should be rejected")
	}
	if _, err := parseRelayURL("ws://localhost:8080"); err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("a", 52)
	pair, err := PairURL("relay.example.com", Credentials{ID: "abc", Key: key})
	if err != nil || pair != "https://relay.example.com/pair#abc."+key {
		t.Fatalf("pair url: %q %v", pair, err)
	}
	for _, raw := range []string{"https://user:secret@relay.example.com", "https://relay.example.com/prefix", "https://relay.example.com?token=secret", "https://relay.example.com#secret", "ws://192.168.1.10:8080"} {
		if _, err := parseRelayURL(raw); err == nil {
			t.Errorf("accepted unsupported relay origin %q", raw)
		}
	}
}

func pairBrowser(t *testing.T, client *http.Client, origin string, creds Credentials) {
	t.Helper()
	body, _ := json.Marshal(creds)
	response, err := client.Post(origin+pairPath, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("pair: %d %s", response.StatusCode, body)
	}
}
