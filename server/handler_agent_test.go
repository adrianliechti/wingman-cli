package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAgentAPIContract(t *testing.T) {
	t.Setenv("WINGMAN_URL", "http://localhost:1")
	app, err := New(context.Background(), t.TempDir(), &ServerOptions{NoBrowser: true, disableManagedTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	stateRecorder := httptest.NewRecorder()
	app.ServeHTTP(stateRecorder, httptest.NewRequest(http.MethodGet, "/api/agent", nil))
	if stateRecorder.Code != http.StatusOK {
		t.Fatalf("state status = %d", stateRecorder.Code)
	}
	var state map[string]any
	if err := json.NewDecoder(stateRecorder.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if _, ok := state["can_delete"].(bool); !ok {
		t.Fatalf("can_delete = %#v, want boolean", state["can_delete"])
	}
	if _, ok := state["canDelete"]; ok {
		t.Fatal("legacy camel-case canDelete field is still present")
	}

	invalidRecorder := httptest.NewRecorder()
	app.ServeHTTP(invalidRecorder, httptest.NewRequest(
		http.MethodPost,
		"/api/agent",
		strings.NewReader(`{"agent":"wingman","legacy":true}`),
	))
	if invalidRecorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown-field status = %d, want %d", invalidRecorder.Code, http.StatusBadRequest)
	}

	validRecorder := httptest.NewRecorder()
	app.ServeHTTP(validRecorder, httptest.NewRequest(
		http.MethodPost,
		"/api/agent",
		strings.NewReader(`{"agent":"wingman"}`),
	))
	if validRecorder.Code != http.StatusNoContent {
		t.Fatalf("switch status = %d, want %d", validRecorder.Code, http.StatusNoContent)
	}
	if validRecorder.Body.Len() != 0 {
		t.Fatalf("switch response body = %q, want empty", validRecorder.Body.String())
	}
}
