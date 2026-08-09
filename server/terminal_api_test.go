//go:build !windows

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestTerminalAPI(t *testing.T) {
	t.Setenv("WINGMAN_URL", "http://localhost:1")

	ctx := t.Context()

	app, err := New(ctx, t.TempDir(), &ServerOptions{NoBrowser: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	web := httptest.NewServer(app)
	defer web.Close()

	res, err := http.Post(web.URL+"/api/terminals", "application/json", strings.NewReader(`{"cols":100,"rows":30}`))
	if err != nil {
		t.Fatal(err)
	}
	var created TerminalEntry
	err = json.NewDecoder(res.Body).Decode(&created)
	res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Cols != 100 || created.Rows != 30 {
		t.Fatalf("created = %+v", created)
	}

	if got := listTerminals(t, web.URL); len(got) != 1 {
		t.Fatalf("terminals = %+v, want 1", got)
	}

	dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
	defer dialCancel()
	conn, _, err := websocket.Dial(dialCtx, web.URL+"/api/terminals/"+created.ID+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()

	input, _ := json.Marshal(terminalMessage{Type: "input", Data: "echo wing\"\"man-web-ok\r"})
	if err := conn.Write(dialCtx, websocket.MessageText, input); err != nil {
		t.Fatal(err)
	}

	readCtx, readCancel := context.WithTimeout(ctx, 15*time.Second)
	defer readCancel()

	var seen strings.Builder
	for !strings.Contains(seen.String(), "wingman-web-ok") {
		typ, data, err := conn.Read(readCtx)
		if err != nil {
			t.Fatalf("read: %v (output so far: %q)", err, seen.String())
		}
		if typ == websocket.MessageBinary {
			seen.Write(data)
		}
	}

	input, _ = json.Marshal(terminalMessage{Type: "input", Data: "sleep 30\r"})
	if err := conn.Write(dialCtx, websocket.MessageText, input); err != nil {
		t.Fatal(err)
	}
	busyDeadline := time.Now().Add(5 * time.Second)
	for !getTerminal(t, web.URL, created.ID).Busy {
		if time.Now().After(busyDeadline) {
			t.Fatal("terminal never reported its foreground process")
		}
		time.Sleep(20 * time.Millisecond)
	}

	req, err := http.NewRequest(http.MethodDelete, web.URL+"/api/terminals/"+created.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d", res.StatusCode)
	}

	if got := listTerminals(t, web.URL); len(got) != 0 {
		t.Fatalf("terminals after delete = %+v, want none", got)
	}
}

func getTerminal(t *testing.T, base, id string) TerminalEntry {
	t.Helper()
	res, err := http.Get(base + "/api/terminals/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("terminal status = %d", res.StatusCode)
	}
	var out TerminalEntry
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func listTerminals(t *testing.T, base string) []TerminalEntry {
	t.Helper()
	res, err := http.Get(base + "/api/terminals")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", res.StatusCode)
	}
	var out []TerminalEntry
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}
