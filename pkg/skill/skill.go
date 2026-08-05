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

var skillDirs = []string{
	".agents/skills",
	".wingman/skills",
	".claude/skills",
	".opencode/skills",
}

var personalSkillRoots = []string{
	".agents/skills",
	".wingman/skills",
	".claude/skills",
	".config/opencode/skills",
}

func Discover(root string) ([]Skill, error) {
	return discover(root, skillDirs, true), nil
}

func DiscoverPersonal() ([]Skill, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return discover(home, personalSkillRoots, false), nil
}

func MustDiscoverPersonal() []Skill {
	skills, _ := DiscoverPersonal()
	return skills
}

func discover(root string, dirs []string, relativeLocation bool) []Skill {
	var skills []Skill
	seen := make(map[string]bool)

	for _, dir := range dirs {
		skillDir := filepath.Join(root, dir)
		matches, err := doublestar.Glob(os.DirFS(skillDir), "*/SKILL.md")
		if err != nil {
			continue
		}

		slices.Sort(matches)

		for _, match := range matches {
			skillFile := filepath.Join(skillDir, match)
			sk, err := parseSkillFile(skillFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "skill: skipped %s: %v\n", skillFile, err)
				continue
			}

			if seen[strings.ToLower(sk.Name)] {
				continue
			}
			seen[strings.ToLower(sk.Name)] = true

			location := filepath.Dir(skillFile)
			if relativeLocation {
				if rel, err := filepath.Rel(root, location); err == nil {
					location = rel
				}
			}
			sk.Location = location

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

func FindSkill(name string, skills []Skill) *Skill {
	lower := strings.ToLower(name)
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

func Merge(bundled, discovered []Skill) []Skill {
	overrides := make(map[string]bool)
	for _, s := range discovered {
		overrides[strings.ToLower(s.Name)] = true
	}

	var result []Skill
	for _, s := range bundled {
		if !overrides[strings.ToLower(s.Name)] {
			result = append(result, s)
		}
	}

	result = append(result, discovered...)
	return result
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
		fmt.Fprintf(&sb, "    <name>%s</name>\n", s.Name)
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
