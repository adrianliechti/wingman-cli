package remote

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

// Relay routes browser requests to workspace servers. Servers register over
// an outbound WebSocket; browsers pair via a secret link, then use a cookie.
// It holds tunnel connections, not agent or chat session state.
type Relay struct {
	token   string
	ctx     context.Context
	cancel  context.CancelFunc
	csrf    *http.CrossOriginProtection
	mu      sync.Mutex
	closed  bool
	tunnels map[string]*relayTunnel
}

type relayTunnel struct {
	key   string
	mux   *yamux.Session
	proxy *httputil.ReverseProxy
}

func NewRelay(token string) *Relay {
	ctx, cancel := context.WithCancel(context.Background())
	return &Relay{token: token, ctx: ctx, cancel: cancel, csrf: http.NewCrossOriginProtection(), tunnels: map[string]*relayTunnel{}}
}

func (r *Relay) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if err := r.csrf.Check(req); err != nil {
		http.Error(w, "cross-origin request denied", http.StatusForbidden)
		return
	}
	switch {
	case req.URL.Path == connectPath:
		r.handleConnect(w, req)
	case req.URL.Path == pairPath:
		r.handlePair(w, req)
	case req.URL.Path == unpairPath:
		r.handleUnpair(w, req)
	default:
		r.handleProxy(w, req)
	}
}

func (r *Relay) handleConnect(w http.ResponseWriter, req *http.Request) {
	if r.token == "" {
		http.Error(w, "relay registration is not configured", http.StatusServiceUnavailable)
		return
	}
	got, bearer := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
	if !bearer || subtle.ConstantTimeCompare([]byte(got), []byte(r.token)) != 1 {
		http.Error(w, "invalid relay token", http.StatusUnauthorized)
		return
	}

	id := req.Header.Get(headerTunnel)
	key := req.Header.Get(headerKey)
	if !(Credentials{ID: id, Key: key}).valid() {
		http.Error(w, "missing session credentials", http.StatusBadRequest)
		return
	}
	r.mu.Lock()
	conflict := r.registrationConflict(id, key)
	r.mu.Unlock()
	if conflict {
		http.Error(w, "relay closed or registration belongs to another key", http.StatusConflict)
		return
	}
	ctx, cancel := context.WithCancel(req.Context())
	stop := context.AfterFunc(r.ctx, cancel)
	defer func() { stop(); cancel() }()
	req = req.WithContext(ctx)

	ws, err := websocket.Accept(w, req, nil)
	if err != nil {
		return
	}
	defer ws.CloseNow()

	conn := websocket.NetConn(req.Context(), ws, websocket.MessageBinary)
	mux, err := yamux.Client(conn, muxConfig())
	if err != nil {
		return
	}

	sess := &relayTunnel{key: key, mux: mux, proxy: newProxy(mux)}

	r.mu.Lock()
	// Check again after upgrading: Close or a competing registration can win
	// while the handshake is in progress.
	if r.registrationConflict(id, key) {
		r.mu.Unlock()
		_ = ws.Close(websocket.StatusPolicyViolation, "registration rejected")
		sess.close()
		return
	}
	previous := r.tunnels[id]
	r.tunnels[id] = sess
	r.mu.Unlock()
	if previous != nil {
		previous.close()
	}
	defer func() {
		r.mu.Lock()
		if r.tunnels[id] == sess {
			delete(r.tunnels, id)
		}
		r.mu.Unlock()
		sess.close()
	}()
	readyCtx, readyCancel := context.WithTimeout(ctx, 10*time.Second)
	err = ws.Write(readyCtx, websocket.MessageText, []byte("ready"))
	readyCancel()
	if err != nil {
		return
	}

	slog.Info("relay: workspace connected", "tunnel", id, "remote", req.RemoteAddr)

	select {
	case <-mux.CloseChan():
	case <-req.Context().Done():
	}

	slog.Info("relay: workspace disconnected", "tunnel", id)
}

// Caller holds r.mu. A reconnect may replace only its own registration.
func (r *Relay) registrationConflict(id, key string) bool {
	previous := r.tunnels[id]
	return r.closed || (previous != nil && subtle.ConstantTimeCompare([]byte(previous.key), []byte(key)) != 1)
}

func (s *relayTunnel) close() {
	_ = s.mux.Close()
	s.proxy.Transport.(*http.Transport).CloseIdleConnections()
}

func newProxy(mux *yamux.Session) *httputil.ReverseProxy {
	transport := &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return mux.Open()
		},
		MaxIdleConns:          16,
		IdleConnTimeout:       time.Minute,
		ResponseHeaderTimeout: 5 * time.Minute,
	}
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = "wingman"
			pr.Out.Host = pr.In.Host
			// Pairing credentials authorize the relay, not the workspace handler.
			pr.Out.Header.Del("Cookie")
			for _, cookie := range pr.In.Cookies() {
				if cookie.Name != cookieName {
					pr.Out.AddCookie(cookie)
				}
			}
		},
		Transport:     transport,
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			slog.Debug("relay: proxy error", "path", req.URL.Path, "error", err)
			http.Error(w, "workspace unavailable", http.StatusBadGateway)
		},
	}
}

func (r *Relay) handlePair(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		privatePageHeaders(w)
		_, _ = fmt.Fprint(w, pairPage)
		return
	}
	if req.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var credentials Credentials
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 4096)).Decode(&credentials); err != nil || !credentials.valid() {
		http.Error(w, "invalid pairing credentials", http.StatusBadRequest)
		return
	}
	if r.lookup(credentials.ID, credentials.Key) == nil {
		renderPage(w, http.StatusNotFound, "Pairing link is invalid or the workspace is offline.")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    credentials.ID + "." + credentials.Key,
		Path:     "/",
		HttpOnly: true,
		Secure:   req.TLS != nil || strings.EqualFold(req.Header.Get("X-Forwarded-Proto"), "https"),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((30 * 24 * time.Hour).Seconds()),
	})
	privatePageHeaders(w)
	w.WriteHeader(http.StatusNoContent)
}

func (r *Relay) handleUnpair(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "use POST to unpair this browser", http.StatusMethodNotAllowed)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Path: "/", MaxAge: -1, HttpOnly: true, Secure: req.TLS != nil || strings.EqualFold(req.Header.Get("X-Forwarded-Proto"), "https"), SameSite: http.SameSiteLaxMode})
	renderPage(w, http.StatusOK, "Unpaired. Scan a new pairing code to connect again.")
}

func (r *Relay) handleProxy(w http.ResponseWriter, req *http.Request) {
	cookie, err := req.Cookie(cookieName)
	if err != nil {
		renderPage(w, http.StatusUnauthorized, "Not paired. Scan the pairing code shown by wingman server.")
		return
	}
	id, key, _ := strings.Cut(cookie.Value, ".")
	sess := r.lookup(id, key)
	if sess == nil {
		renderPage(w, http.StatusServiceUnavailable, "Workspace is offline or the pairing expired.")
		return
	}
	sess.proxy.ServeHTTP(w, req)
}

func (r *Relay) lookup(id, key string) *relayTunnel {
	r.mu.Lock()
	sess := r.tunnels[id]
	r.mu.Unlock()
	if sess == nil || subtle.ConstantTimeCompare([]byte(sess.key), []byte(key)) != 1 {
		return nil
	}
	return sess
}

func (r *Relay) Close() {
	r.cancel()
	r.mu.Lock()
	r.closed = true
	tunnels := r.tunnels
	r.tunnels = make(map[string]*relayTunnel)
	r.mu.Unlock()
	for _, sess := range tunnels {
		sess.close()
	}
}

func privatePageHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

var pageTemplate = template.Must(template.New("page").Parse(`<!doctype html>
<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Wingman Relay</title>
<style>body{font-family:system-ui,sans-serif;display:grid;place-items:center;min-height:100vh;margin:0;background:#111;color:#eee}
main{max-width:28rem;padding:2rem;text-align:center}h1{font-size:1.25rem}p{color:#aaa}</style>
<main><h1>Wingman Relay</h1><p>{{.}}</p></main>
`))

func renderPage(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	privatePageHeaders(w)
	w.WriteHeader(status)
	_ = pageTemplate.Execute(w, message)
}

// ListenAndServe runs an HTTP relay behind a gateway that terminates TLS.
func (r *Relay) ListenAndServe(ctx context.Context, port int) error {
	if r.token == "" {
		return errors.New("relay registration token required")
	}
	if port < 1 || port > 65535 {
		return errors.New("relay port must be between 1 and 65535")
	}
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           r,
		ReadHeaderTimeout: 30 * time.Second,
	}

	runCtx, cancel := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-runCtx.Done()
		r.Close()
		_ = srv.Close()
	}()
	defer func() { cancel(); <-stopped }()

	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("relay: %w", err)
}
