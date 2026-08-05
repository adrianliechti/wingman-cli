package clipboard

import (
	"errors"
	"strings"
	"testing"
)

func TestReadContents(t *testing.T) {
	contents, err := readContents("text", nil, "", errors.New("image unavailable"))
	if err != nil || len(contents) != 1 || contents[0].Text != "text" {
		t.Fatalf("partial read = %#v, %v", contents, err)
	}

	contents, err = readContents("", nil, "", errors.New("image unavailable"))
	if err != nil || len(contents) != 0 {
		t.Fatalf("empty successful read = %#v, %v", contents, err)
	}

	_, err = readContents("", errors.New("text unavailable"), "", errors.New("image unavailable"))
	if err == nil || !strings.Contains(err.Error(), "clipboard text") || !strings.Contains(err.Error(), "clipboard image") {
		t.Fatalf("combined error = %v", err)
	}
}

func TestWriteTextWithRemoteUsesTerminalOnly(t *testing.T) {
	var calls []string
	backends := testCopyBackends(&calls)

	if err := writeTextWith("hello", copyEnvironment{ssh: true}, backends); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls, ","); got != "osc52" {
		t.Fatalf("calls = %q, want osc52", got)
	}
}

func TestWriteTextWithRemoteTMUXFallsBackToOSC52(t *testing.T) {
	var calls []string
	backends := testCopyBackends(&calls)
	backends.tmux = func(string) error {
		calls = append(calls, "tmux")
		return errors.New("not ready")
	}

	if err := writeTextWith("hello", copyEnvironment{ssh: true, tmux: true}, backends); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls, ","); got != "tmux,osc52" {
		t.Fatalf("calls = %q, want tmux,osc52", got)
	}
}

func TestWriteTextWithLocalFallbackOrder(t *testing.T) {
	var calls []string
	backends := testCopyBackends(&calls)
	backends.native = func(string) error {
		calls = append(calls, "native")
		return errors.New("native failed")
	}
	backends.wsl = func(string) error {
		calls = append(calls, "wsl")
		return errors.New("wsl failed")
	}

	if err := writeTextWith("hello", copyEnvironment{wsl: true, tmux: true}, backends); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls, ","); got != "native,wsl,tmux" {
		t.Fatalf("calls = %q, want native,wsl,tmux", got)
	}
}

func TestWriteTextWithLocalShortCircuitsSuccessfulBackends(t *testing.T) {
	tests := []struct {
		name        string
		environment copyEnvironment
		nativeErr   error
		wantCalls   string
	}{
		{name: "native", environment: copyEnvironment{wsl: true, tmux: true}, wantCalls: "native"},
		{name: "WSL", environment: copyEnvironment{wsl: true, tmux: true}, nativeErr: errors.New("native failed"), wantCalls: "native,wsl"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			backends := testCopyBackends(&calls)
			backends.native = func(string) error {
				calls = append(calls, "native")
				return tt.nativeErr
			}
			if err := writeTextWith("hello", tt.environment, backends); err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(calls, ","); got != tt.wantCalls {
				t.Fatalf("calls = %q, want %q", got, tt.wantCalls)
			}
		})
	}
}

func TestWriteTextWithReturnsBackendErrors(t *testing.T) {
	failed := func(string) error { return errors.New("failed") }
	err := writeTextWith("hello", copyEnvironment{wsl: true, tmux: true}, copyBackends{
		native: failed,
		wsl:    failed,
		tmux:   failed,
		osc52:  failed,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	for _, label := range []string{"native clipboard", "WSL fallback", "tmux clipboard", "OSC 52 fallback"} {
		if !strings.Contains(err.Error(), label) {
			t.Errorf("error %q missing %q", err, label)
		}
	}
}

func TestOSC52Sequence(t *testing.T) {
	got, err := osc52Sequence("hello", false)
	if err != nil {
		t.Fatal(err)
	}
	if want := "\x1b]52;c;aGVsbG8=\x07"; got != want {
		t.Fatalf("sequence = %q, want %q", got, want)
	}

	tmux, err := osc52Sequence("hello", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tmux, "\x1bPtmux;\x1b\x1b]52;c;") || !strings.HasSuffix(tmux, "\x07\x1b\\") {
		t.Fatalf("tmux sequence = %q", tmux)
	}
}

func TestOSC52SequenceRejectsLargePayload(t *testing.T) {
	_, err := osc52Sequence(strings.Repeat("x", osc52MaxRawBytes+1), false)
	if err == nil || !strings.Contains(err.Error(), "payload too large") {
		t.Fatalf("error = %v, want payload size error", err)
	}
}

func testCopyBackends(calls *[]string) copyBackends {
	record := func(name string) func(string) error {
		return func(string) error {
			*calls = append(*calls, name)
			return nil
		}
	}
	return copyBackends{
		native: record("native"),
		wsl:    record("wsl"),
		tmux:   record("tmux"),
		osc52:  record("osc52"),
	}
}
