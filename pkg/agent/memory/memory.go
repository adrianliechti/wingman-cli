package memory

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	fileName = "MEMORY.md"
	maxLines = 200
)

// Load reads MEMORY.md from the given directory.
// Returns empty string if the file doesn't exist. Truncates to maxLines.
func Load(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, fileName))
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
