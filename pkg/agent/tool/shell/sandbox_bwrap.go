//go:build linux

package shell

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func platformSandboxCommand(shell, command, workingDir string, writableRoots []string) (string, []string, error) {
	bwrap, err := findBubblewrap(writableRoots)
	if err != nil {
		return "", nil, err
	}
	return bwrap, bubblewrapArgs(shell, command, workingDir, writableRoots), nil
}

func bubblewrapArgs(shell, command, workingDir string, writableRoots []string) []string {
	args := []string{
		"--die-with-parent",
		"--new-session",
		"--unshare-user",
		"--unshare-pid",
		"--unshare-ipc",
		"--unshare-uts",
		"--cap-drop", "ALL",
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
	}
	for _, root := range writableRoots {
		args = append(args, "--bind", root, root)
	}
	return append(args, "--chdir", workingDir, "--", shell, "-c", command)
}

func findBubblewrap(writableRoots []string) (string, error) {
	for _, candidate := range []string{"/usr/bin/bwrap", "/bin/bwrap", "/usr/local/bin/bwrap"} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}

	path, err := exec.LookPath("bwrap")
	if err != nil {
		return "", fmt.Errorf("workspace shell sandbox requires bubblewrap (bwrap): %w", err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve bubblewrap path: %w", err)
	}
	path = canonicalPath(path)
	for _, root := range writableRoots {
		if pathWithin(root, path) {
			return "", fmt.Errorf("refusing bubblewrap executable from writable sandbox root: %s", path)
		}
	}
	return path, nil
}
