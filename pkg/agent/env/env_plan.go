package env

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
	planFileName = "PLAN.md"
	planMaxLines = 200
)

//go:embed plan_template.txt
var planTemplateText string

var planTemplate = template.Must(template.New("plan").Parse(planTemplateText))

// PlanContent reads PLAN.md from the memory directory.
// Returns empty string if the file doesn't exist. Truncates to planMaxLines.
func (e *Environment) PlanContent() string {
	dir := e.MemoryDir()
	if dir == "" {
		return ""
	}

	data, err := os.ReadFile(filepath.Join(dir, planFileName))
	if err != nil {
		return ""
	}

	content := strings.TrimSpace(string(data))
	if content == "" {
		return ""
	}

	lines := strings.Split(content, "\n")
	if len(lines) > planMaxLines {
		lines = lines[:planMaxLines]
		content = strings.Join(lines, "\n")
		content += "\n\n> WARNING: PLAN.md exceeded 200 lines and was truncated. Keep the working plan concise and current."
	}

	return content
}

// EnsurePlan creates the plan file with default content if it doesn't exist.
// Returns the absolute file path.
func (e *Environment) EnsurePlan() (string, error) {
	dir := e.MemoryDir()
	if dir == "" {
		return "", os.ErrNotExist
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}

	path := filepath.Join(dir, planFileName)

	data, err := os.ReadFile(path)
	if err == nil && strings.TrimSpace(string(data)) != "" {
		return path, nil
	}

	if err := os.WriteFile(path, []byte(newPlanContent()), 0644); err != nil {
		return "", err
	}

	return path, nil
}

// EnterPlanMode ensures the plan file exists and enables plan mode.
// Returns the absolute path to the plan file.
func (e *Environment) EnterPlanMode() (string, error) {
	path, err := e.EnsurePlan()
	if err != nil {
		return "", err
	}

	e.planFile = path

	return path, nil
}

// ExitPlanMode disables plan mode.
func (e *Environment) ExitPlanMode() {
	e.planFile = ""
}

// IsPlanning reports whether plan mode is active.
func (e *Environment) IsPlanning() bool {
	return e.planFile != ""
}

// PlanFile returns the absolute path to the current plan file.
func (e *Environment) PlanFile() string {
	return e.planFile
}

func newPlanContent() string {
	var buf bytes.Buffer

	data := struct{ Date string }{
		Date: time.Now().Format("2006-01-02 15:04"),
	}

	if err := planTemplate.Execute(&buf, data); err != nil {
		return ""
	}

	return buf.String()
}
