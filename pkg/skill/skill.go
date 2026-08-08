package skill

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"go.yaml.in/yaml/v4"

	"github.com/adrianliechti/wingman-agent/pkg/layout"
)

type Skill struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
	License       string            `yaml:"license"`
	Compatibility string            `yaml:"compatibility"`
	Metadata      map[string]string `yaml:"metadata"`
	AllowedTools  string            `yaml:"allowed-tools"`

	Location string `yaml:"-"`

	Content string `yaml:"-"`

	Bundled bool `yaml:"-"`

	Plugin string `yaml:"-"`
}

// Qualified returns the plugin-scoped name "<plugin>:<skill>", or the bare name
// for skills that did not come from a plugin. It keeps a plugin skill reachable
// when a same-named skill from a higher-precedence source shadows it.
func (s *Skill) Qualified() string {
	if s.Plugin == "" {
		return s.Name
	}

	return s.Plugin + ":" + s.Name
}

func (s *Skill) GetContent(workingDir string) (string, error) {
	if s.Content != "" {
		return s.Content, nil
	}

	if s.Location == "" {
		return "", fmt.Errorf("skill %q has no location or content", s.Name)
	}

	var path string
	if filepath.IsAbs(s.Location) {
		path = filepath.Join(s.Location, "SKILL.md")
	} else {
		path = filepath.Join(workingDir, s.Location, "SKILL.md")
	}
	return readSkillContent(path)
}

func (s *Skill) ApplyArguments(content, args, skillDir string) string {
	fields := strings.Fields(args)

	lookup := map[string]string{
		"ARGUMENTS":        args,
		"SKILL_DIR":        skillDir,
		"CLAUDE_SKILL_DIR": skillDir,
	}

	matched := false
	resolve := func(name string) (string, bool) {
		if v, ok := lookup[name]; ok {
			return v, true
		}
		return "", false
	}
	resolveIdx := func(idx int) string {
		if idx >= 0 && idx < len(fields) {
			return fields[idx]
		}
		return ""
	}

	content = indexedPattern.ReplaceAllStringFunc(content, func(m string) string {
		sub := indexedPattern.FindStringSubmatch(m)
		if sub[1] != "ARGUMENTS" {
			return m
		}
		idx := atoi(sub[2])
		matched = true
		return resolveIdx(idx)
	})

	content = bracedPattern.ReplaceAllStringFunc(content, func(m string) string {
		name := bracedPattern.FindStringSubmatch(m)[1]
		if i := atoi(name); i > 0 {
			matched = true
			return resolveIdx(i - 1)
		}
		if v, ok := resolve(name); ok {
			matched = true
			return v
		}
		return m
	})

	content = barePattern.ReplaceAllStringFunc(content, func(m string) string {
		sub := barePattern.FindStringSubmatch(m)
		name, boundary := sub[1], sub[2]
		if i := atoi(name); i > 0 {
			matched = true
			return resolveIdx(i-1) + boundary
		}
		if v, ok := resolve(name); ok {
			matched = true
			return v + boundary
		}
		return m
	})

	if !matched && args != "" {
		content = content + "\n\nARGUMENTS: " + args
	}

	return content
}

var (
	indexedPattern = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)\[(\d+)\]\}?`)
	bracedPattern  = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*|\d+)\}`)

	barePattern = regexp.MustCompile(`\$([A-Za-z_][A-Za-z0-9_]*|\d+)([^A-Za-z0-9_\[]|$)`)
)

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func Discover(root string) ([]Skill, error) {
	return discover(layout.ProjectRoots(root, "skills"), root), nil
}

func DiscoverPersonal() ([]Skill, error) {
	if _, err := os.UserHomeDir(); err != nil {
		return nil, err
	}

	return discover(layout.PersonalRoots("skills"), ""), nil
}

func MustDiscoverPersonal() []Skill {
	skills, _ := DiscoverPersonal()
	return skills
}

// LoadDir loads every skill directly beneath dir, one level deep. Locations are
// absolute and parse failures are reported and skipped.
func LoadDir(dir string) []Skill {
	matches, err := doublestar.Glob(os.DirFS(dir), "*/SKILL.md")
	if err != nil {
		return nil
	}

	slices.Sort(matches)

	var skills []Skill

	for _, match := range matches {
		skillFile := filepath.Join(dir, match)

		sk, err := parseSkillFile(skillFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skill: skipped %s: %v\n", skillFile, err)
			continue
		}

		sk.Location = filepath.Dir(skillFile)
		skills = append(skills, sk)
	}

	return skills
}

func discover(dirs []string, relativeTo string) []Skill {
	var skills []Skill
	seen := make(map[string]string)

	for _, dir := range dirs {
		for _, sk := range LoadDir(dir) {
			key := strings.ToLower(sk.Name)

			if winner, ok := seen[key]; ok {
				fmt.Fprintf(os.Stderr, "skill: %s in %s is shadowed by %s\n", sk.Name, sk.Location, winner)
				continue
			}
			seen[key] = sk.Location

			if relativeTo != "" {
				if rel, err := filepath.Rel(relativeTo, sk.Location); err == nil {
					sk.Location = rel
				}
			}

			skills = append(skills, sk)
		}
	}

	return skills
}

func LoadBundled(fsys fs.FS, root string) ([]Skill, error) {
	return loadBundled(fsys, root, "")
}

// LoadBundledAt loads bundled skill metadata and assigns each skill the
// corresponding directory beneath locationRoot. The caller owns copying the
// complete resource tree to that location.
func LoadBundledAt(fsys fs.FS, root, locationRoot string) ([]Skill, error) {
	return loadBundled(fsys, root, locationRoot)
}

func loadBundled(fsys fs.FS, root, locationRoot string) ([]Skill, error) {
	var skills []Skill

	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillRoot := path.Join(root, entry.Name())
		skillPath := path.Join(skillRoot, "SKILL.md")

		data, err := fs.ReadFile(fsys, skillPath)
		if err != nil {
			continue
		}

		skill, content, err := parseSkillData(string(data))
		if err != nil {
			continue
		}

		skill.Content = content
		skill.Bundled = true
		if locationRoot != "" {
			skill.Location = filepath.Join(locationRoot, entry.Name())
		}
		skills = append(skills, skill)
	}

	return skills, nil
}

func (s *Skill) AbsoluteDir(workDir string) string {
	if s.Location == "" {
		return ""
	}
	if filepath.IsAbs(s.Location) {
		return s.Location
	}
	return filepath.Join(workDir, s.Location)
}

// FindSkill resolves either a bare skill name or a "<plugin>:<skill>" name. A
// qualified query never falls back to a bare match, so it always reaches the
// plugin's own skill even when another source shadows the bare name.
func FindSkill(name string, skills []Skill) *Skill {
	lower := strings.ToLower(name)

	if strings.Contains(lower, ":") {
		for i := range skills {
			if strings.ToLower(skills[i].Qualified()) == lower {
				return &skills[i]
			}
		}
		return nil
	}

	for i := range skills {
		if strings.ToLower(skills[i].Name) == lower {
			return &skills[i]
		}
	}
	return nil
}

func MustDiscover(root string) []Skill {
	skills, _ := Discover(root)
	return skills
}

// Merge layers discovered skills over bundled ones by name. A shadowed plugin
// skill is kept, but moved behind the winner so only its qualified name
// resolves; anything else that loses its name is dropped.
func Merge(bundled, discovered []Skill) []Skill {
	overrides := make(map[string]bool)
	for _, s := range discovered {
		overrides[strings.ToLower(s.Name)] = true
	}

	var result, shadowed []Skill

	for _, s := range bundled {
		switch {
		case !overrides[strings.ToLower(s.Name)]:
			result = append(result, s)
		case s.Plugin != "":
			shadowed = append(shadowed, s)
		}
	}

	result = append(result, discovered...)
	return append(result, shadowed...)
}

func FormatForPrompt(skills []Skill) string {
	var sb strings.Builder
	count := 0

	for _, s := range skills {
		if count == 0 {
			fmt.Fprint(&sb, "<available_skills>\n")
		}
		count++

		fmt.Fprint(&sb, "  <skill>\n")
		fmt.Fprintf(&sb, "    <name>%s</name>\n", s.Qualified())
		fmt.Fprintf(&sb, "    <description>%s</description>\n", s.Description)

		if s.Location != "" {
			fmt.Fprintf(&sb, "    <location>%s/SKILL.md</location>\n", displayLocation(s.Location))
		}

		fmt.Fprint(&sb, "  </skill>\n")
	}

	if count == 0 {
		return ""
	}
	fmt.Fprint(&sb, "</available_skills>")
	return sb.String()
}

func displayLocation(loc string) string {
	if filepath.IsAbs(loc) {
		if home, err := os.UserHomeDir(); err == nil {
			if rel, err := filepath.Rel(home, loc); err == nil && !strings.HasPrefix(rel, "..") {
				return "~/" + filepath.ToSlash(rel)
			}
		}
	}
	return filepath.ToSlash(loc)
}

func parseSkillFile(path string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}

	skill, _, err := parseSkillData(string(data))
	return skill, err
}

func parseSkillData(data string) (Skill, string, error) {
	scanner := bufio.NewScanner(strings.NewReader(data))

	var inFrontmatter bool
	var frontmatter strings.Builder
	var content strings.Builder
	var pastFrontmatter bool

	for scanner.Scan() {
		line := scanner.Text()

		if line == "---" {
			if !inFrontmatter && !pastFrontmatter {
				inFrontmatter = true
				continue
			}
			if inFrontmatter {
				inFrontmatter = false
				pastFrontmatter = true
				continue
			}
		}

		if inFrontmatter {
			frontmatter.WriteString(line)
			frontmatter.WriteString("\n")
		} else if pastFrontmatter {
			content.WriteString(line)
			content.WriteString("\n")
		}
	}

	if err := scanner.Err(); err != nil {
		return Skill{}, "", err
	}

	var skill Skill

	if err := yaml.Load([]byte(frontmatter.String()), &skill); err != nil {
		return Skill{}, "", fmt.Errorf("failed to parse frontmatter: %w", err)
	}

	if skill.Name == "" || skill.Description == "" {
		return Skill{}, "", fmt.Errorf("skill missing required fields")
	}

	return skill, strings.TrimSpace(content.String()), nil
}

func readSkillContent(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	_, content, err := parseSkillData(string(data))
	return content, err
}
