package claude

import (
	"context"
	"io"
	"log/slog"

	"github.com/coder/acp-go-sdk"
)

func Run(ctx context.Context, opts Options, in io.Reader, out io.Writer, logger *slog.Logger) error {
	a := New(opts)
	defer a.Close()
	conn := acp.NewAgentSideConnection(a, out, in)
	if logger != nil {
		conn.SetLogger(logger)
	}
	a.SetAgentConnection(conn)
	select {
	case <-conn.Done():
	case <-ctx.Done():
	}
	return nil
}
