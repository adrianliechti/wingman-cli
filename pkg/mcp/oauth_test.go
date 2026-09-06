package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// fakeAuthServer implements just enough of OAuth 2.1 with dynamic client
// registration and PKCE to drive the authorization code flow.
type fakeAuthServer struct {
	server *httptest.Server

	mu            sync.Mutex
	clients       map[string][]string
	codes         map[string]string
	tokens        map[string]time.Time
	registrations int
	authorizes    int
	refreshes     int
	rejectClients bool
}

func newFakeAuthServer(t *testing.T) *fakeAuthServer {
	t.Helper()

	as := &fakeAuthServer{
		clients: map[string][]string{},
		codes:   map[string]string{},
		tokens:  map[string]time.Time{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", as.metadata)
	mux.HandleFunc("/register", as.register)
	mux.HandleFunc("/authorize", as.authorize)
	mux.HandleFunc("/token", as.token)

	as.server = httptest.NewServer(mux)
	t.Cleanup(as.server.Close)

	return as
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func (as *fakeAuthServer) metadata(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                         as.server.URL,
		"authorization_endpoint":                         as.server.URL + "/authorize",
		"token_endpoint":                                 as.server.URL + "/token",
		"registration_endpoint":                          as.server.URL + "/register",
		"response_types_supported":                       []string{"code"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{"none"},
		"authorization_response_iss_parameter_supported": true,
	})
}

func (as *fakeAuthServer) register(w http.ResponseWriter, r *http.Request) {
	var meta oauthex.ClientRegistrationMetadata

	if err := json.NewDecoder(r.Body).Decode(&meta); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	as.mu.Lock()
	as.registrations++
	id := fmt.Sprintf("client-%d", as.registrations)
	as.clients[id] = meta.RedirectURIs
	as.mu.Unlock()

	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  id,
		"redirect_uris":              meta.RedirectURIs,
		"token_endpoint_auth_method": "none",
	})
}

func (as *fakeAuthServer) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	as.mu.Lock()
	as.authorizes++
	redirects, known := as.clients[q.Get("client_id")]
	reject := as.rejectClients
	as.mu.Unlock()

	redirect, err := url.Parse(q.Get("redirect_uri"))

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	values := url.Values{"state": {q.Get("state")}, "iss": {as.server.URL}}

	switch {
	case !known || reject:
		values.Set("error", "invalid_client")
		values.Set("error_description", "unknown client")
	case !contains(redirects, q.Get("redirect_uri")):
		values.Set("error", "invalid_request")
		values.Set("error_description", "redirect_uri mismatch")
	case q.Get("code_challenge_method") != "S256":
		values.Set("error", "invalid_request")
		values.Set("error_description", "pkce required")
	default:
		code := fmt.Sprintf("code-%d", as.authorizes)
		as.mu.Lock()
		as.codes[code] = q.Get("code_challenge")
		as.mu.Unlock()
		values.Set("code", code)
	}

	redirect.RawQuery = values.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

func (as *fakeAuthServer) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	as.mu.Lock()
	defer as.mu.Unlock()

	fail := func(code string) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": code})
	}

	switch r.Form.Get("grant_type") {
	case "authorization_code":
		challenge, ok := as.codes[r.Form.Get("code")]

		if !ok {
			fail("invalid_grant")
			return
		}

		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))

		if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
			fail("invalid_grant")
			return
		}

		delete(as.codes, r.Form.Get("code"))
	case "refresh_token":
		if !strings.HasPrefix(r.Form.Get("refresh_token"), "refresh-") {
			fail("invalid_grant")
			return
		}

		as.refreshes++
	default:
		fail("unsupported_grant_type")
		return
	}

	access := fmt.Sprintf("access-%d", len(as.tokens)+1)
	as.tokens[access] = time.Now().Add(time.Hour)

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"token_type":    "Bearer",
		"expires_in":    3600,
		"refresh_token": "refresh-" + access,
	})
}

func (as *fakeAuthServer) verify(ctx context.Context, token string, r *http.Request) (*auth.TokenInfo, error) {
	as.mu.Lock()
	defer as.mu.Unlock()

	expiry, ok := as.tokens[token]

	if !ok {
		return nil, auth.ErrInvalidToken
	}

	return &auth.TokenInfo{Expiration: expiry}, nil
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}

	return false
}

// newProtectedServer serves an MCP server behind bearer auth pointing at as.
func newProtectedServer(t *testing.T, as *fakeAuthServer) string {
	t.Helper()

	mcpServer := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "protected", Version: "1.0.0"}, nil)
	sdkmcp.AddTool(mcpServer, &sdkmcp.Tool{Name: "ping"},
		func(context.Context, *sdkmcp.CallToolRequest, struct{}) (*sdkmcp.CallToolResult, any, error) {
			return &sdkmcp.CallToolResult{}, nil, nil
		})

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	metadataURL := server.URL + "/.well-known/oauth-protected-resource/mcp"

	mux.Handle("/.well-known/oauth-protected-resource/mcp", auth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
		Resource:             server.URL + "/mcp",
		AuthorizationServers: []string{as.server.URL},
	}))

	handler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return mcpServer }, nil)
	mux.Handle("/mcp", auth.RequireBearerToken(as.verify, &auth.RequireBearerTokenOptions{ResourceMetadataURL: metadataURL})(handler))

	return server.URL + "/mcp"
}

func freePort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "localhost:0")

	if err != nil {
		t.Fatal(err)
	}

	defer listener.Close()

	return listener.Addr().(*net.TCPAddr).Port
}

// followRedirect plays the browser: it visits the authorization URL and lets
// the redirect land on the local callback server.
func followRedirect(t *testing.T) func(string) error {
	return func(u string) error {
		go func() {
			resp, err := http.Get(u)
			if err != nil {
				t.Errorf("visit authorization url: %v", err)
				return
			}
			resp.Body.Close()
		}()
		return nil
	}
}

func TestLoginRegistersAndStoresCredential(t *testing.T) {
	as := newFakeAuthServer(t)
	endpoint := newProtectedServer(t, as)
	store := NewCredentialStore(filepath.Join(t.TempDir(), "creds.json"))
	port := freePort(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	server := ServerConfig{URL: endpoint}

	result, err := Login(ctx, server, LoginOptions{Store: store, CallbackPort: port, OpenURL: followRedirect(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer result.Session.Close()

	if !result.Prompted {
		t.Fatal("expected the browser flow to run")
	}

	tools, err := result.Session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "ping" {
		t.Fatalf("tools = %+v", tools.Tools)
	}

	cred, err := store.Get(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if cred == nil || cred.ClientID != "client-1" || cred.Token == nil || cred.Token.AccessToken != "access-1" {
		t.Fatalf("credential = %+v", cred)
	}
	if cred.RedirectURL != callbackURL(port) || cred.TokenURL != as.server.URL+"/token" {
		t.Fatalf("credential = %+v", cred)
	}

	// The manager reuses the stored token without prompting.
	manager := NewManager(&Config{Servers: map[string]ServerConfig{"protected": server}})
	manager.Credentials = store

	if err := manager.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	if manager.Session("protected") == nil {
		t.Fatal("expected a session")
	}
	if as.authorizes != 1 || as.registrations != 1 {
		t.Fatalf("authorizes = %d registrations = %d", as.authorizes, as.registrations)
	}

	// Logging in again keeps the registered client but prompts anew.
	again, err := Login(ctx, server, LoginOptions{Store: store, CallbackPort: port, Reauthenticate: true, OpenURL: followRedirect(t)})
	if err != nil {
		t.Fatal(err)
	}
	again.Session.Close()

	if !again.Prompted || as.authorizes != 2 || as.registrations != 1 {
		t.Fatalf("prompted = %v authorizes = %d registrations = %d", again.Prompted, as.authorizes, as.registrations)
	}
}

func TestManagerRefreshesExpiredToken(t *testing.T) {
	as := newFakeAuthServer(t)
	endpoint := newProtectedServer(t, as)
	store := NewCredentialStore(filepath.Join(t.TempDir(), "creds.json"))
	port := freePort(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	server := ServerConfig{URL: endpoint}

	result, err := Login(ctx, server, LoginOptions{Store: store, CallbackPort: port, OpenURL: followRedirect(t)})
	if err != nil {
		t.Fatal(err)
	}
	result.Session.Close()

	cred, _ := store.Get(endpoint)
	cred.Token.Expiry = time.Now().Add(-time.Minute)
	if err := store.Set(endpoint, cred); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(&Config{Servers: map[string]ServerConfig{"protected": server}})
	manager.Credentials = store

	if err := manager.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	refreshed, _ := store.Get(endpoint)
	if as.refreshes != 1 || refreshed.Token.AccessToken == "access-1" || !refreshed.Token.Valid() {
		t.Fatalf("refreshes = %d token = %+v", as.refreshes, refreshed.Token)
	}
}

func TestManagerWithoutCredentialRequiresLogin(t *testing.T) {
	as := newFakeAuthServer(t)
	endpoint := newProtectedServer(t, as)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	manager := NewManager(&Config{Servers: map[string]ServerConfig{"protected": {URL: endpoint}}})
	manager.Credentials = NewCredentialStore(filepath.Join(t.TempDir(), "creds.json"))

	err := manager.Connect(ctx)
	if !errors.Is(err, ErrLoginRequired) || !strings.Contains(err.Error(), "wingman mcp login protected") {
		t.Fatalf("err = %v", err)
	}
	if as.registrations != 0 && as.authorizes != 0 {
		t.Fatalf("unexpected browser flow: registrations = %d authorizes = %d", as.registrations, as.authorizes)
	}
}

func TestLoginReplacesRejectedClient(t *testing.T) {
	as := newFakeAuthServer(t)
	endpoint := newProtectedServer(t, as)
	store := NewCredentialStore(filepath.Join(t.TempDir(), "creds.json"))
	port := freePort(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := store.Set(endpoint, &Credential{ClientID: "stale", TokenURL: as.server.URL + "/token", RedirectURL: callbackURL(port)}); err != nil {
		t.Fatal(err)
	}

	result, err := Login(ctx, ServerConfig{URL: endpoint}, LoginOptions{Store: store, CallbackPort: port, OpenURL: followRedirect(t)})
	if err != nil {
		t.Fatal(err)
	}
	result.Session.Close()

	cred, _ := store.Get(endpoint)
	if cred.ClientID != "client-1" || as.registrations != 1 {
		t.Fatalf("credential = %+v registrations = %d", cred, as.registrations)
	}
}

func TestLoginRejectsNonRemoteServers(t *testing.T) {
	if _, err := Login(context.Background(), ServerConfig{Command: "server"}, LoginOptions{}); err == nil {
		t.Fatal("expected an error for stdio servers")
	}
	if _, err := Login(context.Background(), ServerConfig{URL: "http://x", Headers: map[string]string{"authorization": "Bearer x"}}, LoginOptions{}); err == nil {
		t.Fatal("expected an error for header-authenticated servers")
	}
}
