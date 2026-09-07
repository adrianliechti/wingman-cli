package codex

import (
	"context"
	"fmt"
	"io"

	acpcommon "github.com/adrianliechti/wingman-agent/pkg/acp"
	"github.com/coder/acp-go-sdk"
)

const clientWriteTimeout = acpcommon.WriteTimeout

// The ACP SDK checks contexts before writes, but an io.Writer can then block
// indefinitely. Bound the stdio write itself and retire a broken transport.
func newClientWriter(w io.Writer) *acpcommon.ConnectionWriter {
	return acpcommon.NewConnectionWriter(w, clientWriteTimeout)
}

// Also honor cancellation when an embedding host supplies its own ACP
// connection. SDK calls may be waiting to acquire its shared write mutex.
func callClient[T any](ctx context.Context, conn *acp.AgentSideConnection, call func() (T, error)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	type result struct {
		value T
		err   error
	}
	done := make(chan result, 1)
	go func() {
		value, err := call()
		done <- result{value, err}
	}()
	select {
	case r := <-done:
		return r.value, r.err
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-conn.Done():
		return zero, fmt.Errorf("ACP client disconnected")
	}
}

func notifyClient(ctx context.Context, conn *acp.AgentSideConnection, sid acp.SessionId, update acp.SessionUpdate) error {
	ctx, cancel := context.WithTimeout(ctx, clientWriteTimeout)
	defer cancel()
	_, err := callClient(ctx, conn, func() (struct{}, error) {
		return struct{}{}, conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: sid, Update: update})
	})
	return err
}
