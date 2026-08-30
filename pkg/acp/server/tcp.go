package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
)

// ListenAndServe accepts ACP connections on a TCP address until ctx is
// cancelled. Each connection owns an independent ACP server, allowing a
// platform to disconnect and attach again without restarting the worker.
func ListenAndServe(ctx context.Context, address string) error {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("listen for ACP connections: %w", err)
	}

	return Serve(ctx, listener)
}

// Serve serves ACP on listener. It is exported so embedders can provide a
// listener with their own networking or authentication policy.
func Serve(ctx context.Context, listener net.Listener) error {
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = listener.Close()
		case <-done:
		}
	}()
	defer close(done)

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept ACP connection: %w", err)
		}

		go serveConnection(ctx, conn)
	}
}

func serveConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()

	if err := Run(ctx, conn, conn); err != nil && ctx.Err() == nil {
		slog.Warn("ACP connection ended with an error", "remote", conn.RemoteAddr(), "err", err)
	}
	close(done)
}
