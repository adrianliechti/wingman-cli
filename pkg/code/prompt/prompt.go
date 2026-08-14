package prompt

import (
	"bytes"
	"embed"
	"io/fs"
	"strings"
	"sync"
	"text/template"

	"github.com/adrianliechti/wingman-agent/pkg/model"
)

//go:embed mode_agent.txt
var modeAgent string

//go:embed mode_plan.txt
var modePlan string

//go:embed mode_unattended.txt
var modeUnattended string

//go:embed models
var modelFS embed.FS

type Variant struct {
	Agent      string
	Plan       string
	Unattended string
}

var variants = loadVariants()

func loadVariants() map[string]Variant {
	result := map[string]Variant{}

	dirs, err := fs.ReadDir(modelFS, "models")

	if err != nil {
		panic(err)
	}

	for _, dir := range dirs {
		if !dir.IsDir() {
			panic("prompt: unexpected file models/" + dir.Name())
		}

		variant := Variant{Agent: modeAgent, Plan: modePlan, Unattended: modeUnattended}

		files, err := fs.ReadDir(modelFS, "models/"+dir.Name())

		if err != nil {
			panic(err)
		}

		for _, file := range files {
			data, err := fs.ReadFile(modelFS, "models/"+dir.Name()+"/"+file.Name())

			if err != nil {
				panic(err)
			}

			switch file.Name() {
			case "mode_agent.txt":
				variant.Agent = string(data)
			case "mode_plan.txt":
				variant.Plan = string(data)
			case "mode_unattended.txt":
				variant.Unattended = string(data)
			default:
				panic("prompt: unexpected file models/" + dir.Name() + "/" + file.Name())
			}
		}

		result[model.Normalize(dir.Name())] = variant
	}

	return result
}

func VariantFor(id string) Variant {
	id = model.Normalize(id)

	match := ""
	for prefix := range variants {
		if (id == prefix || strings.HasPrefix(id, prefix+"-")) && len(prefix) > len(match) {
			match = prefix
		}
	}

	if variant, ok := variants[match]; ok {
		return variant
	}

	return Variant{Agent: modeAgent, Plan: modePlan, Unattended: modeUnattended}
}

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
	Model               model.Model
	PlanMode            bool
	UnattendedMode      bool
	Date                string
	Timezone            string
	OS                  string
	Arch                string
	WorkingDir          string
	Shell               string
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
	staticParts = append(staticParts, Section{Content: renderBase(base, data)})
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

var baseTemplates sync.Map

func renderBase(source string, data SectionData) string {
	tmpl, ok := baseTemplates.Load(source)
	if !ok {
		parsed := template.Must(template.New("mode").Parse(source))
		tmpl, _ = baseTemplates.LoadOrStore(source, parsed)
	}

	var buf bytes.Buffer
	if err := tmpl.(*template.Template).Execute(&buf, data); err != nil {
		panic(err)
	}
	return buf.String()
}

func composeSections(sections []Section) string {
	var parts []string

	for _, section := range sections {
		content := strings.TrimSpace(section.Content)
		if content == "" {
			continue
		}

		if section.Title != "" {
			parts = append(parts, "# "+section.Title+"\n\n"+content)
			continue
		}

		parts = append(parts, content)
	}

	return strings.Join(parts, "\n\n")
}
