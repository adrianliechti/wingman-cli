package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// pathMarker brackets the PATH we echo from the login shell so we can
// recover it cleanly even when interactive rc files print banners or
// other noise to stdout.
const pathMarker = "__WINGMAN_PATH__"

// ensureShellPath repairs the truncated PATH that GUI apps inherit when
// launched from Finder/Dock on macOS (and some Linux desktops). launchd
// hands the process a minimal PATH (/usr/bin:/bin:/usr/sbin:/sbin), so
// CLIs installed via Homebrew, ~/.local/bin, etc. are invisible to
// exec.LookPath — which is how native ACP agents are detected by the shared
// agent registry. We query the user's interactive login shell for its real
// PATH and merge it into our environment.
//
// No-op on Windows. When launched from a terminal the shell PATH simply
// matches what we already have, so merging is harmless.
func ensureShellPath() {
	if runtime.GOOS == "windows" {
		return
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// -ilc: interactive login shell so both profile (.zprofile/.bash_profile)
	// and rc (.zshrc/.bashrc) PATH additions are sourced.
	// ${PATH} (braced) so the trailing marker isn't parsed as part of the
	// variable name — underscores are valid identifier characters.
	cmd := exec.CommandContext(ctx, shell, "-ilc", "echo "+pathMarker+"${PATH}"+pathMarker)
	out, err := cmd.Output()
	if err != nil {
		return
	}

	shellPath := extractMarked(string(out))
	if shellPath == "" {
		return
	}

	os.Setenv("PATH", mergePath(os.Getenv("PATH"), shellPath))
}

// extractMarked returns the text between the first and last pathMarker,
// or "" if the markers are missing.
func extractMarked(s string) string {
	start := strings.Index(s, pathMarker)
	end := strings.LastIndex(s, pathMarker)
	if start < 0 || end <= start {
		return ""
	}
	return strings.TrimSpace(s[start+len(pathMarker) : end])
}

// mergePath concatenates two PATH lists, preserving order and dropping
// duplicates. Existing entries keep priority over newly discovered ones.
func mergePath(existing, extra string) string {
	seen := make(map[string]bool)
	var dirs []string
	for _, p := range append(filepath.SplitList(existing), filepath.SplitList(extra)...) {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		dirs = append(dirs, p)
	}
	return strings.Join(dirs, string(os.PathListSeparator))
}
