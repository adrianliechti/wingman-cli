package skill

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"gopkg.in/yaml.v3"
)

type Skill struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	WhenToUse   string `yaml:"when-to-use"`

	// Arguments lists named argument placeholders (e.g., ["query", "file"]).
	// These are substituted as ${arg_name} in skill content.
	Arguments []string `yaml:"arguments"`

	// Location is the relative path to the skill directory (for file-based skills).
	Location string `yaml:"-"`

	// Content is the full skill prompt content (after frontmatter).
	// Set for bundled skills; file-based skills load content on demand.
	Content string `yaml:"-"`

	// Bundled indicates this skill is built-in (not from a SKILL.md file).
	Bundled bool `yaml:"-"`
}

// GetContent returns the skill's prompt content. For bundled skills, returns
// the embedded content. For file-based skills, reads the SKILL.md file.
// An absolute Location (e.g. personal skills under the user's home dir) is
// used as-is; a relative Location is resolved against workingDir.
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

// ApplyArguments substitutes ${ARGUMENTS} and named ${arg_name} placeholders
// in the content string. Compatible with Claude Code's skill argument format.
func (s *Skill) ApplyArguments(content string, args string) string {
	// Replace ${ARGUMENTS} with the full argument string
	content = strings.ReplaceAll(content, "${ARGUMENTS}", args)

	// Replace named arguments. The last argument gets the entire remaining string
	// so that "/commit fix the login bug" passes the full message to ${message}.
	if len(s.Arguments) > 0 {
		remaining := args
		for i, name := range s.Arguments {
			placeholder := "${" + name + "}"

			if remaining == "" {
				content = strings.ReplaceAll(content, placeholder, "")
				continue
			}

			// Last argument gets everything remaining
			if i == len(s.Arguments)-1 {
				content = strings.ReplaceAll(content, placeholder, remaining)
			} else {
				// Earlier arguments get one word each
				word, rest, _ := strings.Cut(strings.TrimSpace(remaining), " ")
				content = strings.ReplaceAll(content, placeholder, word)
				remaining = rest
			}
		}
	}

	return content
}

// skillDirs are project-relative roots scanned for skills. The conventional
// layout is <root>/<dir>/skills/<name>/SKILL.md, but Discover globs for
// **/SKILL.md so any nested layout works too.
var skillDirs = []string{
	".agents",
	".skills",
	".wingman",
	".claude",
	".github",
	".opencode",
}

// personalSkillRoots are home-relative directories scanned for user-wide
// skills following <home>/<root>/<name>/SKILL.md.
var personalSkillRoots = []string{
	".agents/skills",
	".wingman/skills",
	".claude/skills",
	".config/opencode/skills",
}

func Discover(root string) ([]Skill, error) {
	var skills []Skill

	for _, dir := range skillDirs {
		skillDir := filepath.Join(root, dir)
		matches, err := doublestar.Glob(os.DirFS(skillDir), "**/SKILL.md")

		if err != nil {
			continue
		}

		for _, match := range matches {
			skillFile := filepath.Join(skillDir, match)
			skill, err := parseSkillFile(skillFile)

			if err != nil {
				continue
			}

			location := filepath.Dir(skillFile)

			if rel, err := filepath.Rel(root, location); err == nil {
				location = rel
			}

			skill.Location = location
			skills = append(skills, skill)
		}
	}

	return skills, nil
}

// DiscoverPersonal scans the user's home directory for personal skills under
// the conventional <home>/.claude/skills and <home>/.wingman/skills paths,
// matching <home>/<dir>/<name>/SKILL.md. These are user-wide skills available
// across all projects.
func DiscoverPersonal() ([]Skill, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	var skills []Skill

	for _, dir := range personalSkillRoots {
		skillDir := filepath.Join(home, dir)
		matches, err := doublestar.Glob(os.DirFS(skillDir), "**/SKILL.md")

		if err != nil {
			continue
		}

		for _, match := range matches {
			skillFile := filepath.Join(skillDir, match)
			sk, err := parseSkillFile(skillFile)

			if err != nil {
				continue
			}

			// Personal skills carry an absolute Location since they live
			// outside the project root.
			sk.Location = filepath.Dir(skillFile)
			skills = append(skills, sk)
		}
	}

	return skills, nil
}

// MustDiscoverPersonal is like DiscoverPersonal but returns nil on error.
func MustDiscoverPersonal() []Skill {
	skills, _ := DiscoverPersonal()
	return skills
}

// LoadBundled loads skills from an embedded filesystem.
// Skills are expected at <root>/<name>/SKILL.md within the filesystem.
func LoadBundled(fsys fs.FS, root string) ([]Skill, error) {
	var skills []Skill

	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillPath := root + "/" + entry.Name() + "/SKILL.md"

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
		skills = append(skills, skill)
	}

	return skills, nil
}

// FindSkill finds a skill by name (case-insensitive).
func FindSkill(name string, skills []Skill) *Skill {
	lower := strings.ToLower(name)
	for i := range skills {
		if strings.ToLower(skills[i].Name) == lower {
			return &skills[i]
		}
	}
	return nil
}

// MustDiscover is like Discover but returns nil on error.
func MustDiscover(root string) []Skill {
	skills, _ := Discover(root)
	return skills
}

// Merge combines bundled and discovered skills. Discovered skills with the same
// name as a bundled skill override the bundled version (allows user customization).
func Merge(bundled, discovered []Skill) []Skill {
	// Build map of discovered skill names for quick lookup
	overrides := make(map[string]bool)
	for _, s := range discovered {
		overrides[strings.ToLower(s.Name)] = true
	}

	// Keep bundled skills that aren't overridden
	var result []Skill
	for _, s := range bundled {
		if !overrides[strings.ToLower(s.Name)] {
			result = append(result, s)
		}
	}

	// Append all discovered skills
	result = append(result, discovered...)
	return result
}

func FormatForPrompt(skills []Skill) string {
	if len(skills) == 0 {
		return ""
	}

	var sb strings.Builder
	fmt.Fprint(&sb, "<available_skills>\n")

	for _, s := range skills {
		fmt.Fprint(&sb, "  <skill>\n")
		fmt.Fprintf(&sb, "    <name>%s</name>\n", s.Name)
		fmt.Fprintf(&sb, "    <description>%s</description>\n", s.Description)

		if s.WhenToUse != "" {
			fmt.Fprintf(&sb, "    <when-to-use>%s</when-to-use>\n", s.WhenToUse)
		}

		if !s.Bundled && s.Location != "" {
			fmt.Fprintf(&sb, "    <location>%s/SKILL.md</location>\n", s.Location)
		}

		fmt.Fprint(&sb, "  </skill>\n")
	}

	fmt.Fprint(&sb, "</available_skills>")

	return sb.String()
}

// parseSkillFile reads a SKILL.md file and returns the skill metadata.
func parseSkillFile(path string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}

	skill, _, err := parseSkillData(string(data))
	return skill, err
}

// parseSkillData parses YAML frontmatter and content from skill markdown.
// Returns the skill metadata and the content after frontmatter.
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

	if err := yaml.Unmarshal([]byte(frontmatter.String()), &skill); err != nil {
		return Skill{}, "", fmt.Errorf("failed to parse frontmatter: %w", err)
	}

	if skill.Name == "" || skill.Description == "" {
		return Skill{}, "", fmt.Errorf("skill missing required fields")
	}

	return skill, strings.TrimSpace(content.String()), nil
}

// readSkillContent reads a SKILL.md file and returns only the content after frontmatter.
func readSkillContent(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	_, content, err := parseSkillData(string(data))
	return content, err
}
