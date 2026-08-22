package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/schedule"
)

func TestSchedulesAPI(t *testing.T) {
	t.Setenv("WINGMAN_URL", "http://localhost:1")

	ctx := t.Context()
	app, err := New(ctx, t.TempDir(), &ServerOptions{NoBrowser: true, disableManagedTools: true})
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

	if got := getSchedules(t, web.URL, created.ID); len(got) != 0 {
		t.Fatalf("schedules = %+v, want empty", got)
	}

	store := app.sessionSchedules(created.ID)
	if store == nil {
		t.Fatal("session has no schedule store")
	}

	err = store.Mutate(func(tasks []schedule.Task) ([]schedule.Task, error) {
		return append(tasks, schedule.Task{
			ID:        "job-1",
			Prompt:    "check the deploy",
			Schedule:  "every 1h",
			Script:    "echo hi",
			Status:    schedule.StatusActive,
			CreatedAt: time.Now(),
		}), nil
	})
	if err != nil {
		t.Fatalf("seed schedule: %v", err)
	}

	got := getSchedules(t, web.URL, created.ID)
	if len(got) != 1 {
		t.Fatalf("schedules = %+v, want one", got)
	}
	entry := got[0]
	if entry.ID != "job-1" || entry.Prompt != "check the deploy" || entry.Schedule != "every 1h" {
		t.Fatalf("entry = %+v", entry)
	}
	if !entry.Script {
		t.Fatal("entry should report its pre-check script")
	}
	if entry.NextRun == "" || entry.NextIn == "" {
		t.Fatalf("entry = %+v, want a next run", entry)
	}

	res = postScheduleAction(t, web.URL, created.ID, "job-1", "pause")
	if res.StatusCode != http.StatusNoContent {
		res.Body.Close()
		t.Fatalf("pause status = %d, want 204", res.StatusCode)
	}
	res.Body.Close()
	got = getSchedules(t, web.URL, created.ID)
	if got[0].Status != schedule.StatusPaused || got[0].NextRun != "" {
		t.Fatalf("paused schedule = %+v", got[0])
	}

	res = postScheduleAction(t, web.URL, created.ID, "job-1", "resume")
	if res.StatusCode != http.StatusNoContent {
		res.Body.Close()
		t.Fatalf("resume status = %d, want 204", res.StatusCode)
	}
	res.Body.Close()
	got = getSchedules(t, web.URL, created.ID)
	if got[0].Status != schedule.StatusActive || got[0].NextRun == "" {
		t.Fatalf("resumed schedule = %+v", got[0])
	}

	req, _ := http.NewRequest(http.MethodDelete, web.URL+"/api/sessions/"+created.ID+"/schedules/nope", nil)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("delete unknown schedule status = %d, want 404", res.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodDelete, web.URL+"/api/sessions/"+created.ID+"/schedules/job-1", nil)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", res.StatusCode)
	}

	if got := getSchedules(t, web.URL, created.ID); len(got) != 0 {
		t.Fatalf("schedules = %+v, want empty after delete", got)
	}
}

func postScheduleAction(t *testing.T, baseURL, sessionID, scheduleID, action string) *http.Response {
	t.Helper()

	res, err := http.Post(
		baseURL+"/api/sessions/"+sessionID+"/schedules/"+scheduleID+"/"+action,
		"application/json",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func getSchedules(t *testing.T, baseURL, sessionID string) []ScheduleEntry {
	t.Helper()

	res, err := http.Get(baseURL + "/api/sessions/" + sessionID + "/schedules")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("schedules status = %d", res.StatusCode)
	}

	var out []ScheduleEntry
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}
