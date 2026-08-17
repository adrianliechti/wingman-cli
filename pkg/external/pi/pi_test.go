package pi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adrianliechti/wingman-agent/internal/testenv"
)

func TestConfigDirUsesWingmanHome(t *testing.T) {
	home := testenv.WingmanHome(t)

	got, err := ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "pi")
	if got != want {
		t.Fatalf("ConfigDir() = %q, want %q", got, want)
	}
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Fatalf("ConfigDir() did not create %q: %v", want, err)
	}
}

func TestNativeSessionsDirUsesPiConfigDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)

	want := filepath.Join(dir, "sessions")
	if got := NativeSessionsDir(); got != want {
		t.Fatalf("NativeSessionsDir() = %q, want %q", got, want)
	}
}

func TestNativeSessionsDirDefaultsUnderHome(t *testing.T) {
	home := testenv.UserHome(t)
	t.Setenv("PI_CODING_AGENT_DIR", "")

	want := filepath.Join(home, ".pi", "agent", "sessions")
	if got := NativeSessionsDir(); got != want {
		t.Fatalf("NativeSessionsDir() = %q, want %q", got, want)
	}
}
