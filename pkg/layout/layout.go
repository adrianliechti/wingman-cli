// Package layout resolves Wingman's user-data path and ordered project and
// personal configuration roots.
package layout

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	wingmanHomeEnv = "WINGMAN_HOME"
	wingmanDir     = ".wingman"
	agentsDir      = ".agents"
	claudeDir      = ".claude"
)

// WingmanPath returns an absolute path beneath the root for Wingman-owned user
// data. WINGMAN_HOME replaces the default ~/.wingman directory as a unit.
func WingmanPath(parts ...string) (string, error) {
	root := os.Getenv(wingmanHomeEnv)
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(home, wingmanDir)
	}

	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}

	path := filepath.Join(append([]string{root}, parts...)...)
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes WINGMAN_HOME: %s", path)
	}

	return path, nil
}

// ProjectRoots returns the per-project directories holding name, in precedence
// order.
func ProjectRoots(workDir, name string) []string {
	roots := make([]string, 0, 3)

	for _, dir := range [...]string{wingmanDir, agentsDir, claudeDir} {
		roots = append(roots, filepath.Join(workDir, dir, name))
	}

	return roots
}

// PersonalRoots returns the per-user directories holding name, in precedence
// order. WINGMAN_HOME affects the Wingman root only; compatible .agents and
// .claude roots remain relative to the operating-system home directory.
func PersonalRoots(name string) []string {
	roots := make([]string, 0, 3)

	if path, err := WingmanPath(name); err == nil {
		roots = append(roots, path)
	}

	home, err := os.UserHomeDir()

	if err != nil {
		return roots
	}

	for _, dir := range [...]string{agentsDir, claudeDir} {
		roots = append(roots, filepath.Join(home, dir, name))
	}

	return roots
}

// Roots returns the project roots followed by the personal roots, which is the
// full precedence order for name.
func Roots(workDir, name string) []string {
	return append(ProjectRoots(workDir, name), PersonalRoots(name)...)
}
