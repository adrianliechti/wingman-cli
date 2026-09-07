package acp

import (
	"context"
	"time"

	acpcommon "github.com/adrianliechti/wingman-agent/pkg/acp"
	acpsdk "github.com/coder/acp-go-sdk"
)

const turnDrainTimeout = 2 * time.Second

// Host callbacks can issue ACP requests themselves. Run them outside the SDK's
// notification worker, since RPC responses wait for prior notifications to
// finish. Coalesce repeated state changes while a callback is busy.
func (a *Agent) notifySessionChange(id string) {
	a.updateMu.Lock()
	if a.updateHandler == nil {
		a.updateMu.Unlock()
		return
	}
	if a.updatePending == nil {
		a.updatePending = map[string]bool{}
	}
	a.updatePending[id] = true
	if a.updateRunning {
		a.updateMu.Unlock()
		return
	}
	a.updateRunning = true
	a.updateMu.Unlock()
	go func() {
		for {
			a.updateMu.Lock()
			pending, handler := a.updatePending, a.updateHandler
			a.updatePending = nil
			if len(pending) == 0 || handler == nil {
				a.updateRunning = false
				a.updateMu.Unlock()
				return
			}
			a.updateMu.Unlock()
			for id := range pending {
				handler(id)
			}
		}
	}()
}

func (a *Agent) watchWriter(writer *acpcommon.ConnectionWriter) {
	go func() {
		select {
		case <-writer.Done():
			a.abortTransport()
		case <-a.conn.Done():
		}
	}()
}

func (a *Agent) abortTransport() {
	a.transportOnce.Do(func() {
		if a.stdin != nil {
			_ = a.stdin.Close()
		}
		if a.stdout != nil {
			_ = a.stdout.Close()
		}
		if a.serverW != nil {
			_ = a.serverW.Close()
		}
	})
}

// A cancelled iterator stops rendering immediately, but the session remains
// occupied until the peer's response has drained its preceding notifications.
// A peer that never settles is disconnected before another turn can start.
func (a *Agent) drainTurn(t *turn, sid acpsdk.SessionId, responseDone <-chan struct{}, cancelRPC context.CancelFunc, prompt bool) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-responseDone:
			return
		case <-t.ctx.Done():
		}
		select {
		case <-responseDone:
			return
		default:
		}
		if prompt {
			a.cancelPrompt(sid)
		}
		timer := time.NewTimer(turnDrainTimeout)
		defer timer.Stop()
		select {
		case <-responseDone:
		case <-timer.C:
			a.abortTransport()
			cancelRPC()
		}
	}()
	return done
}

// Inbound UI requests have their own JSON-RPC contexts. Link them to the local
// turn too, so cancellation closes dialogs even if the peer never sends
// $/cancel_request for its outstanding permission or elicitation request.
func (a *Agent) interactionContext(ctx context.Context, sid string) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	var stop func() bool
	if sess := a.session(sid); sess != nil {
		sess.mu.Lock()
		t := sess.inflight
		sess.mu.Unlock()
		if t != nil && t.ctx != nil {
			stop = context.AfterFunc(t.ctx, cancel)
			if t.ctx.Err() != nil {
				cancel()
			}
		}
	}
	return ctx, func() {
		if stop != nil {
			stop()
		}
		cancel()
	}
}
