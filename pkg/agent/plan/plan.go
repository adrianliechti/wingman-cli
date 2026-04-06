package plan

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
	fileName = "PLAN.md"
	maxLines = 200
)

//go:embed plan_template.txt
var templateText string

var tmpl = template.Must(template.New("plan").Parse(templateText))

// Load reads PLAN.md from the given directory.
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
		content += "\n\n> WARNING: PLAN.md exceeded 200 lines and was truncated. Keep the working plan concise and current."
	}

	return content
}

// Ensure creates the plan file with default content if it doesn't exist.
// Returns the file path.
func Ensure(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}

	path := filepath.Join(dir, fileName)

	data, err := os.ReadFile(path)
	if err == nil && strings.TrimSpace(string(data)) != "" {
		return path, nil
	}

	if err := os.WriteFile(path, []byte(newContent()), 0644); err != nil {
		return "", err
	}

	return path, nil
}

func newContent() string {
	var buf bytes.Buffer

	data := struct{ Date string }{
		Date: time.Now().Format("2006-01-02 15:04"),
	}

	if err := tmpl.Execute(&buf, data); err != nil {
		return ""
	}

	return buf.String()
}
