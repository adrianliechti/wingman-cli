package lsp

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Project struct {
	Dir    string
	Server Server
}

type projectRoot = Project

// skippedDirs are never descended into. Dot-prefixed directories are skipped
// separately, so only their non-hidden counterparts are listed here.
var skippedDirs = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	"__pycache__":  true,
	"venv":         true,
	"target":       true,
	"build":        true,
	"dist":         true,
}

var projectBinDirs = []string{
	filepath.Join("node_modules", ".bin"),
	filepath.Join(".venv", "bin"),
	filepath.Join("venv", "bin"),
	filepath.Join(".venv", "Scripts"),
	filepath.Join("venv", "Scripts"),
	filepath.Join("vendor", "bin"),
}

func resolveCommand(dir, workingDir, command string) string {
	cur := filepath.Clean(dir)
	root := filepath.Clean(workingDir)
	for {
		for _, sub := range projectBinDirs {
			if found := findCommandIn([]string{filepath.Join(cur, sub)}, command); found != "" {
				return found
			}
		}
		if cur == root || !isSubPath(root, cur) {
			return ""
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

// userBinDirs lists fixed, user-owned tool-install directories searched when a
// server is not on PATH — the common case when wingman is launched from an IDE
// or app bundle with a minimal environment. Only these trusted locations are
// probed, never repo-controlled paths, and only for exact known server names.
var userBinDirs = sync.OnceValue(func() []string {
	var dirs []string

	add := func(path string) {
		if path == "" {
			return
		}
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			dirs = append(dirs, path)
		}
	}

	if gobin := os.Getenv("GOBIN"); gobin != "" {
		add(gobin)
	}
	if gopath := os.Getenv("GOPATH"); gopath != "" {
		add(filepath.Join(gopath, "bin"))
	}
	if pnpmHome := os.Getenv("PNPM_HOME"); pnpmHome != "" {
		add(pnpmHome)
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return dirs
	}

	add(filepath.Join(home, "go", "bin"))
	add(filepath.Join(home, ".cargo", "bin"))
	add(filepath.Join(home, ".local", "bin"))
	add(filepath.Join(home, ".dotnet", "tools"))
	add(filepath.Join(home, ".bun", "bin"))
	add(filepath.Join(home, ".deno", "bin"))
	add(filepath.Join(home, ".volta", "bin"))
	add(filepath.Join(home, ".asdf", "shims"))
	add(filepath.Join(home, ".local", "share", "mise", "shims"))
	add(filepath.Join(home, ".npm-global", "bin"))

	if runtime.GOOS == "windows" {
		add(filepath.Join(home, "scoop", "shims"))
		if appData := os.Getenv("APPDATA"); appData != "" {
			add(filepath.Join(appData, "npm"))
		}
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			add(filepath.Join(localAppData, "nvim-data", "mason", "bin"))
			add(filepath.Join(localAppData, "pnpm"))
			add(filepath.Join(localAppData, "Volta", "bin"))
			add(filepath.Join(localAppData, "Microsoft", "WinGet", "Links"))
		}
		if programData := os.Getenv("PROGRAMDATA"); programData != "" {
			add(filepath.Join(programData, "chocolatey", "bin"))
		}
		return dirs
	}

	add(filepath.Join(home, ".local", "share", "nvim", "mason", "bin"))
	add(filepath.Join(home, "Library", "pnpm"))
	add(filepath.Join(home, ".local", "share", "pnpm"))
	add("/opt/homebrew/bin")
	add("/usr/local/bin")
	add("/home/linuxbrew/.linuxbrew/bin")

	return dirs
})

func resolveUserCommand(command string) string {
	return findCommandIn(userBinDirs(), command)
}

func findCommandIn(dirs []string, command string) string {
	names := commandCandidates(runtime.GOOS, command)

	for _, dir := range dirs {
		for _, name := range names {
			candidate := filepath.Join(dir, name)
			if isExecutableFile(candidate) {
				return candidate
			}
		}
	}
	return ""
}

func commandCandidates(goos, command string) []string {
	if goos == "windows" {
		return []string{command + ".exe", command + ".cmd", command + ".bat", command}
	}
	return []string{command}
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0o111 != 0
}

func serverVersionSupported(server Server, command string) bool {
	if server.MinimumMajorVersion == 0 {
		return true
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, command, "--version").CombinedOutput()
	if err != nil {
		return false
	}

	for _, field := range strings.Fields(string(output)) {
		majorText := strings.SplitN(strings.TrimPrefix(field, "v"), ".", 2)[0]
		major, err := strconv.Atoi(majorText)
		if err == nil {
			return major >= server.MinimumMajorVersion
		}
	}
	return false
}

func detectAll(workingDir string) []projectRoot {
	index := indexWorkspace(workingDir)
	commands := make(map[string]string)
	versions := make(map[string]bool)

	var roots []projectRoot
	seen := make(map[string]bool)

	for _, project := range knownProjects {
		for _, dir := range projectDirs(index, project) {
			for _, candidate := range project.Servers {
				server, ok := resolveServer(workingDir, dir, candidate, commands, versions)
				if !ok {
					continue
				}

				root := projectRoot{Dir: dir, Server: server}
				if key := projectKey(root); !seen[key] {
					seen[key] = true
					roots = append(roots, root)
				}
				break
			}
		}
	}

	return roots
}

func projectDirs(index *workspaceIndex, project projectType) []string {
	var dirs []string
	seen := make(map[string]bool)

	for _, marker := range project.Markers {
		for _, entry := range index.matching(marker) {
			dir := filepath.Dir(entry.path)
			if seen[dir] {
				continue
			}
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}

	slices.Sort(dirs)

	return slices.DeleteFunc(dirs, func(dir string) bool {
		for _, exclude := range project.Excludes {
			if index.hasChild(dir, exclude) {
				return true
			}
		}
		return len(project.Requires) > 0 && !index.hasNestedFile(dir, project.Requires)
	})
}

func resolveServer(workingDir, dir string, candidate Server, commands map[string]string, versions map[string]bool) (Server, bool) {
	path := resolveCommand(dir, workingDir, candidate.Command)
	if path == "" {
		global, cached := commands[candidate.Command]
		if !cached {
			if _, err := exec.LookPath(candidate.Command); err == nil {
				global = candidate.Command
			} else {
				global = resolveUserCommand(candidate.Command)
			}
			commands[candidate.Command] = global
		}
		path = global
	}
	if path == "" {
		return Server{}, false
	}

	versionKey := path + "\x00" + strconv.Itoa(candidate.MinimumMajorVersion)
	supported, checked := versions[versionKey]
	if !checked {
		supported = serverVersionSupported(candidate, path)
		versions[versionKey] = supported
	}
	if !supported {
		return Server{}, false
	}

	candidate.Command = path
	return candidate, true
}

type workspaceEntry struct {
	path  string
	name  string
	isDir bool
}

// workspaceIndex holds one scan of the workspace, indexed so that marker
// lookups cost a map hit instead of a pass over every entry.
type workspaceIndex struct {
	byName map[string][]workspaceEntry
	byGlob map[string][]workspaceEntry
}

func (i *workspaceIndex) matching(pattern string) []workspaceEntry {
	if isGlob(pattern) {
		return i.byGlob[pattern]
	}
	return i.byName[pattern]
}

func (i *workspaceIndex) hasChild(dir, pattern string) bool {
	for _, entry := range i.matching(pattern) {
		if filepath.Dir(entry.path) == dir {
			return true
		}
	}
	return false
}

func (i *workspaceIndex) hasNestedFile(dir string, patterns []string) bool {
	for _, pattern := range patterns {
		for _, entry := range i.matching(pattern) {
			if !entry.isDir && isSubPath(dir, entry.path) {
				return true
			}
		}
	}
	return false
}

func indexWorkspace(workingDir string) *workspaceIndex {
	globs := markerGlobs()

	index := &workspaceIndex{
		byName: make(map[string][]workspaceEntry),
		byGlob: make(map[string][]workspaceEntry),
	}

	_ = filepath.WalkDir(workingDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || path == workingDir {
			return nil
		}

		name := entry.Name()
		item := workspaceEntry{path: path, name: name, isDir: entry.IsDir()}
		index.byName[name] = append(index.byName[name], item)

		for _, glob := range globs {
			if matchesName(glob, name) {
				index.byGlob[glob] = append(index.byGlob[glob], item)
			}
		}

		if entry.IsDir() && (skippedDirs[name] || strings.HasPrefix(name, ".")) {
			return filepath.SkipDir
		}
		return nil
	})

	return index
}

var markerGlobs = sync.OnceValue(func() []string {
	var globs []string
	for _, project := range knownProjects {
		for _, patterns := range [][]string{project.Markers, project.Requires, project.Excludes} {
			for _, pattern := range patterns {
				if isGlob(pattern) && !slices.Contains(globs, pattern) {
					globs = append(globs, pattern)
				}
			}
		}
	}
	return globs
})

func isGlob(pattern string) bool {
	return strings.ContainsAny(pattern, `*?[`)
}

func matchesName(pattern, name string) bool {
	matched, err := filepath.Match(pattern, name)
	return err == nil && matched
}

func isSubPath(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)

	if parent == child {
		return true
	}

	if !strings.HasSuffix(parent, string(filepath.Separator)) {
		parent += string(filepath.Separator)
	}

	return strings.HasPrefix(child, parent)
}
