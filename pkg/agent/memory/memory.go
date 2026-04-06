package memory

import (
	"bytes"
	_ "embed"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"
)

const (
	memoryName = "MEMORY.md"
	planName   = "PLAN.md"
	maxLines   = 200
)

//go:embed plan_template.txt
var planTemplateText string

var planTemplate = template.Must(template.New("plan").Parse(planTemplateText))

// NewPlanContent returns the default content for a new plan file.
func NewPlanContent() string {
	var buf bytes.Buffer

	data := struct{ Date string }{
		Date: time.Now().Format("2006-01-02 15:04"),
	}

	if err := planTemplate.Execute(&buf, data); err != nil {
		return ""
	}

	return buf.String()
}

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

// LoadMemory reads MEMORY.md from the given directory.
// Returns empty string if the file doesn't exist. Truncates to maxLines.
func LoadMemory(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, memoryName))
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

func PlanPath(dir string) string {
	return filepath.Join(dir, planName)
}

func LoadPlan(path string) string {
	data, err := os.ReadFile(path)
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
		content += "\n\n> WARNING: PLAN.md exceeded 200 lines and was truncated. Keep the working plan concise and current."
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
