package code

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adrianliechti/wingman-agent/internal/testenv"
)

func TestSaveExecutablePathUsesWingmanHome(t *testing.T) {
	home := testenv.WingmanHome(t)
	t.Setenv("WINGMAN_PATH", "/opt/wingman/bin/wingman")

	saveExecutablePath()

	data, err := os.ReadFile(filepath.Join(home, "path"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "/opt/wingman/bin/wingman"; got != want {
		t.Fatalf("saved executable path = %q, want %q", got, want)
	}
}
