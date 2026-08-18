package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestHealthEndpoint(t *testing.T) {
	s := &Server{}
	router := chi.NewRouter()
	s.registerRoutes(router)

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.String() != "ok\n" {
		t.Fatalf("health response = %d %q", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestBrowserHost(t *testing.T) {
	tests := map[string]string{
		"localhost": "localhost",
		"127.0.0.1": "127.0.0.1",
		"0.0.0.0":   "localhost",
		"::":        "localhost",
		"::1":       "::1",
	}

	for host, want := range tests {
		if got := browserHost(host); got != want {
			t.Errorf("browserHost(%q) = %q, want %q", host, got, want)
		}
	}
}
