// Package commandpath resolves executable tools from project and user install
// locations shared by language servers, debug adapters, and managed tools.
package commandpath

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var projectDirectories = []string{
	filepath.Join("node_modules", ".bin"),
	filepath.Join(".venv", "bin"), filepath.Join("venv", "bin"), filepath.Join("env", "bin"),
	filepath.Join(".venv", "Scripts"), filepath.Join("venv", "Scripts"), filepath.Join("env", "Scripts"),
	filepath.Join("vendor", "bin"),
}

func ProjectDirectories() []string { return append([]string(nil), projectDirectories...) }

func Candidates(goos, command string) []string {
	if goos == "windows" {
		return []string{command + ".exe", command + ".cmd", command + ".bat", command}
	}
	return []string{command}
}

func Executable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return runtime.GOOS == "windows" || info.Mode()&0o111 != 0
}

func Find(directories []string, command string) string {
	for _, directory := range directories {
		for _, name := range Candidates(runtime.GOOS, command) {
			candidate := filepath.Join(directory, name)
			if Executable(candidate) {
				return candidate
			}
		}
	}
	return ""
}

func ResolveProject(project, workspace, command string) string {
	project = filepath.Clean(project)
	workspace = filepath.Clean(workspace)
	rel, err := filepath.Rel(workspace, project)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	for {
		directories := make([]string, 0, len(projectDirectories))
		for _, directory := range projectDirectories {
			directories = append(directories, filepath.Join(project, directory))
		}
		if resolved := Find(directories, command); resolved != "" {
			return resolved
		}
		if project == workspace {
			return ""
		}
		parent := filepath.Dir(project)
		if parent == project {
			return ""
		}
		project = parent
	}
}

func LookPath(command string) (string, error) {
	if filepath.IsAbs(command) {
		if Executable(command) {
			return command, nil
		}
		return "", exec.ErrNotFound
	}
	if path, err := exec.LookPath(command); err == nil {
		return path, nil
	}
	if path := Find(userDirectories(), command); path != "" {
		return path, nil
	}
	return "", exec.ErrNotFound
}

func Resolve(command string) string {
	path, _ := LookPath(command)
	return path
}

func userDirectories() []string {
	var directories []string
	add := func(path string) {
		if path == "" {
			return
		}
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			directories = append(directories, path)
		}
	}
	add(os.Getenv("GOBIN"))
	if value := os.Getenv("GOPATH"); value != "" {
		add(filepath.Join(value, "bin"))
	}
	add(os.Getenv("PNPM_HOME"))

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return directories
	}
	for _, path := range []string{
		filepath.Join(home, "go", "bin"), filepath.Join(home, ".cargo", "bin"),
		filepath.Join(home, ".local", "bin"), filepath.Join(home, ".dotnet", "tools"),
		filepath.Join(home, ".bun", "bin"), filepath.Join(home, ".deno", "bin"),
		filepath.Join(home, ".volta", "bin"), filepath.Join(home, ".asdf", "shims"),
		filepath.Join(home, ".local", "share", "mise", "shims"), filepath.Join(home, ".npm-global", "bin"),
	} {
		add(path)
	}
	if runtime.GOOS == "windows" {
		add(filepath.Join(home, "scoop", "shims"))
		if value := os.Getenv("APPDATA"); value != "" {
			add(filepath.Join(value, "npm"))
		}
		if value := os.Getenv("LOCALAPPDATA"); value != "" {
			for _, path := range []string{
				filepath.Join(value, "nvim-data", "mason", "bin"), filepath.Join(value, "pnpm"),
				filepath.Join(value, "Volta", "bin"), filepath.Join(value, "Microsoft", "WinGet", "Links"),
			} {
				add(path)
			}
		}
		if value := os.Getenv("PROGRAMDATA"); value != "" {
			add(filepath.Join(value, "chocolatey", "bin"))
		}
		return directories
	}
	for _, path := range []string{
		filepath.Join(home, ".local", "share", "nvim", "mason", "bin"), filepath.Join(home, "Library", "pnpm"),
		filepath.Join(home, ".local", "share", "pnpm"), "/opt/homebrew/bin", "/usr/local/bin", "/home/linuxbrew/.linuxbrew/bin",
	} {
		add(path)
	}
	return directories
}
