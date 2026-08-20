package skill

import (
	"bytes"
	"fmt"
	"html"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v4"

	"github.com/adrianliechti/wingman-agent/pkg/layout"
)

type Skill struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
	License       string            `yaml:"license"`
	Compatibility string            `yaml:"compatibility"`
	Metadata      map[string]string `yaml:"metadata"`
	AllowedTools  []string          `yaml:"allowed-tools"`
	Arguments     []string          `yaml:"-"`
	ArgumentHint  string            `yaml:"-"`

	Location string `yaml:"-"`

	Content string `yaml:"-"`

	Bundled bool `yaml:"-"`

	Plugin string `yaml:"-"`

	nonPortableFrontmatter bool
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

// InvocationHint returns the explicit Claude-compatible argument hint or a
// neutral hint derived from the declared positional argument names.
func (s *Skill) InvocationHint() string {
	if hint := strings.TrimSpace(s.ArgumentHint); hint != "" {
		return hint
	}
	parts := make([]string, 0, len(s.Arguments))
	for _, name := range s.Arguments {
		parts = append(parts, "["+name+"]")
	}
	return strings.Join(parts, " ")
}

// Portable reports whether the skill frontmatter uses only Agent Skills fields.
func (s *Skill) Portable() bool {
	return !s.nonPortableFrontmatter
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

// ApplySubstitutions renders Wingman's body-only skill extensions. The neutral
// directory variables have Claude-compatible aliases; argument behavior follows
// Claude Code, including zero-based indexed arguments and fallback appending.
func (s *Skill) ApplySubstitutions(content, args, skillDir, projectDir string) string {
	content = strings.NewReplacer(
		"${SKILL_DIR}", skillDir,
		"${PROJECT_DIR}", projectDir,
		"${CLAUDE_SKILL_DIR}", skillDir,
		"${CLAUDE_PROJECT_DIR}", projectDir,
	).Replace(content)

	fields := splitArguments(args)
	named := make(map[string]string, len(s.Arguments))
	for index, name := range s.Arguments {
		if index < len(fields) {
			named[name] = fields[index]
		} else {
			named[name] = ""
		}
	}
	var rendered strings.Builder
	matched := false

	for index := 0; index < len(content); {
		if content[index] != '$' {
			rendered.WriteByte(content[index])
			index++
			continue
		}

		end, replacement, ok := argumentToken(content, index, args, fields, named)
		if !ok {
			rendered.WriteByte(content[index])
			index++
			continue
		}

		backslashes := 0
		for cursor := index - 1; cursor >= 0 && content[cursor] == '\\'; cursor-- {
			backslashes++
		}
		if backslashes == 1 {
			value := rendered.String()
			rendered.Reset()
			rendered.WriteString(strings.TrimSuffix(value, `\`))
			rendered.WriteString(content[index:end])
			index = end
			continue
		}

		matched = true
		rendered.WriteString(replacement)
		index = end
	}

	if !matched && args != "" {
		rendered.WriteString("\n\nARGUMENTS: ")
		rendered.WriteString(args)
	}
	return rendered.String()
}

func argumentToken(content string, start int, all string, fields []string, named map[string]string) (int, string, bool) {
	const arguments = "$ARGUMENTS"
	if strings.HasPrefix(content[start:], arguments) {
		end := start + len(arguments)
		if end < len(content) && content[end] == '[' {
			close := strings.IndexByte(content[end+1:], ']')
			if close < 0 {
				return 0, "", false
			}
			close += end + 1
			position, ok := decimalIndex(content[end+1 : close])
			if !ok {
				return 0, "", false
			}
			if position >= len(fields) {
				return close + 1, content[start : close+1], true
			}
			return close + 1, fields[position], true
		}
		if end < len(content) && (isNameByte(content[end]) || content[end] == '[') {
			return 0, "", false
		}
		return end, all, true
	}

	end := start + 1
	for end < len(content) && content[end] >= '0' && content[end] <= '9' {
		end++
	}
	if end == start+1 {
		end = start + 1
		for end < len(content) && isArgumentNameByte(content[end]) {
			end++
		}
		if end == start+1 {
			return 0, "", false
		}
		replacement, ok := named[content[start+1:end]]
		if !ok {
			return 0, "", false
		}
		return end, replacement, true
	}
	position, ok := decimalIndex(content[start+1 : end])
	if !ok {
		return end, content[start:end], true
	}
	if position >= len(fields) {
		return end, content[start:end], true
	}
	return end, fields[position], true
}

func isArgumentNameByte(value byte) bool {
	return isNameByte(value) || value == '-'
}

func isArgumentNameStart(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z' || value == '_'
}

func decimalIndex(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, false
		}
	}
	result, err := strconv.Atoi(value)
	return result, err == nil
}

func isNameByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_'
}

func splitArguments(value string) []string {
	var fields []string
	var field strings.Builder
	quote := byte(0)
	escaped := false
	started := false
	flush := func() {
		if started {
			fields = append(fields, field.String())
			field.Reset()
			started = false
		}
	}

	for index := 0; index < len(value); index++ {
		char := value[index]
		if escaped {
			field.WriteByte(char)
			started = true
			escaped = false
			continue
		}
		if char == '\\' && quote != '\'' {
			escaped = true
			started = true
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			} else {
				field.WriteByte(char)
			}
			started = true
			continue
		}
		if char == '\'' || char == '"' {
			quote = char
			started = true
			continue
		}
		if char == ' ' || char == '\t' || char == '\n' || char == '\r' {
			flush()
			continue
		}
		field.WriteByte(char)
		started = true
	}
	if escaped {
		field.WriteByte('\\')
	}
	flush()
	return fields
}

func Discover(root string) ([]Skill, error) {
	sources := []string{filepath.Join(root, ".wingman", "skills")}
	for _, dir := range skillDirsThroughRepo(root, ".agents") {
		sources = append(sources, dir)
	}
	for _, dir := range skillDirsThroughRepo(root, ".claude") {
		sources = append(sources, dir)
	}
	return discover(sources, root), nil
}

func DiscoverPersonal() ([]Skill, error) {
	sources := layout.PersonalRoots("skills")
	if len(sources) == 0 {
		_, err := os.UserHomeDir()
		return nil, err
	}

	return discover(sources, ""), nil
}

func MustDiscoverPersonal() []Skill {
	skills, _ := DiscoverPersonal()
	return skills
}

// LoadDir loads skills beneath dir. Directories without SKILL.md are grouping
// directories and are traversed recursively; a skill directory is a leaf so
// supporting resources below it cannot be mistaken for additional skills.
// Symlinked directories are followed once, with their resolved paths used to
// prevent cycles.
func LoadDir(dir string) []Skill {
	var skills []Skill
	visited := make(map[string]bool)

	var walk func(string)
	walk = func(current string) {
		resolved, err := filepath.EvalSymlinks(current)
		if err != nil {
			return
		}
		resolved, err = filepath.Abs(resolved)
		if err != nil || visited[resolved] {
			return
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.IsDir() {
			return
		}
		visited[resolved] = true

		skillFile := filepath.Join(current, "SKILL.md")
		if info, err := os.Stat(skillFile); err == nil && info.Mode().IsRegular() {
			sk, err := LoadFile(skillFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "skill: skipped %s: %v\n", skillFile, err)
				return
			}
			skills = append(skills, sk)
			return
		}

		entries, err := os.ReadDir(current)
		if err != nil {
			return
		}
		for _, entry := range entries {
			child := filepath.Join(current, entry.Name())
			if info, err := os.Stat(child); err == nil && info.IsDir() {
				walk(child)
			}
		}
	}

	walk(dir)

	return skills
}

// LoadFile parses one SKILL.md and records its containing directory.
func LoadFile(path string) (Skill, error) {
	skill, err := parseSkillFile(path)
	if err != nil {
		return Skill{}, err
	}
	skill.Location = filepath.Dir(path)
	return skill, nil
}

func skillDirsThroughRepo(root, configDir string) []string {
	boundary := root
	for current := root; ; current = filepath.Dir(current) {
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			boundary = current
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	var dirs []string
	for current := root; ; current = filepath.Dir(current) {
		dirs = append(dirs, filepath.Join(current, configDir, "skills"))
		if current == boundary {
			break
		}
	}
	return dirs
}

func discover(sources []string, relativeTo string) []Skill {
	var skills []Skill
	seen := make(map[string]string)

	for _, source := range sources {
		for _, sk := range LoadDir(source) {
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
		if skill.Name != entry.Name() {
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
	const maxPromptBytes = 8000
	maxDescription := 0
	for _, s := range skills {
		if length := len([]rune(s.Description)); length > maxDescription {
			maxDescription = length
		}
	}
	if len(skills) == 0 {
		return ""
	}
	active := skills

	if prompt := renderPromptSkills(active, maxDescription); len(prompt) <= maxPromptBytes {
		return prompt
	}
	if structural := renderPromptSkills(active, 0); len(structural) > maxPromptBytes {
		for len(active) > 0 {
			active = active[:len(active)-1]
			structural = renderPromptSkills(active, 0)
			if len(structural) <= maxPromptBytes {
				return structural
			}
		}
		return ""
	}

	low, high := 0, maxDescription
	for low < high {
		middle := (low + high + 1) / 2
		if len(renderPromptSkills(active, middle)) <= maxPromptBytes {
			low = middle
		} else {
			high = middle - 1
		}
	}
	return renderPromptSkills(active, low)
}

func renderPromptSkills(skills []Skill, descriptionLimit int) string {
	if len(skills) == 0 {
		return ""
	}
	var sb strings.Builder
	fmt.Fprint(&sb, "<available_skills>\n")
	for _, skill := range skills {
		description := []rune(skill.Description)
		if len(description) > descriptionLimit {
			description = description[:descriptionLimit]
		}
		fmt.Fprint(&sb, formatPromptSkill(skill, string(description)))
	}
	fmt.Fprint(&sb, "</available_skills>")
	return sb.String()
}

func formatPromptSkill(s Skill, description string) string {
	var sb strings.Builder
	fmt.Fprint(&sb, "  <skill>\n")
	fmt.Fprintf(&sb, "    <name>%s</name>\n", html.EscapeString(s.Qualified()))
	fmt.Fprintf(&sb, "    <description>%s</description>\n", html.EscapeString(description))
	if s.Location != "" {
		fmt.Fprintf(&sb, "    <location>%s/SKILL.md</location>\n", html.EscapeString(displayLocation(s.Location)))
	}
	fmt.Fprint(&sb, "  </skill>\n")
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
	if err != nil {
		return Skill{}, err
	}
	directoryName := filepath.Base(filepath.Dir(path))
	if skill.Name != directoryName {
		return Skill{}, fmt.Errorf("skill name %q must match parent directory %q", skill.Name, filepath.Base(filepath.Dir(path)))
	}
	return skill, nil
}

func parseSkillData(data string) (Skill, string, error) {
	lines := strings.Split(strings.ReplaceAll(data, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return Skill{}, "", fmt.Errorf("missing YAML frontmatter delimited by ---")
	}
	closing := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			closing = i
			break
		}
	}
	if closing < 0 {
		return Skill{}, "", fmt.Errorf("missing closing YAML frontmatter delimiter")
	}

	frontmatter := []byte(strings.Join(lines[1:closing], "\n"))
	var raw skillFrontmatter
	var present map[string]any
	if len(bytes.TrimSpace(frontmatter)) > 0 {
		if err := yaml.Load(frontmatter, &raw); err != nil {
			return Skill{}, "", fmt.Errorf("failed to parse frontmatter: %w", err)
		}
		if err := yaml.Load(frontmatter, &present); err != nil {
			return Skill{}, "", fmt.Errorf("failed to parse frontmatter: %w", err)
		}
	}
	content := strings.TrimSpace(strings.Join(lines[closing+1:], "\n"))
	skill, err := normalizeSkill(raw, present)
	if err != nil {
		return Skill{}, "", err
	}
	return skill, content, nil
}

type skillFrontmatter struct {
	Name          *string        `yaml:"name"`
	Description   *string        `yaml:"description"`
	License       *string        `yaml:"license"`
	Compatibility *string        `yaml:"compatibility"`
	Metadata      map[string]any `yaml:"metadata"`
	AllowedTools  any            `yaml:"allowed-tools"`
	Arguments     any            `yaml:"arguments"`
	ArgumentHint  *string        `yaml:"argument-hint"`
}

func normalizeSkill(raw skillFrontmatter, present map[string]any) (Skill, error) {
	skill := Skill{}
	for name := range present {
		switch name {
		case "name", "description", "license", "compatibility", "metadata", "allowed-tools", "arguments", "argument-hint":
		default:
			return Skill{}, fmt.Errorf("unsupported Agent Skills frontmatter field %q", name)
		}
	}
	skill.Metadata = make(map[string]string)
	if value, ok := present["metadata"]; ok && value == nil {
		return Skill{}, fmt.Errorf("skill metadata must be a mapping")
	}
	for name, value := range raw.Metadata {
		if text, ok := value.(string); ok {
			skill.Metadata[name] = text
			continue
		}
		return Skill{}, fmt.Errorf("skill metadata value %q must be a string", name)
	}
	if raw.Name != nil {
		skill.Name = strings.TrimSpace(*raw.Name)
	}
	if raw.Description != nil {
		skill.Description = strings.TrimSpace(*raw.Description)
	}
	if raw.License != nil {
		skill.License = strings.TrimSpace(*raw.License)
	}
	if raw.Compatibility != nil {
		skill.Compatibility = strings.TrimSpace(*raw.Compatibility)
	}
	if raw.AllowedTools != nil {
		allowed, ok := raw.AllowedTools.(string)
		if !ok {
			return Skill{}, fmt.Errorf("allowed-tools must be a string in an Agent Skill")
		}
		skill.AllowedTools = strings.Fields(allowed)
	}
	if raw.Arguments != nil {
		arguments, err := parseNamedArguments(raw.Arguments)
		if err != nil {
			return Skill{}, err
		}
		skill.Arguments = arguments
	}
	if raw.ArgumentHint != nil {
		skill.ArgumentHint = strings.TrimSpace(*raw.ArgumentHint)
	}
	if _, ok := present["arguments"]; ok {
		skill.nonPortableFrontmatter = true
	}
	if _, ok := present["argument-hint"]; ok {
		skill.nonPortableFrontmatter = true
	}
	if _, ok := present["compatibility"]; ok && raw.Compatibility == nil {
		return Skill{}, fmt.Errorf("skill compatibility must be a string")
	}
	if _, ok := present["license"]; ok && raw.License == nil {
		return Skill{}, fmt.Errorf("skill license must be a string")
	}
	if _, ok := present["allowed-tools"]; ok && raw.AllowedTools == nil {
		return Skill{}, fmt.Errorf("allowed-tools must be a string in an Agent Skill")
	}
	if _, ok := present["arguments"]; ok && raw.Arguments == nil {
		return Skill{}, fmt.Errorf("skill arguments must be a string or list of strings")
	}
	if _, ok := present["argument-hint"]; ok && raw.ArgumentHint == nil {
		return Skill{}, fmt.Errorf("skill argument-hint must be a string")
	}
	if err := validateSkill(skill, present); err != nil {
		return Skill{}, err
	}
	return skill, nil
}

func parseNamedArguments(value any) ([]string, error) {
	var arguments []string
	switch value := value.(type) {
	case string:
		arguments = strings.Fields(value)
	case []any:
		arguments = make([]string, 0, len(value))
		for _, item := range value {
			name, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("skill arguments must be a string or list of strings")
			}
			arguments = append(arguments, strings.TrimSpace(name))
		}
	default:
		return nil, fmt.Errorf("skill arguments must be a string or list of strings")
	}

	seen := make(map[string]bool, len(arguments))
	for _, name := range arguments {
		if name == "" || name == "ARGUMENTS" || !isArgumentNameStart(name[0]) {
			return nil, fmt.Errorf("skill argument name %q is invalid", name)
		}
		for index := 1; index < len(name); index++ {
			if !isArgumentNameByte(name[index]) {
				return nil, fmt.Errorf("skill argument name %q is invalid", name)
			}
		}
		if seen[name] {
			return nil, fmt.Errorf("skill argument name %q is duplicated", name)
		}
		seen[name] = true
	}
	return arguments, nil
}

func validateSkill(skill Skill, present map[string]any) error {
	if skill.Name == "" {
		return fmt.Errorf("skill missing required field name")
	}
	if len([]rune(skill.Name)) > 64 {
		return fmt.Errorf("skill name exceeds 64 characters")
	}
	if strings.HasPrefix(skill.Name, "-") || strings.HasSuffix(skill.Name, "-") || strings.Contains(skill.Name, "--") {
		return fmt.Errorf("skill name %q is not valid kebab-case", skill.Name)
	}
	for _, r := range skill.Name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return fmt.Errorf("skill name %q may only use lowercase letters, digits, and hyphens", skill.Name)
		}
	}
	description := strings.TrimSpace(skill.Description)
	if description == "" {
		return fmt.Errorf("skill missing required field description")
	}
	if len([]rune(description)) > 1024 {
		return fmt.Errorf("skill description exceeds 1024 characters")
	}
	if _, ok := present["compatibility"]; ok {
		if skill.Compatibility == "" {
			return fmt.Errorf("skill compatibility must contain 1-500 characters")
		}
		if len([]rune(skill.Compatibility)) > 500 {
			return fmt.Errorf("skill compatibility exceeds 500 characters")
		}
	}
	return nil
}

func readSkillContent(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	_, content, err := parseSkillData(string(data))
	return content, err
}
