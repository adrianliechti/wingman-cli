//go:build linux

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

func TestBubblewrapArgsMakeOnlySelectedRootsWritable(t *testing.T) {
	args := bubblewrapArgs("/bin/sh", "touch marker", "/repo/sub", []string{"/repo", "/tmp"})

	for _, sequence := range [][]string{
		{"--new-session", "--unshare-user", "--unshare-pid"},
		{"--ro-bind", "/", "/"},
		{"--cap-drop", "ALL"},
		{"--proc", "/proc"},
		{"--bind", "/repo", "/repo"},
		{"--bind", "/tmp", "/tmp"},
		{"--chdir", "/repo/sub", "--", "/bin/sh", "-c", "touch marker"},
	} {
		if !containsSequence(args, sequence) {
			t.Errorf("args do not contain %q: %q", sequence, args)
		}
	}
	if slices.Contains(args, "--unshare-net") {
		t.Fatalf("compatibility mode unexpectedly disables network: %q", args)
	}
}

func TestBubblewrapEnforcesWorkspaceWrites(t *testing.T) {
	if os.Getenv("WINGMAN_RUN_SANDBOX_INTEGRATION") != "1" {
		t.Skip("set WINGMAN_RUN_SANDBOX_INTEGRATION=1 to run the Bubblewrap integration test")
	}

	workspace := canonicalPath(t.TempDir())
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(home, fmt.Sprintf(".wingman-sandbox-probe-%d-%d", os.Getpid(), time.Now().UnixNano()))
	t.Cleanup(func() { _ = os.Remove(outside) })
	inside := filepath.Join(workspace, "inside")

	bwrap, err := findBubblewrap([]string{workspace})
	if err != nil {
		t.Fatal(err)
	}
	command := "touch " + shellTestQuoteLinux(inside) + " && ! touch " + shellTestQuoteLinux(outside)
	cmd := exec.Command(bwrap, bubblewrapArgs("/bin/sh", command, workspace, []string{workspace})...)
	cmd.Dir = workspace
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sandboxed command failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(inside); err != nil {
		t.Fatalf("workspace write was denied: %v", err)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("write outside workspace was not denied: %v", err)
	}
}

func shellTestQuoteLinux(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func containsSequence(values, sequence []string) bool {
	for i := 0; i+len(sequence) <= len(values); i++ {
		if slices.Equal(values[i:i+len(sequence)], sequence) {
			return true
		}
	}
	return false
}
