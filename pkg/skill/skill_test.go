package skill_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/adrianliechti/wingman-agent/internal/testenv"
	. "github.com/adrianliechti/wingman-agent/pkg/skill"
)

func TestDiscoverParsesSkillData(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, ".wingman", "skills", "test-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: test-skill
description: A test skill
license: Apache-2.0
compatibility: Requires git.
metadata:
  category: testing
allowed-tools: Shell(git:*)
---
# Test Skill

Do the thing.`), 0644); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	skills, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover error: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("skills = %#v, want one skill", skills)
	}

	skill := skills[0]
	if skill.Name != "test-skill" || skill.Description != "A test skill" {
		t.Fatalf("skill metadata = %#v", skill)
	}
	if skill.License != "Apache-2.0" || skill.Compatibility != "Requires git." || skill.Metadata["category"] != "testing" || !reflect.DeepEqual(skill.AllowedTools, []string{"Shell(git:*)"}) {
		t.Fatalf("standard optional metadata = %#v", skill)
	}
	content, err := skill.GetContent(root)
	if err != nil {
		t.Fatalf("GetContent: %v", err)
	}
	if content != "# Test Skill\n\nDo the thing." {
		t.Fatalf("unexpected content: %q", content)
	}
}

func TestDiscoverSkipsSkillDataMissingFields(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, ".wingman", "skills", "incomplete")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: incomplete
---
Content here.`), 0644); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	skills, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover error: %v", err)
	}
	if len(skills) != 0 {
		t.Fatalf("expected invalid skill to be skipped, got %#v", skills)
	}
}

func TestDiscoverFindsSkillsBelowGroupingDirectories(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, ".agents", "skills", "team", "frontend", "reviews", "accessibility")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: accessibility\ndescription: Review frontend accessibility.\n---\nReview the UI."), 0644); err != nil {
		t.Fatal(err)
	}
	// A nested SKILL.md is a resource of accessibility, not another discovered
	// skill, because the first skill directory is a discovery boundary.
	resourceDir := filepath.Join(skillDir, "examples", "nested")
	if err := os.MkdirAll(resourceDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resourceDir, "SKILL.md"), []byte("---\nname: nested\ndescription: Must not be discovered.\n---\nNo."), 0644); err != nil {
		t.Fatal(err)
	}

	skills, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != "accessibility" {
		t.Fatalf("skills = %#v, want grouped accessibility skill only", skills)
	}
}

func TestClaudeArgumentSubstitution(t *testing.T) {
	s := Skill{}
	content := `All: $ARGUMENTS; first: $ARGUMENTS[0]; second: $1; missing: $3; huge: $999999999999999999999999999; escaped: \$0; doubled: \\$0`
	got := s.ApplySubstitutions(content, `"hello world" second`, "", "")
	want := `All: "hello world" second; first: hello world; second: second; missing: $3; huge: $999999999999999999999999999; escaped: $0; doubled: \\hello world`
	if got != want {
		t.Fatalf("ApplySubstitutions = %q, want %q", got, want)
	}
}

func TestClaudeArgumentsAreAppendedWhenNoPlaceholderExists(t *testing.T) {
	got := (&Skill{}).ApplySubstitutions("Do the work.", "src/api", "", "")
	if got != "Do the work.\n\nARGUMENTS: src/api" {
		t.Fatalf("ApplySubstitutions = %q", got)
	}
}

func TestSkillDirectorySubstitutionsHaveNeutralAndClaudeNames(t *testing.T) {
	content := `${SKILL_DIR}|${PROJECT_DIR}|${CLAUDE_SKILL_DIR}|${CLAUDE_PROJECT_DIR}`
	got := (&Skill{}).ApplySubstitutions(content, "", "/skills/review", "/workspace")
	want := `/skills/review|/workspace|/skills/review|/workspace`
	if got != want {
		t.Fatalf("ApplySubstitutions = %q, want %q", got, want)
	}
}

func TestClaudeNamedArgumentsAndDerivedHint(t *testing.T) {
	s := Skill{Arguments: []string{"component", "from", "to"}}
	content := `Migrate $component from $from to $to; missing: $missing; escaped: \$component.`
	got := s.ApplySubstitutions(content, `"Search Bar" React`, "", "")
	want := `Migrate Search Bar from React to ; missing: $missing; escaped: $component.`
	if got != want {
		t.Fatalf("ApplySubstitutions = %q, want %q", got, want)
	}
	if hint := s.InvocationHint(); hint != "[component] [from] [to]" {
		t.Fatalf("InvocationHint = %q", hint)
	}
}

func TestClaudeArgumentFrontmatterStringListAndExplicitHint(t *testing.T) {
	for _, test := range []struct {
		name      string
		arguments string
		want      []string
	}{
		{"string", "component from to", []string{"component", "from", "to"}},
		{"list", "[component, from, to]", []string{"component", "from", "to"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "migrate")
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			data := "---\nname: migrate\ndescription: Migrate a component.\narguments: " + test.arguments + "\nargument-hint: <component> <source> <target>\n---\nMigrate $component."
			if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(data), 0644); err != nil {
				t.Fatal(err)
			}
			skills := LoadDir(root)
			if len(skills) != 1 || !reflect.DeepEqual(skills[0].Arguments, test.want) {
				t.Fatalf("skills = %#v", skills)
			}
			if skills[0].InvocationHint() != "<component> <source> <target>" || skills[0].Portable() {
				t.Fatalf("Claude extension metadata = %#v", skills[0])
			}
		})
	}
}

func TestClaudeNamedArgumentsRejectInvalidNames(t *testing.T) {
	for _, arguments := range []string{"[ARGUMENTS]", "[1st]", "[two words]", "[same, same]"} {
		root := t.TempDir()
		dir := filepath.Join(root, "invalid")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		data := "---\nname: invalid\ndescription: Invalid arguments.\narguments: " + arguments + "\n---\nbody"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
		if skills := LoadDir(root); len(skills) != 0 {
			t.Fatalf("arguments %q accepted: %#v", arguments, skills)
		}
	}
}

func TestLoadBundled(t *testing.T) {
	fs := fstest.MapFS{
		"skills/my-skill/SKILL.md": &fstest.MapFile{
			Data: []byte(`---
name: my-skill
description: Does things
---
# My Skill

Do the thing.`),
		},
		"skills/bad-skill/SKILL.md": &fstest.MapFile{
			Data: []byte(`not valid frontmatter`),
		},
		"skills/my-skill/assets/example.txt": &fstest.MapFile{
			Data: []byte("example asset\n"),
		},
		"skills/my-skill/references/guide.md": &fstest.MapFile{
			Data: []byte("# Guide\n"),
		},
	}

	skills, err := LoadBundled(fs, "skills")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}

	s := skills[0]
	if s.Name != "my-skill" {
		t.Errorf("Name = %q", s.Name)
	}
	if !s.Bundled {
		t.Error("expected Bundled = true")
	}
	if s.Content != "# My Skill\n\nDo the thing." {
		t.Errorf("Content = %q", s.Content)
	}
}

func TestLoadBundledAtSetsResourceLocation(t *testing.T) {
	fsys := fstest.MapFS{
		"skills/my-skill/SKILL.md": &fstest.MapFile{Data: []byte(`---
name: my-skill
description: Does things
---
Do the thing.`)},
	}

	skills, err := LoadBundledAt(fsys, "skills", "/managed/skills")
	if err != nil || len(skills) != 1 {
		t.Fatalf("LoadBundledAt = %#v, %v", skills, err)
	}
	if got, want := skills[0].Location, filepath.Join("/managed/skills", "my-skill"); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

func TestFindSkill(t *testing.T) {
	skills := []Skill{
		{Name: "foo"},
		{Name: "Bar"},
		{Name: "baz-qux"},
	}

	if s := FindSkill("foo", skills); s == nil || s.Name != "foo" {
		t.Error("expected to find 'foo'")
	}
	if s := FindSkill("BAR", skills); s == nil || s.Name != "Bar" {
		t.Error("expected case-insensitive find for 'Bar'")
	}
	if s := FindSkill("baz-qux", skills); s == nil {
		t.Error("expected to find 'baz-qux'")
	}
	if s := FindSkill("missing", skills); s != nil {
		t.Error("expected nil for missing skill")
	}
}

func TestMerge(t *testing.T) {
	bundled := []Skill{
		{Name: "simplify", Bundled: true, Content: "bundled content"},
		{Name: "commit", Bundled: true, Content: "bundled commit"},
	}
	discovered := []Skill{
		{Name: "Simplify", Location: ".skills/simplify"},
		{Name: "custom", Location: ".skills/custom"},
	}

	result := Merge(bundled, discovered)

	if len(result) != 3 {
		t.Fatalf("expected 3 skills, got %d", len(result))
	}

	if result[0].Name != "commit" || !result[0].Bundled {
		t.Errorf("expected bundled commit first, got %q bundled=%v", result[0].Name, result[0].Bundled)
	}

	if result[1].Name != "Simplify" || result[1].Bundled {
		t.Errorf("expected discovered Simplify, got %q bundled=%v", result[1].Name, result[1].Bundled)
	}

	if result[2].Name != "custom" {
		t.Errorf("expected custom, got %q", result[2].Name)
	}
}

func TestFormatForPrompt(t *testing.T) {
	skills := []Skill{
		{Name: "test", Description: "Test skill", Location: ".skills/test", Bundled: false},
		{Name: "builtin", Description: "Built-in skill", Bundled: true},
	}

	result := FormatForPrompt(skills)

	if !contains(result, "<name>test</name>") {
		t.Error("expected test skill name")
	}
	if !contains(result, "<location>.skills/test/SKILL.md</location>") {
		t.Error("expected location for file-based skill")
	}

	if contains(result, "<location>builtin") {
		t.Error("bundled skill should not have location tag")
	}
}

func TestFormatForPrompt_Empty(t *testing.T) {
	result := FormatForPrompt(nil)
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestFormatForPromptEscapesXMLAndStaysWithinBudget(t *testing.T) {
	skills := []Skill{{
		Name:        `a&b`,
		Description: `<unsafe> & "quoted"`,
		Location:    `.agents/skills/a&b`,
	}}
	for i := range 20 {
		skills = append(skills, Skill{Name: "large-" + strings.Repeat("x", i), Description: strings.Repeat("z", 1024)})
	}
	result := FormatForPrompt(skills)
	if strings.Contains(result, "<unsafe>") || !strings.Contains(result, "a&amp;b") || !strings.Contains(result, "&lt;unsafe&gt;") {
		t.Fatalf("prompt was not XML escaped: %q", result)
	}
	if len(result) > 8000 || !strings.HasSuffix(result, "</available_skills>") {
		t.Fatalf("prompt length = %d or document is incomplete", len(result))
	}
}

func writeSkill(t *testing.T, dir, name, description string) {
	t.Helper()

	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}

	body := "---\nname: " + name + "\ndescription: " + description + "\n---\n# " + name + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0644); err != nil {
		t.Fatalf("write skill %s: %v", dir, err)
	}
}

func TestDiscoverPrefersWingmanOverAgentsAndClaude(t *testing.T) {
	root := t.TempDir()

	writeSkill(t, filepath.Join(root, ".claude", "skills", "deploy"), "deploy", "from claude")
	writeSkill(t, filepath.Join(root, ".agents", "skills", "deploy"), "deploy", "from agents")
	writeSkill(t, filepath.Join(root, ".wingman", "skills", "deploy"), "deploy", "from wingman")

	skills, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover error: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("skills = %#v, want exactly one", skills)
	}
	if skills[0].Description != "from wingman" {
		t.Fatalf("winner = %q, want the .wingman copy", skills[0].Description)
	}
}

func TestDiscoverIgnoresOpencode(t *testing.T) {
	root := t.TempDir()

	writeSkill(t, filepath.Join(root, ".opencode", "skills", "legacy"), "legacy", "from opencode")

	skills, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover error: %v", err)
	}
	if len(skills) != 0 {
		t.Fatalf("skills = %#v, want none", skills)
	}
}

func TestDiscoverPersonalIgnoresReservedSystemSkills(t *testing.T) {
	home := testenv.UserHome(t)
	wingmanHome := testenv.WingmanHome(t)

	writeSkill(t, filepath.Join(wingmanHome, "skills", ".system", "bundled"), "bundled", "system")
	writeSkill(t, filepath.Join(wingmanHome, "skills", "personal"), "personal", "user")

	skills, err := DiscoverPersonal()
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != "personal" {
		t.Fatalf("personal skills = %#v, want only personal", skills)
	}

	agentsDir := filepath.Join(home, ".agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(wingmanHome, "skills"), filepath.Join(agentsDir, "skills")); err != nil {
		t.Skipf("skill-root symlink is unavailable: %v", err)
	}
	skills, err = DiscoverPersonal()
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != "personal" {
		t.Fatalf("personal skills through alias = %#v, want only personal", skills)
	}
}

func TestMergeKeepsShadowedPluginSkillQualified(t *testing.T) {
	plugin := []Skill{{Name: "deploy", Description: "from plugin", Plugin: "ops"}}
	project := []Skill{{Name: "deploy", Description: "from project"}}

	merged := Merge(plugin, project)

	if len(merged) != 2 {
		t.Fatalf("merged = %#v, want both entries", merged)
	}

	bare := FindSkill("deploy", merged)
	if bare == nil || bare.Description != "from project" {
		t.Fatalf("bare lookup = %#v, want the project skill", bare)
	}

	qualified := FindSkill("ops:deploy", merged)
	if qualified == nil || qualified.Description != "from plugin" {
		t.Fatalf("qualified lookup = %#v, want the plugin skill", qualified)
	}
}

func TestMergeDropsShadowedNonPluginSkill(t *testing.T) {
	bundled := []Skill{{Name: "commit", Description: "bundled"}}
	project := []Skill{{Name: "commit", Description: "project"}}

	merged := Merge(bundled, project)

	if len(merged) != 1 || merged[0].Description != "project" {
		t.Fatalf("merged = %#v, want only the project skill", merged)
	}
}

func TestLoadDirEnforcesAgentSkillsNameAndDirectoryRules(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "right-name"), "other-name", "mismatch")
	writeSkill(t, filepath.Join(root, "Bad-Name"), "Bad-Name", "uppercase")
	writeSkill(t, filepath.Join(root, "double--hyphen"), "double--hyphen", "repeated separator")
	writeSkill(t, filepath.Join(root, "valid-skill"), "valid-skill", "valid")

	skills := LoadDir(root)
	if len(skills) != 1 || skills[0].Name != "valid-skill" {
		t.Fatalf("skills = %#v, want only the Agent Skills-conformant entry", skills)
	}
}

func TestLoadDirEnforcesAgentSkillsLengthLimits(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "long-description"), "long-description", strings.Repeat("x", 1025))

	dir := filepath.Join(root, "long-compatibility")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	data := "---\nname: long-compatibility\ndescription: valid\ncompatibility: " + strings.Repeat("x", 501) + "\n---\nbody"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	if skills := LoadDir(root); len(skills) != 0 {
		t.Fatalf("skills = %#v, want over-limit skills skipped", skills)
	}
}

func TestLoadDirRejectsEmptyAgentSkillsCompatibility(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "empty-compatibility")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: empty-compatibility\ndescription: valid\ncompatibility: ''\n---\nbody"), 0644); err != nil {
		t.Fatal(err)
	}
	if skills := LoadDir(root); len(skills) != 0 {
		t.Fatalf("skills = %#v, want explicit empty compatibility rejected", skills)
	}
}

func TestLoadDirRejectsNullAgentSkillsOptionalFields(t *testing.T) {
	for _, field := range []string{"license", "compatibility", "metadata", "allowed-tools"} {
		t.Run(field, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "null-field")
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			data := "---\nname: null-field\ndescription: valid\n" + field + ": null\n---\nbody"
			if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(data), 0644); err != nil {
				t.Fatal(err)
			}
			if skills := LoadDir(root); len(skills) != 0 {
				t.Fatalf("skills = %#v, want null %s rejected", skills, field)
			}
		})
	}
}

func TestLoadDirRejectsProprietaryClaudeFrontmatter(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "review")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: review\ndescription: Review code.\nuser-invocable: false\n---\nbody"), 0644); err != nil {
		t.Fatal(err)
	}
	if skills := LoadDir(root); len(skills) != 0 {
		t.Fatalf("skills = %#v, want proprietary frontmatter rejected", skills)
	}
}

func TestDiscoverLoadsAgentSkillsFromWorkingDirectoryToRepoRoot(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	workDir := filepath.Join(repo, "src", "nested")
	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, filepath.Join(repo, ".agents", "skills", "root-skill"), "root-skill", "from repo root")
	writeSkill(t, filepath.Join(repo, "src", ".agents", "skills", "near-skill"), "near-skill", "from parent")
	skills, err := Discover(workDir)
	if err != nil || FindSkill("root-skill", skills) == nil || FindSkill("near-skill", skills) == nil {
		t.Fatalf("skills = %#v, err = %v", skills, err)
	}
}

func TestDiscoverLoadsStandardSkillsFromClaudeLocationToRepoRoot(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	workDir := filepath.Join(repo, "src", "nested")
	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(repo, ".claude", "skills", "root-claude")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: root-claude\ndescription: Standard skill in Claude's location.\n---\nbody"), 0644); err != nil {
		t.Fatal(err)
	}
	skills, err := Discover(workDir)
	if err != nil || FindSkill("root-claude", skills) == nil {
		t.Fatalf("skills = %#v, err = %v", skills, err)
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && s != substr && indexOf(s, substr) >= 0
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
