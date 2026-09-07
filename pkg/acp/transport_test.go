package acp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestConnectionWriterUnblocksConcurrentWritersOnTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		local, peer := net.Pipe()
		defer peer.Close()
		writer := NewConnectionWriter(local, time.Second)
		defer writer.Close()
		var wg sync.WaitGroup
		for range 32 {
			wg.Go(func() {
				if _, err := writer.Write([]byte("frame\n")); err == nil {
					t.Error("stalled write succeeded")
				}
			})
		}
		wg.Wait()
		if writer.Err() == nil {
			t.Fatal("failed transport remained available")
		}
		if _, err := writer.Write([]byte("another frame\n")); err == nil {
			t.Fatal("retired transport accepted a frame")
		}
	})
}

type shortFrameWriter struct{ calls atomic.Int32 }

func (w *shortFrameWriter) Write(b []byte) (int, error) {
	w.calls.Add(1)
	return len(b) - 1, nil
}

func TestConnectionWriterRetiresAfterPartialFrame(t *testing.T) {
	underlying := &shortFrameWriter{}
	w := NewConnectionWriter(underlying, time.Second)
	if _, err := w.Write([]byte("frame\n")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("partial write = %v", err)
	}
	if _, err := w.Write([]byte("second frame\n")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("second write = %v", err)
	}
	if underlying.calls.Load() != 1 {
		t.Fatal("sent another frame after a truncated frame")
	}
}

type delayedWriter struct {
	release chan struct{}
	got     chan []byte
}

func (w *delayedWriter) Write(b []byte) (int, error) {
	<-w.release
	w.got <- bytes.Clone(b)
	return len(b), nil
}

func TestConnectionWriterOwnsBufferAfterTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		underlying := &delayedWriter{release: make(chan struct{}), got: make(chan []byte, 1)}
		w := NewConnectionWriter(underlying, time.Second)
		b := []byte("original")
		if _, err := w.Write(b); err == nil {
			t.Fatal("stalled write succeeded")
		}
		copy(b, "modified")
		close(underlying.release)
		if got := string(<-underlying.got); got != "original" {
			t.Fatalf("writer retained caller-owned buffer: %q", got)
		}
	})
}

func TestCallRejectsResponseReturnedAfterCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		value, err := Call(ctx, func() (bool, error) {
			cancel()
			return true, nil
		})
		if value || !errors.Is(err, context.Canceled) {
			t.Fatalf("late response = %v, %v", value, err)
		}
	})
}

func TestPromptGateCancellationDoesNotAcquireOrReleaseAnotherTurn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var gate PromptGate
		if err := gate.Lock(context.Background()); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { result <- gate.Lock(ctx) }()
		synctest.Wait()
		cancel()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled waiter = %v", err)
		}
		acquired := make(chan struct{})
		go func() {
			_ = gate.Lock(context.Background())
			close(acquired)
		}()
		synctest.Wait()
		select {
		case <-acquired:
			t.Fatal("cancelled waiter released the active turn")
		default:
		}
		gate.Unlock()
		<-acquired
		gate.Unlock()
		if err := gate.Lock(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("already cancelled caller acquired idle gate: %v", err)
		}
	})
}
