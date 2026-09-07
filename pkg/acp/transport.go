package acp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

const WriteTimeout = 10 * time.Second

// ConnectionWriter bounds writes, including writers whose Write method ignores
// context cancellation. After an error it retires the stream: a partial JSON
// frame must never be followed by another request on the same connection.
type ConnectionWriter struct {
	w       io.Writer
	timeout time.Duration
	gate    chan struct{}
	done    chan struct{}
	once    sync.Once
	err     error // published by closing done
}

func NewConnectionWriter(w io.Writer, timeout time.Duration) *ConnectionWriter {
	if timeout <= 0 {
		timeout = WriteTimeout
	}
	return &ConnectionWriter{w: w, timeout: timeout, gate: make(chan struct{}, 1), done: make(chan struct{})}
}

func (w *ConnectionWriter) Done() <-chan struct{} { return w.done }

func (w *ConnectionWriter) Err() error {
	select {
	case <-w.done:
		return w.err
	default:
		return nil
	}
}

func (w *ConnectionWriter) Close() error {
	w.CloseWithError(io.ErrClosedPipe)
	return nil
}

func (w *ConnectionWriter) CloseWithError(err error) {
	if err == nil {
		err = io.ErrClosedPipe
	}
	w.once.Do(func() {
		w.err = err
		close(w.done)
		if closer, ok := w.w.(io.Closer); ok {
			_ = closer.Close()
		}
	})
}

func (w *ConnectionWriter) Write(data []byte) (int, error) {
	timer := time.NewTimer(w.timeout)
	defer timer.Stop()
	select {
	case w.gate <- struct{}{}:
		defer func() { <-w.gate }()
	case <-w.done:
		return 0, w.Err()
	case <-timer.C:
		w.CloseWithError(fmt.Errorf("ACP write timed out after %s", w.timeout))
		return 0, w.Err()
	}
	if err := w.Err(); err != nil {
		return 0, err
	}
	// A caller can reuse its buffer as soon as a timed-out Write returns.
	data = bytes.Clone(data)
	written := make(chan error, 1)
	go func() {
		n, err := w.w.Write(data)
		if err == nil && n != len(data) {
			err = io.ErrShortWrite
		}
		written <- err
	}()
	select {
	case err := <-written:
		if err != nil {
			w.CloseWithError(fmt.Errorf("write ACP frame: %w", err))
			return 0, w.Err()
		}
		return len(data), nil
	case <-timer.C:
		w.CloseWithError(fmt.Errorf("ACP write timed out after %s", w.timeout))
	case <-w.done:
	}
	return 0, w.Err()
}

// Call also honors cancellation while an SDK request waits for its write lock.
// The connection must use a ConnectionWriter so abandoned writes are bounded.
func Call[T any](ctx context.Context, call func() (T, error)) (T, error) {
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
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		return r.value, r.err
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}

// PromptGate serializes turns without trapping cancelled requests behind a
// running prompt. The zero value is ready to use.
type PromptGate struct {
	once sync.Once
	gate chan struct{}
}

func (g *PromptGate) Lock(ctx context.Context) error {
	g.once.Do(func() { g.gate = make(chan struct{}, 1) })
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case g.gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			g.Unlock()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *PromptGate) Unlock() { <-g.gate }
