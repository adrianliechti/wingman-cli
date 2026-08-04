package terminal

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

type Shell struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Shells worth offering next to the user's own $SHELL. /etc/shells is
// deliberately not used as a source: it lists login shells, which on many
// systems includes entries that make for a broken terminal (git-shell, rbash,
// screen) alongside ones nobody picks on purpose (csh, dash, ksh).
var unixCandidates = []string{"zsh", "bash", "fish", "pwsh", "nu"}

var windowsCandidates = []string{"pwsh.exe", "powershell.exe", "cmd.exe"}

// Shells lists the shells available on this host, the default one first.
func Shells() []Shell {
	var out []Shell
	seen := map[string]bool{}

	add := func(path string) {
		if path == "" {
			return
		}
		resolved, err := exec.LookPath(path)
		if err != nil {
			return
		}
		if abs, err := filepath.Abs(resolved); err == nil {
			resolved = abs
		}
		// Deduplicated by name as well as by path: a second "bash" from another
		// prefix is indistinguishable in the picker, and $SHELL is listed first
		// so its variant is the one that wins.
		name := shellName(resolved)
		if seen[resolved] || seen[name] {
			return
		}
		seen[resolved], seen[name] = true, true
		out = append(out, Shell{ID: resolved, Name: name})
	}

	add(DefaultShell())

	candidates := unixCandidates
	if runtime.GOOS == "windows" {
		candidates = windowsCandidates
	}
	for _, c := range candidates {
		add(c)
	}
	return out
}

func DefaultShell() string {
	if runtime.GOOS == "windows" {
		for _, c := range windowsCandidates {
			if path, err := exec.LookPath(c); err == nil {
				return path
			}
		}
		return "powershell.exe"
	}
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	return "/bin/sh"
}

// resolveShell maps a requested shell onto one of the detected entries so the
// API can never spawn an arbitrary binary.
func resolveShell(requested string) (string, bool) {
	shells := Shells()
	if requested == "" {
		if len(shells) == 0 {
			return DefaultShell(), true
		}
		return shells[0].ID, true
	}
	idx := slices.IndexFunc(shells, func(s Shell) bool {
		return s.ID == requested || s.Name == requested
	})
	if idx < 0 {
		return "", false
	}
	return shells[idx].ID, true
}

func shellName(path string) string {
	name := filepath.Base(path)
	if runtime.GOOS == "windows" {
		name = strings.TrimSuffix(strings.ToLower(name), ".exe")
	}
	return name
}
