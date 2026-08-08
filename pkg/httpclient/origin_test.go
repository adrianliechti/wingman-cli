package httpclient

import (
	"context"
	"net/http"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestWithOriginHeadersBindsOriginAndPreservesExistingHeaders(t *testing.T) {
	var requests []*http.Request
	base := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req)
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header), Request: req}, nil
	})}
	client, err := WithOriginHeaders(base, "https://example.test/mcp", map[string]string{
		"Authorization": "plugin",
		"X-Plugin":      "yes",
	})
	if err != nil {
		t.Fatal(err)
	}

	request, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.test:443/next", nil)
	request.Header.Set("Authorization", "client")
	if _, err := client.Do(request); err != nil {
		t.Fatal(err)
	}
	crossOrigin, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://other.test/next", nil)
	if _, err := client.Do(crossOrigin); err != nil {
		t.Fatal(err)
	}

	if got := requests[0].Header.Get("Authorization"); got != "client" {
		t.Fatalf("Authorization = %q, want client", got)
	}
	if got := requests[0].Header.Get("X-Plugin"); got != "yes" {
		t.Fatalf("X-Plugin = %q, want yes", got)
	}
	if got := requests[1].Header.Get("X-Plugin"); got != "" {
		t.Fatalf("cross-origin X-Plugin = %q, want empty", got)
	}
	if got := request.Header.Get("X-Plugin"); got != "" {
		t.Fatalf("original request was mutated: X-Plugin = %q", got)
	}
}
