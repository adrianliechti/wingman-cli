package skill

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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

	// Raw is the original SKILL.md bytes. Set for bundled skills so we can
	// materialize them to disk byte-for-byte (preserving frontmatter).
	Raw string `yaml:"-"`

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

// ApplyArguments substitutes argument and skill-dir placeholders in the
// skill content. Supports both brace and no-brace forms for cross-client
// compatibility (Claude Code uses $-prefix, agentskills.io uses ${...}):
//
//   ${ARGUMENTS}, $ARGUMENTS                  → full args string
//   ${SKILL_DIR}, $SKILL_DIR                  → absolute skill directory
//   ${CLAUDE_SKILL_DIR}, $CLAUDE_SKILL_DIR    → same (Claude Code alias)
//   ${ARGUMENTS[N]}, $ARGUMENTS[N]            → 0-based positional arg
//   ${1}, $1, ${2}, $2, …                     → 1-based positional arg
//   ${name}, $name                            → named args (last gets remainder)
//
// Out-of-range positional indices substitute the empty string. Bare `$KEY`
// is word-bounded — `$message` substitutes, `$messagefoo` stays literal.
//
// If no placeholder matched and args is non-empty, "\n\nARGUMENTS: <args>"
// is appended so the user's input still reaches the model. This mirrors
// Claude Code's appendIfNoPlaceholder behaviour.
func (s *Skill) ApplyArguments(content, args, skillDir string) string {
	fields := strings.Fields(args)

	// Build a name→value lookup table for named/magic placeholders.
	lookup := map[string]string{
		"ARGUMENTS":        args,
		"SKILL_DIR":        skillDir,
		"CLAUDE_SKILL_DIR": skillDir, // Claude Code compatibility
	}
	if len(s.Arguments) > 0 {
		remaining := args
		for i, name := range s.Arguments {
			if remaining == "" {
				lookup[name] = ""
				continue
			}
			if i == len(s.Arguments)-1 {
				lookup[name] = remaining
			} else {
				word, rest, _ := strings.Cut(strings.TrimSpace(remaining), " ")
				lookup[name] = word
				remaining = rest
			}
		}
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

	// Pass 1: ${KEY[N]} and $KEY[N] — indexed access.
	content = indexedPattern.ReplaceAllStringFunc(content, func(m string) string {
		sub := indexedPattern.FindStringSubmatch(m)
		// sub[1] = "ARGUMENTS" (the only indexed name we recognise), sub[2] = digits
		if sub[1] != "ARGUMENTS" {
			return m
		}
		idx := atoi(sub[2])
		matched = true
		return resolveIdx(idx)
	})

	// Pass 2: ${KEY}.
	content = bracedPattern.ReplaceAllStringFunc(content, func(m string) string {
		name := bracedPattern.FindStringSubmatch(m)[1]
		if i := atoi(name); i > 0 {
			matched = true
			return resolveIdx(i - 1) // 1-based for ${N}
		}
		if v, ok := resolve(name); ok {
			matched = true
			return v
		}
		return m // leave unknown vars as literal
	})

	// Pass 3: $KEY (word-bounded — must be followed by non-word/non-`[` or end).
	content = barePattern.ReplaceAllStringFunc(content, func(m string) string {
		sub := barePattern.FindStringSubmatch(m)
		name, boundary := sub[1], sub[2]
		if i := atoi(name); i > 0 {
			matched = true
			return resolveIdx(i-1) + boundary // 1-based for $N
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
	// ${ARGUMENTS[3]} or $ARGUMENTS[3]
	indexedPattern = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)\[(\d+)\]\}?`)
	// ${KEY}
	bracedPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*|\d+)\}`)
	// $KEY — captures the trailing boundary char (or empty at end-of-input)
	// so we can put it back on substitution. Skipping word chars and `[`
	// avoids matching mid-identifier or chewing the indexed form already
	// handled in pass 1.
	barePattern = regexp.MustCompile(`\$([A-Za-z_][A-Za-z0-9_]*|\d+)([^A-Za-z0-9_\[]|$)`)
)

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// skillDirs are project-relative roots scanned for skills. Layout is
// <root>/<dir>/<name>/SKILL.md (strictly two levels), matching the spec.
var skillDirs = []string{
	".agents/skills",
	".wingman/skills",
	".claude/skills",
	".opencode/skills",
}

// personalSkillRoots are home-relative directories scanned for user-wide
// skills following <home>/<root>/<name>/SKILL.md.
var personalSkillRoots = []string{
	".agents/skills",
	".wingman/skills",
	".claude/skills",
	".config/opencode/skills",
}

// Discover scans the project's skill roots (.claude, .wingman, etc.) for
// SKILL.md files. Locations are returned relative to root.
func Discover(root string) ([]Skill, error) {
	return discover(root, skillDirs, true), nil
}

// DiscoverPersonal scans the user's home directory for personal skills under
// the conventional roots in personalSkillRoots. These are user-wide skills
// available across all projects. Locations are returned as absolute paths
// since they live outside any project root.
func DiscoverPersonal() ([]Skill, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return discover(home, personalSkillRoots, false), nil
}

// MustDiscoverPersonal is like DiscoverPersonal but returns nil on error.
func MustDiscoverPersonal() []Skill {
	skills, _ := DiscoverPersonal()
	return skills
}

// discover is the shared implementation behind Discover/DiscoverPersonal.
// It walks each <root>/<dir> for **/SKILL.md, parses the frontmatter, and
// keeps the first skill encountered for each name (case-insensitive) so a
// single scan never returns duplicates.
//
// When relativeLocation is true (project scope), Location is set relative
// to root so the system prompt can show e.g. ".claude/skills/foo". Otherwise
// (personal scope) Location stays absolute so GetContent can find the file
// without knowing the workdir.
func discover(root string, dirs []string, relativeLocation bool) []Skill {
	var skills []Skill
	seen := make(map[string]bool)

	for _, dir := range dirs {
		skillDir := filepath.Join(root, dir)
		matches, err := doublestar.Glob(os.DirFS(skillDir), "*/SKILL.md")
		if err != nil {
			continue
		}

		// Sort for deterministic order across platforms — doublestar.Glob
		// doesn't guarantee any particular ordering.
		sort.Strings(matches)

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
		skill.Raw = string(data)
		skill.Bundled = true
		skills = append(skills, skill)
	}

	return skills, nil
}

// MaterializeBundled writes a bundled skill's SKILL.md to
// ~/.wingman/skills/<name>/SKILL.md if it isn't already there, and updates
// the in-memory Skill so subsequent prompt builds in the same session see
// it as a "real" on-disk skill (with a `<location>` in the catalog).
//
// Returns the absolute skill directory. From the next session the file is
// discovered as a personal skill, so the user can customize it freely
// without losing it on a wingman update.
func MaterializeBundled(s *Skill) (string, error) {
	if !s.Bundled || s.Raw == "" {
		return "", nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	dir := filepath.Join(home, ".wingman", "skills", s.Name)
	file := filepath.Join(dir, "SKILL.md")

	// If the user has already customized this skill, leave it alone.
	if _, err := os.Stat(file); err == nil {
		s.Location = dir
		return dir, nil
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(file, []byte(s.Raw), 0644); err != nil {
		return "", err
	}

	s.Location = dir
	return dir, nil
}

// AbsoluteDir returns the absolute filesystem directory of the skill.
// Bundled skills that haven't been materialized yet return "".
func (s *Skill) AbsoluteDir(workDir string) string {
	if s.Location == "" {
		return ""
	}
	if filepath.IsAbs(s.Location) {
		return s.Location
	}
	return filepath.Join(workDir, s.Location)
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

		if s.Location != "" {
			fmt.Fprintf(&sb, "    <location>%s/SKILL.md</location>\n", displayLocation(s.Location))
		}

		fmt.Fprint(&sb, "  </skill>\n")
	}

	fmt.Fprint(&sb, "</available_skills>")

	return sb.String()
}

// displayLocation returns a path suitable for showing to the LLM. Paths
// under the user's home dir are abbreviated with `~` so the prompt doesn't
// leak the username on personal-skill entries.
func displayLocation(loc string) string {
	if !filepath.IsAbs(loc) {
		return loc
	}
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, loc); err == nil && !strings.HasPrefix(rel, "..") {
			return "~/" + filepath.ToSlash(rel)
		}
	}
	return loc
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
