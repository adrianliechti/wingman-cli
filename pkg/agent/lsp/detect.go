package lsp

import (
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

// detectAll scans the working directory tree for known project markers and
// returns all detected project roots with their available LSP servers.
func detectAll(workingDir string) []projectRoot {
	var roots []projectRoot
	seen := make(map[string]bool) // dir+command dedup

	fsys := os.DirFS(workingDir)

	for _, pt := range knownProjects {
		for _, marker := range pt.Markers {
			pattern := "**/" + marker

			matches, err := doublestar.Glob(fsys, pattern)
			if err != nil {
				continue
			}

			for _, match := range matches {
				dir := filepath.Join(workingDir, filepath.Dir(match))

				for _, candidate := range pt.Servers {
					key := dir + "\x00" + candidate.Command
					if seen[key] {
						continue
					}
					seen[key] = true

					if _, err := exec.LookPath(candidate.Command); err != nil {
						continue
					}

					roots = append(roots, projectRoot{
						Dir:     dir,
						Servers: []Server{candidate},
					})
					break // first available server per project type per dir
				}
			}
		}
	}

	return roots
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

