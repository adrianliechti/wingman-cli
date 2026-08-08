package watch

import (
	"sync/atomic"
	"testing"
	"time"
)

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestNotifyThrottles(t *testing.T) {
	var checks atomic.Int32
	m := New(Options{MinInterval: 300 * time.Millisecond, Fallback: time.Hour}, func() {
		checks.Add(1)
	})
	ctx := t.Context()
	go m.Run(ctx)

	m.Notify()
	waitFor(t, func() bool { return checks.Load() == 1 })

	m.Notify()
	m.Notify()
	m.Notify()
	time.Sleep(100 * time.Millisecond)
	if got := checks.Load(); got != 1 {
		t.Fatalf("check ran before MinInterval elapsed: %d", got)
	}
	waitFor(t, func() bool { return checks.Load() == 2 })

	time.Sleep(400 * time.Millisecond)
	if got := checks.Load(); got != 2 {
		t.Fatalf("coalesced notifies ran more than once: %d", got)
	}
}

func TestFlushBypassesThrottle(t *testing.T) {
	var checks atomic.Int32
	m := New(Options{MinInterval: time.Hour, Fallback: time.Hour}, func() {
		checks.Add(1)
	})
	ctx := t.Context()
	go m.Run(ctx)

	m.Flush()
	waitFor(t, func() bool { return checks.Load() == 1 })

	m.Flush()
	waitFor(t, func() bool { return checks.Load() == 2 })
}

func TestFlushInterruptsThrottleWait(t *testing.T) {
	var checks atomic.Int32
	m := New(Options{MinInterval: time.Hour, Fallback: time.Hour}, func() {
		checks.Add(1)
	})
	ctx := t.Context()
	go m.Run(ctx)

	m.Flush()
	waitFor(t, func() bool { return checks.Load() == 1 })

	m.Notify()
	time.Sleep(30 * time.Millisecond)
	m.Flush()
	waitFor(t, func() bool { return checks.Load() == 2 })
}

func TestInactiveDropsSignals(t *testing.T) {
	var checks atomic.Int32
	m := New(Options{
		MinInterval: 10 * time.Millisecond,
		Fallback:    time.Hour,
		Active:      func() bool { return false },
	}, func() {
		checks.Add(1)
	})
	ctx := t.Context()
	go m.Run(ctx)

	m.Notify()
	m.Flush()
	time.Sleep(50 * time.Millisecond)
	if got := checks.Load(); got != 0 {
		t.Fatalf("inactive monitor ran checks: %d", got)
	}
}
