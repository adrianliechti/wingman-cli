package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAgentAPIContract(t *testing.T) {
	t.Setenv("WINGMAN_URL", "http://localhost:1")
	app, err := New(t.Context(), t.TempDir(), &ServerOptions{NoBrowser: true, disableManagedTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v2/bootstrap", nil))
	if rec.Code != 200 {
		t.Fatalf("bootstrap: %d %s", rec.Code, rec.Body)
	}
	var boot struct {
		WorkspaceScope
		Protocol int          `json:"protocol"`
		Backends []AgentEntry `json:"backends"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &boot); err != nil {
		t.Fatal(err)
	}
	if boot.Protocol != 2 || boot.InstanceID == "" || boot.WorkspaceID == "" || len(boot.Backends) == 0 {
		t.Fatalf("bootstrap: %#v", boot)
	}
	for _, instance := range []string{"", "old-instance"} {
		req := httptest.NewRequest(http.MethodPost, "/api/files", nil)
		req.Header.Set(instanceHeader, instance)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusConflict {
			t.Fatalf("instance %q: %d", instance, rec.Code)
		}
	}
}
