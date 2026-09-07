package remote

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

type ClientOptions struct {
	Relay string
	Token string
	// Credentials are generated when empty.
	Credentials Credentials
	// OnStatus receives connection state changes.
	OnStatus func(connected bool, err error)
}

// Serve keeps the handler reachable through the relay until ctx is done,
// reconnecting with backoff after failures.
func Serve(ctx context.Context, opts ClientOptions, handler http.Handler) error {
	u, err := parseRelayURL(opts.Relay)
	if err != nil {
		return err
	}
	if opts.Credentials == (Credentials{}) {
		opts.Credentials = NewCredentials()
	}
	if !opts.Credentials.valid() {
		return errors.New("invalid remote credentials")
	}
	if opts.Token == "" {
		return errors.New("relay registration token required")
	}
	endpoint := u.String() + connectPath

	header := http.Header{}
	header.Set(headerTunnel, opts.Credentials.ID)
	header.Set(headerKey, opts.Credentials.Key)
	header.Set("Authorization", "Bearer "+opts.Token)

	backoff := time.Second
	for {
		err := serveOnce(ctx, endpoint, header, handler, func(connected bool, err error) {
			backoff = time.Second
			if opts.OnStatus != nil {
				opts.OnStatus(connected, err)
			}
		})
		if ctx.Err() != nil {
			return nil
		}
		if opts.OnStatus != nil {
			opts.OnStatus(false, err)
		}
		if errors.Is(err, errRejected) {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

var errRejected = errors.New("relay rejected registration")

func serveOnce(ctx context.Context, endpoint string, header http.Header, handler http.Handler, onStatus func(bool, error)) error {
	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	ws, resp, err := websocket.Dial(dialCtx, endpoint, &websocket.DialOptions{HTTPHeader: header})
	cancel()
	if err != nil {
		if resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusBadRequest) {
			return fmt.Errorf("%w: %s", errRejected, resp.Status)
		}
		return err
	}
	defer ws.CloseNow()
	// An upgraded socket alone does not mean registration has been published.
	readyCtx, readyCancel := context.WithTimeout(ctx, 10*time.Second)
	typ, ready, err := ws.Read(readyCtx)
	readyCancel()
	if err != nil {
		if websocket.CloseStatus(err) == websocket.StatusPolicyViolation {
			return errRejected
		}
		return err
	}
	if typ != websocket.MessageText || string(ready) != "ready" {
		return errors.New("unsupported relay handshake")
	}

	conn := websocket.NetConn(ctx, ws, websocket.MessageBinary)
	sess, err := yamux.Server(conn, muxConfig())
	if err != nil {
		return err
	}
	defer sess.Close()

	if onStatus != nil {
		onStatus(true, nil)
	}

	sessionCtx, sessionCancel := context.WithCancel(ctx)
	defer sessionCancel()
	srv := &http.Server{
		Handler:           handler,
		BaseContext:       func(net.Listener) context.Context { return sessionCtx },
		ReadHeaderTimeout: 30 * time.Second,
	}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
		case <-sess.CloseChan():
		}
		sessionCancel()
		_ = srv.Close()
	}()

	err = srv.Serve(sess)
	_ = sess.Close()
	<-stopped
	if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return errors.New("relay connection closed")
	}
	return err
}

func muxConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.LogOutput = nil
	cfg.Logger = muxLogger{}
	return cfg
}

type muxLogger struct{}

func (muxLogger) Print(v ...any)                 { slog.Debug(fmt.Sprint(v...)) }
func (muxLogger) Printf(format string, v ...any) { slog.Debug(fmt.Sprintf(format, v...)) }
func (muxLogger) Println(v ...any)               { slog.Debug(fmt.Sprintln(v...)) }
