package claude

import (
	"context"
	"io"
	"log/slog"

	acpcommon "github.com/adrianliechti/wingman-agent/pkg/acp"
	"github.com/coder/acp-go-sdk"
)

func Run(ctx context.Context, opts Options, in io.Reader, out io.Writer, logger *slog.Logger) error {
	a := New(opts)
	defer a.Close()
	writer := acpcommon.NewConnectionWriter(out, 0)
	defer writer.Close()
	conn := acp.NewAgentSideConnection(a, writer, in)
	if logger != nil {
		conn.SetLogger(logger)
	}
	a.SetAgentConnection(conn)
	select {
	case <-conn.Done():
	case <-writer.Done():
		return writer.Err()
	case <-ctx.Done():
	}
	return nil
}
