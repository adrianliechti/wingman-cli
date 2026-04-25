package app

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/rivo/tview"
)

// detectFilePaths checks if text contains file paths that exist on disk.
// Returns resolved absolute paths for any detected files.
func detectFilePaths(text, workingDir string) []string {
	lines := strings.Split(strings.TrimSpace(text), "\n")

	var paths []string

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Strip surrounding quotes
		if len(line) >= 2 {
			if (line[0] == '"' && line[len(line)-1] == '"') || (line[0] == '\'' && line[len(line)-1] == '\'') {
				line = line[1 : len(line)-1]
			}
		}

		if line == "" {
			continue
		}

		if !isLikelyFilePath(line) {
			continue
		}

		resolved := resolveFilePath(line, workingDir)
		if resolved == "" {
			continue
		}

		info, err := os.Stat(resolved)
		if err != nil || info.IsDir() {
			continue
		}

		paths = append(paths, resolved)
	}

	return paths
}

// isLikelyFilePath returns true if the string looks like a file path.
func isLikelyFilePath(s string) bool {
	// Reject strings with characters unlikely in file paths
	if strings.ContainsAny(s, "{}<>|") {
		return false
	}

	// Reject multi-word text that doesn't look like a path
	if !strings.Contains(s, "/") && !strings.Contains(s, "\\") {
		return false
	}

	// Absolute path (Unix `/foo`, Windows `C:\foo` or drive-relative `\foo`)
	if filepath.IsAbs(s) {
		return true
	}

	// Home-relative
	if strings.HasPrefix(s, "~/") || strings.HasPrefix(s, `~\`) {
		return true
	}

	// Relative paths
	if strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") ||
		strings.HasPrefix(s, `.\`) || strings.HasPrefix(s, `..\`) {
		return true
	}

	return false
}

// resolveFilePath resolves a file path to an absolute path.
func resolveFilePath(path, workingDir string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}

		path = filepath.Join(home, path[2:])
	}

	if !filepath.IsAbs(path) {
		path = filepath.Join(workingDir, path)
	}

	return filepath.Clean(path)
}

// normalizeFilePath converts an absolute path to workspace-relative if it falls within the workspace.
func normalizeFilePath(absPath, workingDir string) string {
	rel, err := filepath.Rel(workingDir, absPath)
	if err != nil {
		return absPath
	}

	// If the relative path escapes the workspace, keep absolute
	if strings.HasPrefix(rel, "..") {
		return absPath
	}

	return rel
}

// pasteInterceptRoot wraps a tview.Primitive to intercept bracketed paste events.
type pasteInterceptRoot struct {
	tview.Primitive
	intercept func(text string) bool
}

func (p *pasteInterceptRoot) PasteHandler() func(string, func(tview.Primitive)) {
	inner := p.Primitive.PasteHandler()

	return func(text string, setFocus func(tview.Primitive)) {
		if p.intercept != nil && p.intercept(text) {
			return
		}

		if inner != nil {
			inner(text, setFocus)
		}
	}
}
