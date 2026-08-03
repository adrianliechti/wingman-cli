//go:build !windows

package terminal

import (
	"strings"
	"testing"
	"time"
)

func TestSessionEchoAndExit(t *testing.T) {
	m := NewManager(t.TempDir())
	defer m.Close()

	exited := make(chan string, 1)
	m.SetExitHandler(func(id string) { exited <- id })

	s, err := m.Create(120, 40)
	if err != nil {
		t.Fatal(err)
	}
	if cols, rows := s.Size(); cols != 120 || rows != 40 {
		t.Fatalf("size = %dx%d, want 120x40", cols, rows)
	}

	_, out, cancel := s.Subscribe()
	defer cancel()

	if err := s.Write([]byte("echo wing\"\"man-marker-ok\r")); err != nil {
		t.Fatal(err)
	}
	if got := readUntil(t, out, "wingman-marker-ok"); !got {
		t.Fatal("marker never echoed")
	}

	if err := s.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	if err := s.Write([]byte("exit\r")); err != nil {
		t.Fatal(err)
	}

	select {
	case id := <-exited:
		if id != s.ID() {
			t.Fatalf("exit id = %q, want %q", id, s.ID())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("session never exited")
	}

	if m.Get(s.ID()) != nil {
		t.Fatal("exited session still registered")
	}
	if !s.Exited() {
		t.Fatal("session not marked exited")
	}
}

func TestSubscribeReplaysScrollback(t *testing.T) {
	m := NewManager(t.TempDir())
	defer m.Close()

	s, err := m.Create(80, 24)
	if err != nil {
		t.Fatal(err)
	}

	_, out, cancel := s.Subscribe()
	if err := s.Write([]byte("echo repl\"\"ay-marker\r")); err != nil {
		t.Fatal(err)
	}
	if got := readUntil(t, out, "replay-marker"); !got {
		t.Fatal("marker never echoed")
	}
	cancel()

	snapshot, _, cancel2 := s.Subscribe()
	defer cancel2()
	if !strings.Contains(string(snapshot), "replay-marker") {
		t.Fatalf("snapshot missing prior output: %q", snapshot)
	}
}

func readUntil(t *testing.T, out <-chan []byte, want string) bool {
	t.Helper()
	var buf strings.Builder
	deadline := time.After(15 * time.Second)
	for {
		select {
		case chunk, ok := <-out:
			if !ok {
				return false
			}
			buf.Write(chunk)
			if strings.Contains(buf.String(), want) {
				return true
			}
		case <-deadline:
			t.Logf("output so far: %q", buf.String())
			return false
		}
	}
}
