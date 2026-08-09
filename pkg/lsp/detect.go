package lsp

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bmatcuk/doublestar/v4"
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

func hasFileMatching(fsys fs.FS, relDir string, patterns []string) bool {
	prefix := ""
	if relDir != "" && relDir != "." {
		prefix = filepath.ToSlash(relDir) + "/"
	}
	for _, pat := range patterns {
		matches, err := doublestar.Glob(fsys, prefix+"**/"+pat)
		if err == nil && len(matches) > 0 {
			return true
		}
	}
	return false
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
	resolveCache := make(map[string]string)

	fsys := filteredFS{root: workingDir}

	for _, pt := range knownProjects {
		for _, marker := range pt.Markers {
			matches, err := doublestar.Glob(fsys, "**/"+marker)
			if err != nil {
				continue
			}

			for _, match := range matches {
				dir := filepath.Join(workingDir, filepath.Dir(match))

				if excluded(dir, pt.Excludes) {
					continue
				}

				if len(pt.Requires) > 0 {
					relDir, err := filepath.Rel(workingDir, dir)
					if err != nil {
						continue
					}
					if !hasFileMatching(fsys, relDir, pt.Requires) {
						continue
					}
				}

				for _, candidate := range pt.Servers {
					key := dir + "\x00" + candidate.Command
					if seen[key] {
						continue
					}
					seen[key] = true

					path, cached := resolveCache[key]
					if !cached {
						if abs := resolveCommand(dir, workingDir, candidate.Command); abs != "" {
							path = abs
						} else if _, err := exec.LookPath(candidate.Command); err == nil {
							path = candidate.Command
						} else if abs := resolveUserCommand(candidate.Command); abs != "" {
							path = abs
						}
						resolveCache[key] = path
					}
					if path == "" {
						continue
					}
					if !serverVersionSupported(candidate, path) {
						continue
					}

					server := candidate
					server.Command = path
					roots = append(roots, projectRoot{Dir: dir, Server: server})
					break
				}
			}
		}
	}

	return roots
}

func excluded(dir string, excludes []string) bool {
	for _, marker := range excludes {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}

type filteredFS struct {
	root string
}

func (f filteredFS) Open(name string) (fs.File, error) {
	return os.Open(filepath.Join(f.root, name))
}

func (f filteredFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := os.ReadDir(filepath.Join(f.root, name))
	if err != nil {
		return nil, err
	}

	filtered := entries[:0]
	for _, e := range entries {
		if e.IsDir() && (ignoredDirs[e.Name()] || strings.HasPrefix(e.Name(), ".")) {
			continue
		}
		filtered = append(filtered, e)
	}
	return filtered, nil
}

func (f filteredFS) Stat(name string) (fs.FileInfo, error) {
	return os.Stat(filepath.Join(f.root, name))
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
