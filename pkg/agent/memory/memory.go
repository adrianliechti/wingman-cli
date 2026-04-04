package memory

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	entrypointName = "MEMORY.md"
	maxLines       = 200
)

// Dir returns the memory directory path for a given working directory.
// The path is ~/.wingman/projects/<sanitized-wd>/memory/.
func Dir(workingDir string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}

	sanitized := sanitizePath(workingDir)
	return filepath.Join(home, ".wingman", "projects", sanitized, "memory")
}

// EnsureDir creates the memory directory if it doesn't exist.
func EnsureDir(dir string) error {
	return os.MkdirAll(dir, 0755)
}

// LoadEntrypoint reads MEMORY.md from the given directory.
// Returns empty string if the file doesn't exist. Truncates to maxLines.
func LoadEntrypoint(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, entrypointName))
	if err != nil {
		return ""
	}

	content := strings.TrimSpace(string(data))
	if content == "" {
		return ""
	}

	lines := strings.Split(content, "\n")
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		content = strings.Join(lines, "\n")
		content += "\n\n> WARNING: MEMORY.md exceeded 200 lines and was truncated. Keep index entries concise; move detail into topic files."
	}

	return content
}

// sanitizePath converts an absolute path to a safe directory name
// by replacing path separators with underscores and stripping the leading separator.
func sanitizePath(path string) string {
	path = filepath.Clean(path)
	path = strings.TrimPrefix(path, string(filepath.Separator))
	return strings.ReplaceAll(path, string(filepath.Separator), "_")
}
