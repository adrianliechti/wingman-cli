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

//go:embed mode_unattended.txt
var Unattended string

//go:embed section_environment.txt
var sectionEnvironment string

//go:embed section_memory.txt
var sectionMemory string

//go:embed section_skills.txt
var sectionSkills string

//go:embed section_project.txt
var sectionProject string

const BoundaryMarker = "--- session context ---"

type namedTemplate struct {
	title string
	tmpl  *template.Template
}

var (
	tmplProject     = namedTemplate{"Project Guidelines", template.Must(template.New("project").Parse(sectionProject))}
	tmplSkills      = namedTemplate{"Skills", template.Must(template.New("skills").Parse(sectionSkills))}
	tmplMemory      = namedTemplate{"Memory", template.Must(template.New("memory").Parse(sectionMemory))}
	tmplEnvironment = namedTemplate{"Environment", template.Must(template.New("environment").Parse(sectionEnvironment))}
)

var staticTemplates = []namedTemplate{tmplProject, tmplSkills, tmplMemory}

var dynamicTemplates = []namedTemplate{tmplEnvironment}

type SectionData struct {
	PlanMode            bool
	UnattendedMode      bool
	Date                string
	OS                  string
	Arch                string
	WorkingDir          string
	MemoryDir           string
	MemoryContent       string
	Skills              string
	ProjectInstructions string
}

type Section struct {
	Title   string
	Content string
}

func renderSections(templates []namedTemplate, data SectionData) []Section {
	var sections []Section

	for _, st := range templates {
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

func BuildAgentContext(data SectionData) string {
	return composeSections(renderSections([]namedTemplate{tmplProject, tmplEnvironment}, data))
}

func BuildInstructions(base string, data SectionData) string {
	var staticParts []Section
	staticParts = append(staticParts, Section{Content: base})
	staticParts = append(staticParts, renderSections(staticTemplates, data)...)

	dynamicParts := renderSections(dynamicTemplates, data)

	staticBlock := composeSections(staticParts)
	dynamicBlock := composeSections(dynamicParts)

	if dynamicBlock == "" {
		return staticBlock
	}
	if staticBlock == "" {
		return dynamicBlock
	}
	return staticBlock + "\n\n" + BoundaryMarker + "\n\n" + dynamicBlock
}

func composeSections(sections []Section) string {
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
