package lsp

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// FindServer finds an appropriate LSP server for the given file by walking up
// the directory tree from the file's location, looking for project markers.
// It stops at workingDir and won't search above it.
func FindServer(workingDir, filePath string) *Server {
	// Get file extension
	ext := strings.TrimPrefix(filepath.Ext(filePath), ".")
	if ext == "" {
		return nil
	}

	// Start from the file's directory
	dir := filepath.Dir(filePath)

	// Ensure we're within workingDir
	if !isSubPath(workingDir, dir) {
		dir = workingDir
	}

	// Walk up the directory tree
	for {
		// Check each project type for markers in this directory
		for _, pt := range knownProjects {
			if !hasAnyFile(dir, pt.Markers) {
				continue
			}

			// Found a project marker, check if it has a server for our file type
			for _, candidate := range pt.Servers {
				// Check if this server handles our file extension
				if !hasLanguage(candidate.Languages, ext) {
					continue
				}

				// Check if binary is available
				if _, err := exec.LookPath(candidate.Command); err != nil {
					continue
				}

				// Found a matching server
				return &candidate
			}
		}

		// Stop if we've reached the working directory
		if dir == workingDir {
			break
		}

		// Move up one directory
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root
			break
		}
		dir = parent

		// Don't go above workingDir
		if !isSubPath(workingDir, dir) {
			break
		}
	}

	return nil
}

// hasAnyFile checks if any of the named files exist in the directory.
func hasAnyFile(dir string, names []string) bool {
	for _, name := range names {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

// hasLanguage checks if the language/extension is in the list.
func hasLanguage(languages []string, ext string) bool {
	for _, lang := range languages {
		if lang == ext {
			return true
		}
	}
	return false
}

// isSubPath checks if child is under parent directory.
func isSubPath(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)

	if parent == child {
		return true
	}

	// Ensure parent ends with separator for proper prefix matching
	if !strings.HasSuffix(parent, string(filepath.Separator)) {
		parent += string(filepath.Separator)
	}

	return strings.HasPrefix(child, parent)
}
