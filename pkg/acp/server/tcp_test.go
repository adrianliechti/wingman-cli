package server

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestServeStopsWhenContextIsCancelled(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, listener) }()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned an error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not stop after cancellation")
	}
}
