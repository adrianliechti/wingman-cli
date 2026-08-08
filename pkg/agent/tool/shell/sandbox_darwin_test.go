//go:build darwin

package shell

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSeatbeltProfileUsesParameterizedWritableRoots(t *testing.T) {
	profile, definitions := seatbeltProfile([]string{"/repo with spaces", "/private/tmp"})

	if strings.Contains(profile, "/repo with spaces") {
		t.Fatalf("profile interpolates an untrusted path directly: %s", profile)
	}
	for _, want := range []string{
		`(deny default)`,
		`(allow file-read*)`,
		`(param "WRITABLE_ROOT_0")`,
		`(deny appleevent-send)`,
		`(deny lsopen)`,
	} {
		if !strings.Contains(profile, want) {
			t.Errorf("profile missing %q", want)
		}
	}
	if !slices.Contains(definitions, "-DWRITABLE_ROOT_0=/repo with spaces") {
		t.Fatalf("definitions = %q", definitions)
	}
	for _, broadGrant := range []string{
		`(allow process*)`,
		`(allow mach*)`,
		`(allow ipc*)`,
		`(allow file-ioctl)`,
		`(allow iokit-open)`,
		`(allow system-socket)`,
	} {
		if strings.Contains(profile, broadGrant) {
			t.Errorf("profile contains broad grant %q", broadGrant)
		}
	}
}

func TestSeatbeltProfileCompiles(t *testing.T) {
	profile, definitions := seatbeltProfile([]string{t.TempDir(), "/private/tmp"})
	args := []string{"-p", profile}
	args = append(args, definitions...)
	args = append(args, "--", "/usr/bin/true")

	output, err := exec.Command(sandboxExecPath, args...).CombinedOutput()
	if err == nil {
		return
	}
	// Some test runners already run under Seatbelt, where applying a second
	// profile is prohibited. sandbox-exec parses the profile before reaching
	// that error, so this still catches malformed policies there.
	if strings.Contains(string(output), "sandbox_apply: Operation not permitted") {
		t.Skip("nested Seatbelt profiles are unavailable")
	}
	t.Fatalf("profile did not compile: %v\n%s", err, output)
}

func TestSeatbeltProfileEnforcesWorkspaceWrites(t *testing.T) {
	workspace := canonicalPath(t.TempDir())
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(home, fmt.Sprintf(".wingman-sandbox-probe-%d-%d", os.Getpid(), time.Now().UnixNano()))
	t.Cleanup(func() { _ = os.Remove(outside) })
	inside := filepath.Join(workspace, "inside")

	profile, definitions := seatbeltProfile([]string{workspace})
	args := []string{"-p", profile}
	args = append(args, definitions...)
	command := "touch " + shellTestQuote(inside) + " && ! touch " + shellTestQuote(outside)
	args = append(args, "--", "/bin/sh", "-c", command)

	cmd := exec.Command(sandboxExecPath, args...)
	cmd.Dir = workspace
	output, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(output), "sandbox_apply: Operation not permitted") {
		t.Skip("nested Seatbelt profiles are unavailable")
	}
	if err != nil {
		t.Fatalf("sandboxed command failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(inside); err != nil {
		t.Fatalf("workspace write was denied: %v", err)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("write outside workspace was not denied: %v", err)
	}
}

func shellTestQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
