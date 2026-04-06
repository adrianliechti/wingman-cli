package env

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	memoryFileName = "MEMORY.md"
	memoryMaxLines = 200
)

// MemoryContent reads MEMORY.md from the memory directory.
// Returns empty string if the file doesn't exist. Truncates to memoryMaxLines.
func (e *Environment) MemoryContent() string {
	dir := e.MemoryDir()
	if dir == "" {
		return ""
	}

	data, err := os.ReadFile(filepath.Join(dir, memoryFileName))
	if err != nil {
		return ""
	}

	content := strings.TrimSpace(string(data))
	if content == "" {
		return ""
	}

	lines := strings.Split(content, "\n")
	if len(lines) > memoryMaxLines {
		lines = lines[:memoryMaxLines]
		content = strings.Join(lines, "\n")
		content += "\n\n> WARNING: MEMORY.md exceeded 200 lines and was truncated. Keep index entries concise; move detail into topic files."
	}

	return content
}
