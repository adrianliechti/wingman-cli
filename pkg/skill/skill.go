package skill

import (
	"bytes"
	"fmt"
	"html"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/bmatcuk/doublestar/v4"
	"go.yaml.in/yaml/v4"
)

type Skill struct {
	Name            string            `yaml:"name"`
	DisplayName     string            `yaml:"-"`
	Description     string            `yaml:"description"`
	License         string            `yaml:"license"`
	Compatibility   string            `yaml:"compatibility"`
	Metadata        map[string]string `yaml:"metadata"`
	ClaudeMetadata  map[string]any    `yaml:"-"`
	AllowedTools    []string          `yaml:"allowed-tools"`
	DisallowedTools []string          `yaml:"disallowed-tools"`

	WhenToUse               string   `yaml:"when_to_use"`
	ArgumentHint            string   `yaml:"argument-hint"`
	Arguments               []string `yaml:"arguments"`
	DisableModelInvocation  bool     `yaml:"disable-model-invocation"`
	AllowImplicitInvocation *bool    `yaml:"-"`
	UserInvocable           *bool    `yaml:"user-invocable"`
	Model                   string   `yaml:"model"`
	Effort                  string   `yaml:"effort"`
	Context                 string   `yaml:"context"`
	Agent                   string   `yaml:"agent"`
	Background              *bool    `yaml:"background"`
	Paths                   []string `yaml:"paths"`
	Shell                   string   `yaml:"shell"`
	Hooks                   any      `yaml:"hooks"`

	Location string `yaml:"-"`

	Content string `yaml:"-"`

	Bundled bool `yaml:"-"`

	Plugin string `yaml:"-"`

	profile Profile `yaml:"-"`
}

// Profile selects the frontmatter contract used by a skill source.
type Profile uint8

const (
	AgentSkillsProfile Profile = iota
	CodexSkillsProfile
	ClaudeSkillsProfile
)

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
	return readSkillContent(path, s.profile)
}

func (s *Skill) ApplyArguments(content, args, skillDir string) string {
	return s.applyArguments(content, args, skillDir, "")
}

func (s *Skill) applyArguments(content, args, skillDir, projectDir string) string {
	if s.profile == ClaudeSkillsProfile {
		content = s.protectEscapedArguments(content)
	}
	fields := strings.Fields(args)
	if s.profile == ClaudeSkillsProfile {
		fields = splitShellArguments(args)
	}

	lookup := map[string]string{
		"ARGUMENTS":         args,
		"SKILL_DIR":         skillDir,
		"CLAUDE_SKILL_DIR":  skillDir,
		"CLAUDE_SKILL_NAME": s.Name,
	}
	if projectDir != "" {
		lookup["CLAUDE_PROJECT_DIR"] = projectDir
	}
	for i, name := range s.Arguments {
		if name != "" {
			lookup[name] = resolveArgument(fields, i)
		}
	}

	matched := false
	resolve := func(name string) (string, bool) {
		if v, ok := lookup[name]; ok {
			return v, true
		}
		return "", false
	}
	resolveIdx := func(idx int) (string, bool) {
		if idx >= 0 && idx < len(fields) {
			return fields[idx], true
		}
		return "", false
	}

	content = indexedPattern.ReplaceAllStringFunc(content, func(m string) string {
		sub := indexedPattern.FindStringSubmatch(m)
		if sub[1] != "ARGUMENTS" {
			return m
		}
		idx := atoi(sub[2])
		matched = true
		if value, ok := resolveIdx(idx); ok {
			return value
		}
		if s.profile == ClaudeSkillsProfile {
			return m
		}
		return ""
	})

	content = bracedPattern.ReplaceAllStringFunc(content, func(m string) string {
		name := bracedPattern.FindStringSubmatch(m)[1]
		if i, ok := s.numericArgument(name); ok {
			matched = true
			if value, exists := resolveIdx(i); exists {
				return value
			}
			if s.profile == ClaudeSkillsProfile {
				return m
			}
			return ""
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
		if i, ok := s.numericArgument(name); ok {
			matched = true
			if value, exists := resolveIdx(i); exists {
				return value + boundary
			}
			if s.profile == ClaudeSkillsProfile {
				return m
			}
			return boundary
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
	if s.profile == ClaudeSkillsProfile {
		content = strings.ReplaceAll(content, escapedArgumentSentinel, "$")
	}

	return content
}

const escapedArgumentSentinel = "\x00wingman-escaped-argument\x00"

func (s *Skill) protectEscapedArguments(content string) string {
	result := make([]byte, 0, len(content))
	for i := 0; i < len(content); i++ {
		if content[i] == '$' && s.isClaudeArgumentToken(content[i:]) {
			backslashes := 0
			for j := len(result) - 1; j >= 0 && result[j] == '\\'; j-- {
				backslashes++
			}
			if backslashes%2 == 1 {
				result = result[:len(result)-1]
				result = append(result, escapedArgumentSentinel...)
				continue
			}
		}
		result = append(result, content[i])
	}
	return string(result)
}

func (s *Skill) isClaudeArgumentToken(value string) bool {
	if sub := indexedPattern.FindStringSubmatch(value); len(sub) > 0 && strings.HasPrefix(value, sub[0]) {
		return sub[1] == "ARGUMENTS"
	}
	if sub := bracedPattern.FindStringSubmatch(value); len(sub) > 0 && strings.HasPrefix(value, sub[0]) {
		return s.isArgumentName(sub[1])
	}
	if sub := barePattern.FindStringSubmatch(value); len(sub) > 0 && strings.HasPrefix(value, sub[0]) {
		return s.isArgumentName(sub[1])
	}
	return false
}

func (s *Skill) isArgumentName(name string) bool {
	if name == "ARGUMENTS" {
		return true
	}
	if _, ok := s.numericArgument(name); ok {
		return true
	}
	return slices.Contains(s.Arguments, name)
}

func (s *Skill) numericArgument(value string) (int, bool) {
	index, err := strconv.Atoi(value)
	if err != nil {
		return 0, false
	}
	if s.profile == ClaudeSkillsProfile {
		return index, index >= 0
	}
	return index - 1, index > 0
}

func splitShellArguments(value string) []string {
	var result []string
	var current strings.Builder
	var quote rune
	escaped := false
	started := false
	flush := func() {
		if started {
			result = append(result, current.String())
			current.Reset()
			started = false
		}
	}
	for _, r := range value {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
			started = true
		case r == '\\' && quote != '\'':
			escaped = true
			started = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
			started = true
		case r == '\'' || r == '"':
			quote = r
			started = true
		case unicode.IsSpace(r):
			flush()
		default:
			current.WriteRune(r)
			started = true
		}
	}
	if escaped {
		current.WriteRune('\\')
	}
	flush()
	return result
}

func resolveArgument(fields []string, index int) string {
	if index >= 0 && index < len(fields) {
		return fields[index]
	}
	return ""
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
	sources := []skillSource{{filepath.Join(root, ".wingman", "skills"), AgentSkillsProfile}}
	for _, dir := range skillDirsThroughRepo(root, ".agents") {
		sources = append(sources, skillSource{dir, AgentSkillsProfile})
	}
	for _, dir := range skillDirsThroughRepo(root, ".claude") {
		sources = append(sources, skillSource{dir, ClaudeSkillsProfile})
	}
	return discover(sources, root), nil
}

func DiscoverPersonal() ([]Skill, error) {
	if _, err := os.UserHomeDir(); err != nil {
		return nil, err
	}

	home, _ := os.UserHomeDir()
	return discover([]skillSource{
		{filepath.Join(home, ".wingman", "skills"), AgentSkillsProfile},
		{filepath.Join(home, ".agents", "skills"), AgentSkillsProfile},
		{filepath.Join(home, ".claude", "skills"), ClaudeSkillsProfile},
	}, ""), nil
}

func MustDiscoverPersonal() []Skill {
	skills, _ := DiscoverPersonal()
	return skills
}

// LoadDir loads every skill directly beneath dir, one level deep. Locations are
// absolute and parse failures are reported and skipped.
func LoadDir(dir string) []Skill {
	return loadDir(dir, "*/SKILL.md", AgentSkillsProfile)
}

// LoadDirRecursive loads skills at any depth beneath dir. Codex uses this for
// legacy plugin manifests, while portable Agent Plugins use direct children.
func LoadDirRecursive(dir string) []Skill {
	return loadDir(dir, "**/SKILL.md", AgentSkillsProfile)
}

// LoadDirRecursiveCodex accepts an Agent Skills document at the declared root
// itself as well as nested skill directories, matching Codex plugin loading.
func LoadDirRecursiveCodex(dir string) []Skill {
	return loadDir(dir, "**/SKILL.md", CodexSkillsProfile)
}

// LoadDirRecursiveClaude loads Claude-compatible skills, whose frontmatter is
// intentionally more permissive than the Agent Skills specification.
func LoadDirRecursiveClaude(dir string) []Skill {
	return loadDir(dir, "**/SKILL.md", ClaudeSkillsProfile)
}

// LoadClaudeFile loads Claude's single-skill plugin form, where SKILL.md sits
// at the plugin root and its optional name controls invocation.
func LoadClaudeFile(path string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}
	skill, _, err := parseSkillDataProfile(string(data), ClaudeSkillsProfile)
	if err != nil {
		return Skill{}, err
	}
	if skill.Name == "" {
		skill.Name = filepath.Base(filepath.Dir(path))
	}
	if err := validateClaudeName(skill.Name); err != nil {
		return Skill{}, err
	}
	skill.DisplayName = skill.Name
	skill.Location = filepath.Dir(path)
	skill.profile = ClaudeSkillsProfile
	return skill, nil
}

func loadDir(dir, pattern string, profile Profile) []Skill {
	matches, err := doublestar.Glob(os.DirFS(dir), pattern)
	if err != nil {
		return nil
	}

	slices.Sort(matches)

	var skills []Skill

	for _, match := range matches {
		skillFile := filepath.Join(dir, match)

		sk, err := parseSkillFileProfile(skillFile, profile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skill: skipped %s: %v\n", skillFile, err)
			continue
		}

		sk.Location = filepath.Dir(skillFile)
		skills = append(skills, sk)
	}

	return skills
}

type skillSource struct {
	dir     string
	profile Profile
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

func discover(sources []skillSource, relativeTo string) []Skill {
	var skills []Skill
	seen := make(map[string]string)

	for _, source := range sources {
		for _, sk := range loadDir(source.dir, "*/SKILL.md", source.profile) {
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
	active := make([]Skill, 0, len(skills))
	maxDescription := 0
	for _, s := range skills {
		if s.DisableModelInvocation {
			continue
		}
		active = append(active, s)
		if length := len([]rune(s.Description)); length > maxDescription {
			maxDescription = length
		}
	}
	if len(active) == 0 {
		return ""
	}

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
	return parseSkillFileProfile(path, AgentSkillsProfile)
}

func parseSkillFileProfile(path string, profile Profile) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}

	skill, _, err := parseSkillDataProfile(string(data), profile)
	if err != nil {
		return Skill{}, err
	}
	directoryName := filepath.Base(filepath.Dir(path))
	if profile == ClaudeSkillsProfile {
		skill.DisplayName = skill.Name
		skill.Name = directoryName
		if err := validateClaudeName(skill.Name); err != nil {
			return Skill{}, err
		}
		skill.profile = profile
		return skill, nil
	}
	loadCodexMetadata(&skill, filepath.Dir(path))
	if profile == AgentSkillsProfile && skill.Name != directoryName {
		return Skill{}, fmt.Errorf("skill name %q must match parent directory %q", skill.Name, filepath.Base(filepath.Dir(path)))
	}
	return skill, nil
}

// loadCodexMetadata applies the one agents/openai.yaml setting that affects
// Wingman's runtime behavior. Codex treats this file as optional metadata and
// fails open when it is absent or malformed, so a bad decoration never hides
// an otherwise valid Agent Skill.
func loadCodexMetadata(skill *Skill, skillDir string) {
	content, err := os.ReadFile(filepath.Join(skillDir, "agents", "openai.yaml"))
	if err != nil {
		return
	}
	var metadata struct {
		Policy struct {
			AllowImplicitInvocation *bool `yaml:"allow_implicit_invocation"`
		} `yaml:"policy"`
	}
	if err := yaml.Load(content, &metadata); err != nil {
		return
	}
	skill.AllowImplicitInvocation = metadata.Policy.AllowImplicitInvocation
	if skill.AllowImplicitInvocation != nil && !*skill.AllowImplicitInvocation {
		skill.DisableModelInvocation = true
	}
}

func parseSkillData(data string) (Skill, string, error) {
	return parseSkillDataProfile(data, AgentSkillsProfile)
}

type skillFrontmatter struct {
	Name          *string        `yaml:"name"`
	Description   *string        `yaml:"description"`
	License       string         `yaml:"license"`
	Compatibility *string        `yaml:"compatibility"`
	Metadata      map[string]any `yaml:"metadata"`
	AllowedTools  any            `yaml:"allowed-tools"`
}

type claudeFrontmatter struct {
	WhenToUse              string `yaml:"when_to_use"`
	ArgumentHint           string `yaml:"argument-hint"`
	Arguments              any    `yaml:"arguments"`
	DisableModelInvocation any    `yaml:"disable-model-invocation"`
	UserInvocable          any    `yaml:"user-invocable"`
	DisallowedTools        any    `yaml:"disallowed-tools"`
	Model                  string `yaml:"model"`
	Effort                 string `yaml:"effort"`
	Context                string `yaml:"context"`
	Agent                  string `yaml:"agent"`
	Background             any    `yaml:"background"`
	Paths                  any    `yaml:"paths"`
	Shell                  string `yaml:"shell"`
	Hooks                  any    `yaml:"hooks"`
}

func parseSkillDataProfile(data string, profile Profile) (Skill, string, error) {
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
	var claude claudeFrontmatter
	var present map[string]any
	if len(bytes.TrimSpace(frontmatter)) > 0 {
		if err := yaml.Load(frontmatter, &raw); err != nil {
			return Skill{}, "", fmt.Errorf("failed to parse frontmatter: %w", err)
		}
		if profile == ClaudeSkillsProfile {
			if err := yaml.Load(frontmatter, &claude); err != nil {
				return Skill{}, "", fmt.Errorf("failed to parse Claude frontmatter: %w", err)
			}
		}
		if err := yaml.Load(frontmatter, &present); err != nil {
			return Skill{}, "", fmt.Errorf("failed to parse frontmatter: %w", err)
		}
	}
	content := strings.TrimSpace(strings.Join(lines[closing+1:], "\n"))
	skill, err := normalizeSkill(raw, claude, present, content, profile)
	if err != nil {
		return Skill{}, "", err
	}
	return skill, content, nil
}

func normalizeSkill(raw skillFrontmatter, claude claudeFrontmatter, present map[string]any, content string, profile Profile) (Skill, error) {
	skill := Skill{
		License:      raw.License,
		WhenToUse:    strings.TrimSpace(claude.WhenToUse),
		ArgumentHint: claude.ArgumentHint,
		Model:        claude.Model,
		Effort:       claude.Effort,
		Context:      claude.Context,
		Agent:        claude.Agent,
		Shell:        claude.Shell,
		Hooks:        claude.Hooks,
		profile:      profile,
	}
	var err error
	if profile == ClaudeSkillsProfile {
		if disabled, boolErr := flexibleBool(claude.DisableModelInvocation); boolErr != nil {
			return Skill{}, fmt.Errorf("disable-model-invocation: %w", boolErr)
		} else if disabled != nil {
			skill.DisableModelInvocation = *disabled
		}
		if skill.UserInvocable, err = flexibleBool(claude.UserInvocable); err != nil {
			return Skill{}, fmt.Errorf("user-invocable: %w", err)
		}
		if skill.Background, err = flexibleBool(claude.Background); err != nil {
			return Skill{}, fmt.Errorf("background: %w", err)
		}
		if skill.Paths, err = stringList(claude.Paths); err != nil {
			return Skill{}, fmt.Errorf("paths: %w", err)
		}
		if text, ok := claude.Paths.(string); ok {
			skill.Paths = splitCommaList(text)
		}
		if skill.Shell != "" && skill.Shell != "bash" && skill.Shell != "powershell" {
			return Skill{}, fmt.Errorf("shell must be bash or powershell")
		}
	}
	skill.Metadata = make(map[string]string)
	for name, value := range raw.Metadata {
		if text, ok := value.(string); ok {
			skill.Metadata[name] = text
			continue
		}
		if profile != ClaudeSkillsProfile {
			return Skill{}, fmt.Errorf("skill metadata value %q must be a string", name)
		}
	}
	if profile == ClaudeSkillsProfile {
		skill.ClaudeMetadata = raw.Metadata
	}
	if raw.Name != nil {
		skill.Name = strings.TrimSpace(*raw.Name)
	}
	if raw.Description != nil {
		skill.Description = strings.TrimSpace(*raw.Description)
	}
	if raw.Compatibility != nil {
		skill.Compatibility = strings.TrimSpace(*raw.Compatibility)
	}
	if skill.AllowedTools, err = stringList(raw.AllowedTools); err != nil {
		return Skill{}, fmt.Errorf("allowed-tools: %w", err)
	}
	if profile != ClaudeSkillsProfile {
		if _, ok := raw.AllowedTools.(string); raw.AllowedTools != nil && !ok {
			return Skill{}, fmt.Errorf("allowed-tools must be a string in an Agent Skill")
		}
	} else {
		if text, ok := raw.AllowedTools.(string); ok {
			skill.AllowedTools = splitToolRules(text)
		}
		if skill.DisallowedTools, err = stringList(claude.DisallowedTools); err != nil {
			return Skill{}, fmt.Errorf("disallowed-tools: %w", err)
		}
		if text, ok := claude.DisallowedTools.(string); ok {
			skill.DisallowedTools = splitToolRules(text)
		}
		if skill.Arguments, err = argumentNames(claude.Arguments); err != nil {
			return Skill{}, fmt.Errorf("arguments: %w", err)
		}
	}

	if profile == ClaudeSkillsProfile {
		if skill.Description == "" {
			skill.Description = firstParagraph(content)
		}
		if skill.WhenToUse != "" {
			if skill.Description != "" {
				skill.Description += " "
			}
			skill.Description += skill.WhenToUse
		}
		skill.Description = truncateRunes(skill.Description, 1536)
		if skill.Name != "" {
			if err := validateClaudeName(skill.Name); err != nil {
				return Skill{}, err
			}
		}
		return skill, nil
	}
	if _, ok := present["compatibility"]; ok && raw.Compatibility == nil {
		return Skill{}, fmt.Errorf("skill compatibility must be a string")
	}
	if err := validateSkill(skill, present); err != nil {
		return Skill{}, err
	}
	return skill, nil
}

func stringList(value any) ([]string, error) {
	switch value := value.(type) {
	case nil:
		return nil, nil
	case string:
		return []string{value}, nil
	case []any:
		result := make([]string, len(value))
		for i, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("must be a string or list of strings")
			}
			result[i] = text
		}
		return result, nil
	default:
		return nil, fmt.Errorf("must be a string or list of strings")
	}
}

func argumentNames(value any) ([]string, error) {
	if text, ok := value.(string); ok {
		return strings.Fields(text), nil
	}
	return stringList(value)
}

func splitCommaList(value string) []string {
	var result []string
	for item := range strings.SplitSeq(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func splitToolRules(value string) []string {
	var result []string
	var current strings.Builder
	depth := 0
	flush := func() {
		if rule := strings.TrimSpace(current.String()); rule != "" {
			result = append(result, rule)
		}
		current.Reset()
	}
	for _, r := range value {
		switch {
		case r == '(':
			depth++
			current.WriteRune(r)
		case r == ')' && depth > 0:
			depth--
			current.WriteRune(r)
		case depth == 0 && (unicode.IsSpace(r) || r == ','):
			flush()
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return result
}

func flexibleBool(value any) (*bool, error) {
	if value == nil {
		return nil, nil
	}
	var result bool
	switch value := value.(type) {
	case bool:
		result = value
	case int:
		if value != 0 && value != 1 {
			return nil, fmt.Errorf("must be a boolean")
		}
		result = value == 1
	case string:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "true", "yes", "on", "1":
			result = true
		case "false", "no", "off", "0":
			result = false
		default:
			return nil, fmt.Errorf("must be a boolean")
		}
	default:
		return nil, fmt.Errorf("must be a boolean")
	}
	return &result, nil
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func validateClaudeName(name string) error {
	if len([]rune(name)) > 64 {
		return fmt.Errorf("Claude skill name exceeds 64 characters")
	}
	if name == "" || strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") || strings.Contains(name, "--") {
		return fmt.Errorf("Claude skill name %q is not valid kebab-case", name)
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return fmt.Errorf("Claude skill name %q may only use lowercase letters, digits, and hyphens", name)
		}
	}
	return nil
}

func firstParagraph(content string) string {
	var lines []string
	started := false
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if started {
				break
			}
			continue
		}
		if !started && strings.HasPrefix(line, "#") {
			continue
		}
		started = true
		lines = append(lines, line)
	}
	return strings.Join(lines, " ")
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

func readSkillContent(path string, profile Profile) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	_, content, err := parseSkillDataProfile(string(data), profile)
	return content, err
}
