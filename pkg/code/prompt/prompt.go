package prompt

import (
	"bytes"
	_ "embed"
	"strings"
	"text/template"
)

//go:embed mode_agent.txt
var Instructions string

//go:embed mode_plan.txt
var Planning string

//go:embed section_environment.txt
var sectionEnvironment string

//go:embed section_memory.txt
var sectionMemory string

//go:embed section_plan.txt
var sectionPlan string

//go:embed section_skills.txt
var sectionSkills string

//go:embed section_project.txt
var sectionProject string

//go:embed section_bridge.txt
var sectionBridge string


var sectionTemplates = []struct {
	title string
	tmpl  *template.Template
}{
	{"Environment", template.Must(template.New("environment").Parse(sectionEnvironment))},
	{"Memory", template.Must(template.New("memory").Parse(sectionMemory))},
	{"Session Plan", template.Must(template.New("plan").Parse(sectionPlan))},
	{"Skills", template.Must(template.New("skills").Parse(sectionSkills))},
	{"Project Guidelines", template.Must(template.New("project").Parse(sectionProject))},
	{"Bridge", template.Must(template.New("bridge").Parse(sectionBridge))},
}

type SectionData struct {
	PlanMode            bool
	Date                string
	OS                  string
	Arch                string
	WorkingDir          string
	MemoryDir           string
	MemoryContent       string
	Skills              string
	ProjectInstructions string
	BridgeInstructions  string
}

// Section is a titled block of the system prompt.
type Section struct {
	Title   string
	Content string
}

// RenderSections renders all section templates with the given data,
// returning only non-empty sections.
func RenderSections(data SectionData) []Section {
	var sections []Section

	for _, st := range sectionTemplates {
		var buf bytes.Buffer

		if err := st.tmpl.Execute(&buf, data); err != nil {
			continue
		}

		if content := strings.TrimSpace(buf.String()); content != "" {
			sections = append(sections, Section{Title: st.title, Content: content})
		}
	}

	return sections
}

// BuildInstructions composes a full system prompt from a base prompt and
// dynamic section data (environment, memory, skills, etc.).
func BuildInstructions(base string, data SectionData) string {
	sections := append([]Section{{Content: base}}, RenderSections(data)...)
	return ComposeSections(sections...)
}

// ComposeSections joins sections into a single system prompt string.
// Empty sections are skipped. Titled sections get a ## header.
func ComposeSections(sections ...Section) string {
	var parts []string

	for _, section := range sections {
		content := strings.TrimSpace(section.Content)
		if content == "" {
			continue
		}

		if section.Title != "" {
			parts = append(parts, "## "+section.Title+"\n\n"+content)
			continue
		}

		parts = append(parts, content)
	}

	return strings.Join(parts, "\n\n")
}
