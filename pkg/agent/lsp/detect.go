package lsp

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// projectRoot represents a detected project and its available LSP servers.
type projectRoot struct {
	Dir     string
	Servers []Server
}

// ignoredDirs are directories skipped during project detection.
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

// detectionResult contains discovered project roots and any projects
// where no LSP server binary was found.
type detectionResult struct {
	Roots   []projectRoot
	Missing []MissingServer
}

// MissingServer describes a detected project type with no available LSP server.
type MissingServer struct {
	ProjectName string // e.g. "go", "typescript"
	Servers     []string // candidate commands that were not found
}

// detectAll scans the working directory tree for known project markers and
// returns all detected project roots with their available LSP servers.
func detectAll(workingDir string) detectionResult {
	var roots []projectRoot
	seen := make(map[string]bool)          // dir+command dedup
	lookPathCache := make(map[string]bool) // command -> available
	detectedTypes := make(map[string]bool) // project types where markers were found
	resolvedTypes := make(map[string]bool) // project types with at least one server

	fsys := filteredFS{root: workingDir}

	for _, pt := range knownProjects {
		for _, marker := range pt.Markers {
			pattern := "**/" + marker

			matches, err := doublestar.Glob(fsys, pattern)
			if err != nil {
				continue
			}

			for _, match := range matches {
				dir := filepath.Join(workingDir, filepath.Dir(match))

				if excluded(dir, pt.Excludes) {
					continue
				}

				detectedTypes[pt.Name] = true

				for _, candidate := range pt.Servers {
					key := dir + "\x00" + candidate.Command
					if seen[key] {
						continue
					}
					seen[key] = true

					available, cached := lookPathCache[candidate.Command]
					if !cached {
						_, err := exec.LookPath(candidate.Command)
						available = err == nil
						lookPathCache[candidate.Command] = available
					}
					if !available {
						continue
					}

					roots = append(roots, projectRoot{
						Dir:     dir,
						Servers: []Server{candidate},
					})
					resolvedTypes[pt.Name] = true
					break // first available server per project type per dir
				}
			}
		}
	}

	var missing []MissingServer
	for _, pt := range knownProjects {
		if !detectedTypes[pt.Name] || resolvedTypes[pt.Name] {
			continue
		}
		var cmds []string
		for _, s := range pt.Servers {
			cmds = append(cmds, s.Command)
		}
		missing = append(missing, MissingServer{
			ProjectName: pt.Name,
			Servers:     cmds,
		})
	}

	return detectionResult{Roots: roots, Missing: missing}
}

// excluded returns true if any of the exclude markers exist in dir.
func excluded(dir string, excludes []string) bool {
	for _, marker := range excludes {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}

// filteredFS wraps os.DirFS but skips ignored directories.
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

// hasLanguage checks if the language/extension is in the list.
func hasLanguage(languages []string, ext string) bool {
	return slices.Contains(languages, ext)
}

// isSubPath checks if child is under parent directory.
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
