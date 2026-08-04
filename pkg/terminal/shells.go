package terminal

import (
	"bufio"
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

var unixCandidates = []string{"zsh", "bash", "fish", "nu", "ksh", "dash", "sh"}

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
		key := resolved
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, Shell{ID: resolved, Name: shellName(resolved)})
	}

	add(DefaultShell())

	if runtime.GOOS == "windows" {
		for _, c := range windowsCandidates {
			add(c)
		}
		return out
	}

	for _, path := range etcShells() {
		add(path)
	}
	for _, c := range unixCandidates {
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

func etcShells() []string {
	f, err := os.Open("/etc/shells")
	if err != nil {
		return nil
	}
	defer f.Close()

	out := []string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}
