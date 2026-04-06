package env

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	memoryFileName = "MEMORY.md"
	memoryMaxBytes = 25 * 1024 // 25KB
)

// MemoryContent reads MEMORY.md from the memory directory.
// Returns empty string if the file doesn't exist. Truncates to memoryMaxBytes.
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

	if len(content) > memoryMaxBytes {
		truncated := content[:memoryMaxBytes]
		if idx := strings.LastIndex(truncated, "\n"); idx > 0 {
			truncated = truncated[:idx]
		}

		content = truncated + "\n\n> WARNING: MEMORY.md exceeded 25KB and was truncated. Keep index entries concise; move detail into topic files."
	}

	return content
}
