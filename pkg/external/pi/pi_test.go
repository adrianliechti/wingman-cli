package pi

import (
	"path/filepath"
	"testing"
)

func TestNativeSessionsDirUsesPiConfigDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)

	want := filepath.Join(dir, "sessions")
	if got := NativeSessionsDir(); got != want {
		t.Fatalf("NativeSessionsDir() = %q, want %q", got, want)
	}
}

func TestNativeSessionsDirDefaultsUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PI_CODING_AGENT_DIR", "")

	want := filepath.Join(home, ".pi", "agent", "sessions")
	if got := NativeSessionsDir(); got != want {
		t.Fatalf("NativeSessionsDir() = %q, want %q", got, want)
	}
}
