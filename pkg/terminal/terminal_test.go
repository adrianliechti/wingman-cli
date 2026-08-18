//go:build !windows

package terminal

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCreateCommandUsesWorkspacePTY(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root)
	defer manager.Close()

	value := "command-env-ok"
	session, err := manager.CreateCommand(CommandSpec{
		Path:  "/bin/sh",
		Args:  []string{"-c", `printf '%s\n%s\n' "$WINGMAN_COMMAND_TEST" "$PWD"; read answer`},
		Dir:   root,
		Env:   map[string]*string{"WINGMAN_COMMAND_TEST": &value},
		Title: "Debug command",
	}, 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	if session.Title() != "Debug command" || session.ProcessID() <= 0 {
		t.Fatalf("session = title %q pid %d", session.Title(), session.ProcessID())
	}
	snapshot, output, cancel := session.Subscribe()
	defer cancel()
	combined := make(chan []byte, 8)
	if len(snapshot) > 0 {
		combined <- snapshot
	}
	go func() {
		for chunk := range output {
			combined <- chunk
		}
		close(combined)
	}()
	if !readUntil(t, combined, value) {
		t.Fatal("command environment was not printed")
	}
	if err := session.Write([]byte("done\r")); err != nil {
		t.Fatal(err)
	}
}

func TestCreateCommandRejectsOutsideWorkingDirectory(t *testing.T) {
	manager := NewManager(t.TempDir())
	defer manager.Close()
	_, err := manager.CreateCommand(CommandSpec{Path: "/bin/sh", Dir: filepath.Dir(manager.dir)}, 80, 24)
	if err == nil || !strings.Contains(err.Error(), "inside the workspace") {
		t.Fatalf("outside working directory error = %v", err)
	}
}

func TestSessionEchoAndExit(t *testing.T) {
	m := NewManager(t.TempDir())
	defer m.Close()

	exited := make(chan string, 1)
	m.SetExitHandler(func(id string) { exited <- id })

	s, err := m.Create("", 120, 40)
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

	s, err := m.Create("", 80, 24)
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

func TestShellsIncludeDefault(t *testing.T) {
	shells := Shells()
	if len(shells) == 0 {
		t.Fatal("no shells detected")
	}
	if shells[0].Name == "" || shells[0].ID == "" {
		t.Fatalf("default shell = %+v", shells[0])
	}

	names := map[string]bool{}
	for _, s := range shells {
		if names[s.Name] {
			t.Fatalf("duplicate shell name %q in %+v", s.Name, shells)
		}
		names[s.Name] = true
	}

	if _, ok := resolveShell("definitely-not-a-shell"); ok {
		t.Fatal("unknown shell accepted")
	}
	resolved, ok := resolveShell(shells[len(shells)-1].Name)
	if !ok || resolved != shells[len(shells)-1].ID {
		t.Fatalf("resolveShell by name = %q, %v", resolved, ok)
	}
}

func TestTitleDefaultsToShellName(t *testing.T) {
	m := NewManager(t.TempDir())
	defer m.Close()

	s, err := m.Create("", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if want := shellName(s.Shell()); s.Title() != want {
		t.Fatalf("title = %q, want %q", s.Title(), want)
	}
}
