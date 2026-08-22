package tooling

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Source identifies where a command was found. Project and system tools are
// intentionally preferred over Wingman's managed fallback.
type Source string

const (
	SourceProject Source = "project"
	SourceSystem  Source = "system"
	SourceManaged Source = "managed"
)

type Resolution struct {
	Path    string
	Source  Source
	Project string
}

// Resolver applies the command precedence shared by LSP, DAP, and managed
// tool planning. Lookup and Managed are injectable so callers can cache or
// test discovery without changing the ordering rules.
type Resolver struct {
	Workspace string
	Lookup    func(string) string
	Managed   func(string) string
}

func (r Resolver) Candidates(projects []string, command string) []Resolution {
	var result []Resolution
	seen := make(map[string]bool)
	add := func(path string, source Source, project string) {
		if path == "" {
			return
		}
		path = filepath.Clean(path)
		if seen[path] {
			return
		}
		seen[path] = true
		result = append(result, Resolution{Path: path, Source: source, Project: project})
	}

	if r.Workspace != "" {
		for _, project := range projects {
			add(ResolveProject(project, r.Workspace, command), SourceProject, filepath.Clean(project))
		}
	}

	lookup := r.Lookup
	if lookup == nil {
		lookup = Resolve
	}
	add(lookup(command), SourceSystem, "")
	if r.Managed != nil {
		add(r.Managed(command), SourceManaged, "")
	}
	return result
}

func (r Resolver) Resolve(projects []string, command string, accept func(string) bool) Resolution {
	for _, candidate := range r.Candidates(projects, command) {
		if accept == nil || accept(candidate.Path) {
			return candidate
		}
	}
	return Resolution{}
}

type versionProbeKey struct {
	path     string
	minimum  int
	modified int64
}

var versionProbes sync.Map

// ProbeExecutes requests only a successful --version run instead of a version
// floor. It filters launchers that exist but cannot run, such as a rustup
// proxy whose component is not installed.
const ProbeExecutes = -1

// MajorVersionAtLeast probes a command's conventional --version output.
// Results are cached per executable modification time because probes spawn a
// process and are repeated by detection refreshes.
func MajorVersionAtLeast(ctx context.Context, command string, minimum int) bool {
	if minimum == 0 {
		return true
	}
	var key versionProbeKey
	cacheable := false
	if filepath.IsAbs(command) {
		if info, err := os.Stat(command); err == nil {
			key = versionProbeKey{path: command, minimum: minimum, modified: info.ModTime().UnixNano()}
			cacheable = true
			if cached, ok := versionProbes.Load(key); ok {
				return cached.(bool)
			}
		}
	}
	process := exec.CommandContext(ctx, command, "--version")
	process.Env = Environment(command, os.Environ())
	process.WaitDelay = 100 * time.Millisecond
	output, err := process.CombinedOutput()
	result := false
	if err == nil && minimum < 0 {
		result = true
	} else if err == nil {
		for _, field := range strings.Fields(string(output)) {
			majorText := strings.SplitN(strings.TrimPrefix(field, "v"), ".", 2)[0]
			major, atoiErr := strconv.Atoi(majorText)
			if atoiErr == nil {
				result = major >= minimum
				break
			}
		}
	}
	if cacheable && ctx.Err() == nil {
		versionProbes.Store(key, result)
	}
	return result
}
