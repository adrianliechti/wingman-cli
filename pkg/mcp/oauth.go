package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"

	"github.com/adrianliechti/wingman-agent/internal/browser"
)

// DefaultCallbackPort receives the OAuth redirect on localhost. It is fixed so
// a dynamically registered client keeps a stable redirect URI across logins.
const DefaultCallbackPort = 3142

// ErrLoginRequired is returned when a remote server demands OAuth and no
// interactive login is available.
var ErrLoginRequired = errors.New("login required")

func callbackURL(port int) string {
	return fmt.Sprintf("http://localhost:%d/callback", port)
}

func (s ServerConfig) callbackPort() int {
	if s.OAuth != nil && s.OAuth.CallbackPort > 0 {
		return s.OAuth.CallbackPort
	}

	return DefaultCallbackPort
}

func hasAuthorizationHeader(headers map[string]string) bool {
	for name := range headers {
		if strings.EqualFold(name, "Authorization") {
			return true
		}
	}

	return false
}

type oauthOptions struct {
	store   *CredentialStore
	server  ServerConfig
	fetcher auth.AuthorizationCodeFetcher

	redirectURL string

	// skipToken ignores a stored token so the flow always prompts.
	skipToken bool
}

func newOAuthHandler(opts oauthOptions) (*auth.AuthorizationCodeHandler, error) {
	url := opts.server.URL

	redirect := opts.redirectURL

	if redirect == "" {
		redirect = callbackURL(opts.server.callbackPort())
	}

	cred, err := opts.store.Get(url)

	if err != nil {
		return nil, err
	}

	cfg := &auth.AuthorizationCodeHandlerConfig{
		RedirectURL:              redirect,
		RequestRefreshToken:      true,
		AuthorizationCodeFetcher: opts.fetcher,

		NewTokenSource: func(ctx context.Context, oc *oauth2.Config, token *oauth2.Token) (oauth2.TokenSource, error) {
			cred := &Credential{
				ClientID:     oc.ClientID,
				ClientSecret: oc.ClientSecret,
				TokenURL:     oc.Endpoint.TokenURL,
				AuthStyle:    oc.Endpoint.AuthStyle,
				RedirectURL:  oc.RedirectURL,
				Token:        token,
			}

			if err := opts.store.Set(url, cred); err != nil {
				return nil, err
			}

			return newPersistingTokenSource(oc.TokenSource(ctx, token), opts.store, url, cred), nil
		},
	}

	if cfg.AuthorizationCodeFetcher == nil {
		cfg.AuthorizationCodeFetcher = func(context.Context, *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			return nil, ErrLoginRequired
		}
	}

	switch {
	case opts.server.OAuth != nil && opts.server.OAuth.ClientID != "":
		cfg.PreregisteredClient = &oauthex.ClientCredentials{ClientID: opts.server.OAuth.ClientID}

	case cred != nil && cred.ClientID != "" && cred.RedirectURL == redirect:
		cfg.PreregisteredClient = &oauthex.ClientCredentials{ClientID: cred.ClientID}

		if cred.ClientSecret != "" {
			cfg.PreregisteredClient.ClientSecretAuth = &oauthex.ClientSecretAuth{ClientSecret: cred.ClientSecret}
		}

	default:
		cfg.DynamicClientRegistrationConfig = &auth.DynamicClientRegistrationConfig{
			Metadata: &oauthex.ClientRegistrationMetadata{
				ClientName:              "Wingman",
				RedirectURIs:            []string{redirect},
				GrantTypes:              []string{"authorization_code", "refresh_token"},
				ResponseTypes:           []string{"code"},
				TokenEndpointAuthMethod: "none",
			},
		}
	}

	if !opts.skipToken && cred != nil && cred.TokenURL != "" && cred.usableToken() {
		ctx := context.WithValue(context.Background(), oauth2.HTTPClient, http.DefaultClient)
		cfg.InitialTokenSource = newPersistingTokenSource(cred.config().TokenSource(ctx, cred.Token), opts.store, url, cred)
	}

	return auth.NewAuthorizationCodeHandler(cfg)
}

// usableToken reports whether the token can still produce an access token,
// either directly or through a refresh.
func (c *Credential) usableToken() bool {
	return c.Token != nil && (c.Token.Valid() || c.Token.RefreshToken != "")
}

// persistingTokenSource writes refreshed tokens back to the store.
type persistingTokenSource struct {
	base  oauth2.TokenSource
	store *CredentialStore
	url   string

	mu   sync.Mutex
	cred *Credential
}

func newPersistingTokenSource(base oauth2.TokenSource, store *CredentialStore, url string, cred *Credential) *persistingTokenSource {
	return &persistingTokenSource{base: base, store: store, url: url, cred: cred}
}

func (s *persistingTokenSource) Token() (*oauth2.Token, error) {
	token, err := s.base.Token()

	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cred.Token != nil && s.cred.Token.AccessToken == token.AccessToken && s.cred.Token.RefreshToken == token.RefreshToken {
		return token, nil
	}

	cred := *s.cred
	cred.Token = token
	s.cred = &cred

	if err := s.store.Set(s.url, s.cred); err != nil {
		return nil, err
	}

	return token, nil
}

// LoginOptions controls the interactive authorization flow.
type LoginOptions struct {
	// Store defaults to the user-wide credential file.
	Store *CredentialStore

	// CallbackPort overrides the server's configured callback port.
	CallbackPort int

	// Reauthenticate ignores a stored token and always opens the browser.
	Reauthenticate bool

	// OpenURL launches the authorization URL. It defaults to the system browser.
	OpenURL func(url string) error

	// Output receives progress messages such as the authorization URL.
	Output io.Writer
}

// LoginResult reports how a login connected.
type LoginResult struct {
	Session *mcp.ClientSession

	// Prompted is true when the user was sent to the browser.
	Prompted bool
}

// Login connects to a remote server, running the browser-based authorization
// code flow when the server asks for it, and stores the resulting credential.
// The returned session is open and owned by the caller.
func Login(ctx context.Context, server ServerConfig, opts LoginOptions) (*LoginResult, error) {
	if server.URL == "" {
		return nil, errors.New("only remote (url) servers support login")
	}

	if hasAuthorizationHeader(server.Headers) {
		return nil, errors.New("server uses a configured Authorization header")
	}

	if opts.Store == nil {
		opts.Store = DefaultCredentialStore()
	}

	if opts.OpenURL == nil {
		opts.OpenURL = browser.Open
	}

	if opts.Output == nil {
		opts.Output = io.Discard
	}

	port := opts.CallbackPort

	if port <= 0 {
		port = server.callbackPort()
	}

	callback, err := newCallbackServer(port)

	if err != nil {
		return nil, err
	}

	defer callback.close()

	stored, err := opts.Store.Get(server.URL)

	if err != nil {
		return nil, err
	}

	result, err := login(ctx, server, opts, callback)

	// A stored client the authorization server no longer accepts is replaced
	// by registering afresh.
	reusedClient := stored != nil && stored.ClientID != "" && (server.OAuth == nil || server.OAuth.ClientID == "")

	if err != nil && reusedClient && ctx.Err() == nil {
		if _, err := opts.Store.Delete(server.URL); err != nil {
			return nil, err
		}

		fmt.Fprintf(opts.Output, "Stored client registration was rejected, registering again\n")

		result, err = login(ctx, server, opts, callback)
	}

	return result, err
}

func login(ctx context.Context, server ServerConfig, opts LoginOptions, callback *callbackServer) (*LoginResult, error) {
	result := &LoginResult{}

	fetcher := func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
		result.Prompted = true

		fmt.Fprintf(opts.Output, "Opening your browser to log in. If it does not open, visit:\n  %s\n", args.URL)

		if err := opts.OpenURL(args.URL); err != nil {
			fmt.Fprintf(opts.Output, "Could not open browser: %v\n", err)
		}

		return callback.wait(ctx)
	}

	handler, err := newOAuthHandler(oauthOptions{
		store:       opts.Store,
		server:      server,
		fetcher:     fetcher,
		redirectURL: callback.url,
		skipToken:   opts.Reauthenticate,
	})

	if err != nil {
		return nil, err
	}

	transport, err := createTransport(server, "", handler)

	if err != nil {
		return nil, err
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "wingman", Version: "1.0.0"}, nil)

	session, err := client.Connect(ctx, transport, nil)

	if err != nil {
		return nil, err
	}

	result.Session = session

	return result, nil
}

type callbackResult struct {
	result *auth.AuthorizationResult
	err    error
}

type callbackServer struct {
	url     string
	server  *http.Server
	results chan callbackResult
}

func newCallbackServer(port int) (*callbackServer, error) {
	listener, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", port))

	if err != nil {
		return nil, fmt.Errorf("listen on callback port %d: %w", port, err)
	}

	cs := &callbackServer{
		url:     callbackURL(listener.Addr().(*net.TCPAddr).Port),
		results: make(chan callbackResult, 1),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", cs.handle)

	cs.server = &http.Server{Handler: mux}

	go func() { _ = cs.server.Serve(listener) }()

	return cs, nil
}

func (cs *callbackServer) handle(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if code := query.Get("error"); code != "" {
		message := code

		if description := query.Get("error_description"); description != "" {
			message += ": " + description
		}

		fmt.Fprintf(w, "<p>Login failed: %s</p>", message)
		cs.deliver(callbackResult{err: fmt.Errorf("authorization failed: %s", message)})
		return
	}

	if query.Get("code") == "" {
		http.Error(w, "missing authorization code", http.StatusBadRequest)
		return
	}

	fmt.Fprint(w, "<p>Login successful. You can close this window and return to Wingman.</p>")

	cs.deliver(callbackResult{result: &auth.AuthorizationResult{
		Code:  query.Get("code"),
		State: query.Get("state"),
		Iss:   query.Get("iss"),
	}})
}

func (cs *callbackServer) deliver(result callbackResult) {
	select {
	case cs.results <- result:
	default:
	}
}

func (cs *callbackServer) wait(ctx context.Context) (*auth.AuthorizationResult, error) {
	select {
	case r := <-cs.results:
		return r.result, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (cs *callbackServer) close() {
	_ = cs.server.Close()
}
