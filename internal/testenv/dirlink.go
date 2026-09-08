package testenv

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
)

// DirLink creates an actual directory symlink or Windows junction for a test.
// Junction tests run without the privilege required to create Windows symlinks.
func DirLink(t testing.TB, kind, target, link string) {
	t.Helper()
	switch kind {
	case "symlink":
		if err := os.Symlink(target, link); err != nil {
			if runtime.GOOS == "windows" {
				t.Skipf("directory symlink creation unavailable: %v", err)
			}
			t.Fatal(err)
		}
	case "junction":
		if runtime.GOOS != "windows" {
			t.Skip("junctions require Windows")
		}
		output, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
		if err != nil {
			t.Fatalf("mklink /J: %v: %s", err, output)
		}
	default:
		t.Fatalf("unknown directory link kind %q", kind)
	}
}
