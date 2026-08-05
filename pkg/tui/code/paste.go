package code

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
)

func normalizePastedText(text string) string {
	text = ansi.Strip(text)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, text)
}

func normalizePastedSearchQuery(text string) string {
	return strings.Join(strings.Fields(normalizePastedText(text)), " ")
}

func appendPastedSearchQuery(current, pasted string) string {
	pasted = normalizePastedSearchQuery(pasted)
	if pasted == "" {
		return current
	}
	if current != "" && strings.TrimRightFunc(current, unicode.IsSpace) == current {
		current += " "
	}
	return current + pasted
}

// detectFilePaths returns the file paths in text, but only when the entire
// paste is paths — a paste mixing prose and paths must stay text, not
// silently become attachments.
func detectFilePaths(text, workingDir string) []string {
	lines := strings.Split(strings.TrimSpace(text), "\n")

	var paths []string

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if len(line) >= 2 {
			if (line[0] == '"' && line[len(line)-1] == '"') || (line[0] == '\'' && line[len(line)-1] == '\'') {
				line = line[1 : len(line)-1]
			}
		}

		if line == "" {
			continue
		}

		if !isLikelyFilePath(line) {
			return nil
		}

		resolved := resolveFilePath(line, workingDir)
		if resolved == "" {
			return nil
		}

		info, err := os.Stat(resolved)
		if err != nil || info.IsDir() {
			return nil
		}

		paths = append(paths, resolved)
	}

	return paths
}

func isLikelyFilePath(s string) bool {
	if strings.ContainsAny(s, "{}<>|") {
		return false
	}

	if !strings.Contains(s, "/") && !strings.Contains(s, "\\") {
		return false
	}

	if filepath.IsAbs(s) {
		return true
	}

	if strings.HasPrefix(s, "~/") || strings.HasPrefix(s, `~\`) {
		return true
	}

	if strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") ||
		strings.HasPrefix(s, `.\`) || strings.HasPrefix(s, `..\`) {
		return true
	}

	return false
}

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

func normalizeFilePath(absPath, workingDir string) string {
	rel, err := filepath.Rel(workingDir, absPath)
	if err != nil {
		return absPath
	}

	if strings.HasPrefix(rel, "..") {
		return absPath
	}

	return rel
}
