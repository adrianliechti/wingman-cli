package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/task"
)

func TestTasksAPI(t *testing.T) {
	t.Setenv("WINGMAN_URL", "http://localhost:1")

	ctx := t.Context()
	app, err := New(ctx, t.TempDir(), &ServerOptions{NoBrowser: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	web := httptest.NewServer(app)
	defer web.Close()

	res, err := http.Post(web.URL+"/api/sessions", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if created.ID == "" {
		t.Fatal("no session id")
	}

	res, err = http.Get(web.URL + "/api/sessions/" + created.ID + "/tasks")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("tasks status = %d", res.StatusCode)
	}
	var tasks []TaskEntry
	if err := json.NewDecoder(res.Body).Decode(&tasks); err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks = %+v, want empty", tasks)
	}

	res, err = http.Get(web.URL + "/api/sessions/" + created.ID + "/tasks/nope")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown task status = %d, want 404", res.StatusCode)
	}

	reg := app.sessionTasks(created.ID)
	if reg == nil {
		t.Fatal("no registry for coder session")
	}
	launched, err := reg.Launch("probe", "explore", func(context.Context, *task.Task) (string, error) {
		return "probe result", nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// The server's task pump owns the events channel; observe completion
	// through the REST API instead of racing it for the event.
	var detail struct {
		TaskEntry
		Result string `json:"result"`
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		res, err = http.Get(web.URL + "/api/sessions/" + created.ID + "/tasks/" + launched.ID)
		if err != nil {
			t.Fatal(err)
		}
		err = json.NewDecoder(res.Body).Decode(&detail)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if detail.Status == "done" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("task never completed: %+v", detail)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if detail.ID != launched.ID || detail.Result != "probe result" {
		t.Fatalf("detail = %+v", detail)
	}
}
