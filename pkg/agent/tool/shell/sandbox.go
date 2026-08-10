package shell

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

const workspaceSandboxActiveEnv = "WINGMAN_SANDBOX_ACTIVE"

// workspaceSandboxEnabled is deliberately opt-in while the native backends
// mature. WINGMAN_SANDBOX=off keeps its existing meaning for the file tools;
// workspace (or workspace-write) adds an OS boundary around shell processes.
func workspaceSandboxEnabled() bool {
	// Native sandboxes are inherited by descendants. Avoid trying to apply a
	// second Seatbelt profile or Bubblewrap namespace when a sandboxed command
	// starts another Wingman shell runner.
	if os.Getenv(workspaceSandboxActiveEnv) == "1" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("WINGMAN_SANDBOX"))) {
	case "workspace", "workspace-write":
		return true
	default:
		return false
	}
}

func pathWithin(root, candidate string) bool {
	root = canonicalPath(root)
	candidate = canonicalPath(candidate)

	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func canonicalPath(path string) string {
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

func buildSandboxedCommand(ctx context.Context, shell, command, workingDir string, opts sandboxOptions) (*exec.Cmd, error) {
	workspaceDir := canonicalPath(opts.WorkspaceDir)
	workingDir = canonicalPath(workingDir)
	for label, path := range map[string]string{
		"workspace": workspaceDir,
		"workdir":   workingDir,
	} {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("sandbox %s %q is not an accessible directory", label, path)
		}
	}
	if !pathWithin(workspaceDir, workingDir) {
		return nil, fmt.Errorf("workdir %q is outside sandbox workspace %q", workingDir, workspaceDir)
	}

	shellPath, err := exec.LookPath(shell)
	if err != nil {
		return nil, fmt.Errorf("resolve sandbox shell %q: %w", shell, err)
	}
	shellPath, err = filepath.Abs(shellPath)
	if err != nil {
		return nil, fmt.Errorf("resolve sandbox shell %q: %w", shell, err)
	}
	shell = canonicalPath(shellPath)

	writableRoots := writableRootsFor(workspaceDir, opts.ExtraWritableRoots)

	path, args, err := platformSandboxCommand(shell, command, workingDir, writableRoots)
	if err != nil {
		return nil, err
	}
	return exec.CommandContext(ctx, path, args...), nil
}

// writableRootsFor assembles the sandbox's writable roots: the workspace,
// any extra roots the caller needs (e.g. the memory directory), and the OS
// temp dirs every command may stage files under. Entries that don't resolve
// to an accessible directory are dropped rather than failing the command —
// an extra root is a nice-to-have, not a precondition like the workspace.
func writableRootsFor(workspaceDir string, extraWritableRoots []string) []string {
	writableRoots := []string{canonicalPath(workspaceDir)}
	for _, path := range append(append([]string{}, extraWritableRoots...), os.TempDir(), "/tmp") {
		if path == "" {
			continue
		}
		path = canonicalPath(path)
		if info, err := os.Stat(path); err == nil && info.IsDir() && !slices.Contains(writableRoots, path) {
			writableRoots = append(writableRoots, path)
		}
	}
	return writableRoots
}
