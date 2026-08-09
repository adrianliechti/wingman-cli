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

type projectRoot struct {
	Dir    string
	Server Server
}

var ignoredDirs = map[string]bool{
	".git":         true,
	".hg":          true,
	".svn":         true,
	"node_modules": true,
	"vendor":       true,
	"__pycache__":  true,
	".venv":        true,
	"venv":         true,
	"target":       true,
	"build":        true,
	"dist":         true,
	".next":        true,
	".nuxt":        true,
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
	var roots []projectRoot
	seen := make(map[string]bool)
	globalCommandCache := make(map[string]string)
	versionCache := make(map[string]bool)
	entries := scanWorkspaceEntries(workingDir)
	entrySet := make(map[string]bool, len(entries))
	for _, entry := range entries {
		entrySet[filepath.Clean(entry.path)] = true
	}

	for _, pt := range knownProjects {
		projectDirs := make(map[string]bool)
		for _, marker := range pt.Markers {
			for _, entry := range entries {
				if !matchesName(marker, entry.name) {
					continue
				}
				projectDirs[filepath.Dir(entry.path)] = true
			}
		}

		dirs := make([]string, 0, len(projectDirs))
		for dir := range projectDirs {
			dirs = append(dirs, dir)
		}
		slices.Sort(dirs)
		for _, dir := range dirs {
			if excludedEntry(entrySet, dir, pt.Excludes) {
				continue
			}

			if len(pt.Requires) > 0 && !hasRequiredEntry(entries, dir, pt.Requires) {
				continue
			}

			for _, candidate := range pt.Servers {
				key := dir + "\x00" + serverKey(candidate)
				if seen[key] {
					continue
				}
				seen[key] = true

				path := resolveCommand(dir, workingDir, candidate.Command)
				if path == "" {
					var cached bool
					path, cached = globalCommandCache[candidate.Command]
					if !cached {
						if _, err := exec.LookPath(candidate.Command); err == nil {
							path = candidate.Command
						} else {
							path = resolveUserCommand(candidate.Command)
						}
						globalCommandCache[candidate.Command] = path
					}
				}
				if path == "" {
					continue
				}
				versionKey := path + "\x00" + strconv.Itoa(candidate.MinimumMajorVersion)
				supported, checked := versionCache[versionKey]
				if !checked {
					supported = serverVersionSupported(candidate, path)
					versionCache[versionKey] = supported
				}
				if !supported {
					continue
				}

				server := candidate
				server.Command = path
				roots = append(roots, projectRoot{Dir: dir, Server: server})
				break
			}
		}
	}

	return roots
}

type workspaceEntry struct {
	path  string
	name  string
	isDir bool
}

func scanWorkspaceEntries(workingDir string) []workspaceEntry {
	var entries []workspaceEntry
	_ = filepath.WalkDir(workingDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path != workingDir {
			entries = append(entries, workspaceEntry{path: path, name: entry.Name(), isDir: entry.IsDir()})
		}
		if entry.IsDir() && path != workingDir && (ignoredDirs[entry.Name()] || strings.HasPrefix(entry.Name(), ".")) {
			return filepath.SkipDir
		}
		return nil
	})
	return entries
}

func matchesName(pattern, name string) bool {
	matched, err := filepath.Match(pattern, name)
	return err == nil && matched
}

func hasRequiredEntry(entries []workspaceEntry, dir string, patterns []string) bool {
	for _, entry := range entries {
		if entry.isDir || !isSubPath(dir, entry.path) {
			continue
		}
		for _, pattern := range patterns {
			if matchesName(pattern, entry.name) {
				return true
			}
		}
	}
	return false
}

func excludedEntry(entries map[string]bool, dir string, excludes []string) bool {
	for _, marker := range excludes {
		if entries[filepath.Clean(filepath.Join(dir, marker))] {
			return true
		}
	}
	return false
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
