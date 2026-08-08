// Package layout owns the configuration directory names Wingman reads and the
// order they resolve in, so skills, plugins and agent definitions cannot drift
// apart. Callers scan the returned roots in order and keep the first entry for
// a given name; project roots always precede personal ones.
package layout

import (
	"os"
	"path/filepath"
)

// Dirs are the configuration directory names, highest precedence first.
var Dirs = []string{".wingman", ".agents", ".claude"}

// ProjectRoots returns the per-project directories holding name, in precedence
// order.
func ProjectRoots(workDir, name string) []string {
	roots := make([]string, 0, len(Dirs))

	for _, dir := range Dirs {
		roots = append(roots, filepath.Join(workDir, dir, name))
	}

	return roots
}

// PersonalRoots returns the per-user directories holding name, in precedence
// order. It returns nil when the home directory cannot be determined.
func PersonalRoots(name string) []string {
	home, err := os.UserHomeDir()

	if err != nil {
		return nil
	}

	return ProjectRoots(home, name)
}

// Roots returns the project roots followed by the personal roots, which is the
// full precedence order for name.
func Roots(workDir, name string) []string {
	return append(ProjectRoots(workDir, name), PersonalRoots(name)...)
}
