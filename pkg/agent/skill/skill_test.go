package skill

import (
	"testing"
	"testing/fstest"
)

func TestParseSkillData(t *testing.T) {
	data := `---
name: test-skill
description: A test skill
when-to-use: When testing
arguments: [query, file]
---
# Test Skill

Do the thing with ${ARGUMENTS}.
Use ${query} to search in ${file}.`

	skill, content, err := parseSkillData(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if skill.Name != "test-skill" {
		t.Errorf("Name = %q, want %q", skill.Name, "test-skill")
	}
	if skill.Description != "A test skill" {
		t.Errorf("Description = %q, want %q", skill.Description, "A test skill")
	}
	if skill.WhenToUse != "When testing" {
		t.Errorf("WhenToUse = %q, want %q", skill.WhenToUse, "When testing")
	}
	if len(skill.Arguments) != 2 || skill.Arguments[0] != "query" || skill.Arguments[1] != "file" {
		t.Errorf("Arguments = %v, want [query file]", skill.Arguments)
	}
	if content == "" {
		t.Error("expected non-empty content")
	}
	if content != "# Test Skill\n\nDo the thing with ${ARGUMENTS}.\nUse ${query} to search in ${file}." {
		t.Errorf("unexpected content: %q", content)
	}
}

func TestParseSkillData_MissingFields(t *testing.T) {
	data := `---
name: incomplete
---
Content here.`

	_, _, err := parseSkillData(data)
	if err == nil {
		t.Error("expected error for missing description")
	}
}

func TestApplyArguments(t *testing.T) {
	s := Skill{
		Arguments: []string{"query", "file"},
	}

	content := "Search for ${ARGUMENTS}. Use ${query} in ${file}."

	// Last argument gets everything remaining
	result := s.ApplyArguments(content, "foo bar.go baz")

	if result != "Search for foo bar.go baz. Use foo in bar.go baz." {
		t.Errorf("got %q", result)
	}
}

func TestApplyArguments_LastArgGetsRemainder(t *testing.T) {
	s := Skill{
		Arguments: []string{"message"},
	}

	content := "Commit: ${message}"

	// Single argument gets the full string including spaces
	result := s.ApplyArguments(content, "fix the login bug")

	if result != "Commit: fix the login bug" {
		t.Errorf("got %q", result)
	}
}

func TestApplyArguments_NoArgs(t *testing.T) {
	s := Skill{}
	content := "No args: ${ARGUMENTS}."

	result := s.ApplyArguments(content, "hello world")

	if result != "No args: hello world." {
		t.Errorf("got %q", result)
	}
}

func TestApplyArguments_Empty(t *testing.T) {
	s := Skill{
		Arguments: []string{"x"},
	}
	content := "Value: ${x}, all: ${ARGUMENTS}."

	result := s.ApplyArguments(content, "")

	if result != "Value: , all: ." {
		t.Errorf("got %q", result)
	}
}

func TestLoadBundled(t *testing.T) {
	fs := fstest.MapFS{
		"skills/my-skill/SKILL.md": &fstest.MapFile{
			Data: []byte(`---
name: my-skill
description: Does things
when-to-use: Always
---
# My Skill

Do the thing.`),
		},
		"skills/bad-skill/SKILL.md": &fstest.MapFile{
			Data: []byte(`not valid frontmatter`),
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

func TestBundledSkills(t *testing.T) {
	skills := BundledSkills()

	if len(skills) < 4 {
		t.Fatalf("expected at least 4 bundled skills, got %d", len(skills))
	}

	names := make(map[string]bool)
	for _, s := range skills {
		names[s.Name] = true
		if !s.Bundled {
			t.Errorf("skill %q not marked as bundled", s.Name)
		}
		if s.Content == "" {
			t.Errorf("skill %q has empty content", s.Name)
		}
	}

	for _, name := range []string{"simplify", "commit", "review", "init"} {
		if !names[name] {
			t.Errorf("missing bundled skill %q", name)
		}
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
		{Name: "Simplify", Location: ".skills/simplify"}, // overrides bundled (case-insensitive)
		{Name: "custom", Location: ".skills/custom"},
	}

	result := Merge(bundled, discovered)

	if len(result) != 3 {
		t.Fatalf("expected 3 skills, got %d", len(result))
	}

	// commit (bundled, not overridden)
	if result[0].Name != "commit" || !result[0].Bundled {
		t.Errorf("expected bundled commit first, got %q bundled=%v", result[0].Name, result[0].Bundled)
	}
	// Simplify (discovered, overrides bundled)
	if result[1].Name != "Simplify" || result[1].Bundled {
		t.Errorf("expected discovered Simplify, got %q bundled=%v", result[1].Name, result[1].Bundled)
	}
	// custom (discovered, new)
	if result[2].Name != "custom" {
		t.Errorf("expected custom, got %q", result[2].Name)
	}
}

func TestFormatForPrompt(t *testing.T) {
	skills := []Skill{
		{Name: "test", Description: "Test skill", WhenToUse: "When testing", Location: ".skills/test", Bundled: false},
		{Name: "builtin", Description: "Built-in skill", Bundled: true},
	}

	result := FormatForPrompt(skills)

	if !contains(result, "<name>test</name>") {
		t.Error("expected test skill name")
	}
	if !contains(result, "<when-to-use>When testing</when-to-use>") {
		t.Error("expected when-to-use for test skill")
	}
	if !contains(result, "<location>.skills/test/SKILL.md</location>") {
		t.Error("expected location for file-based skill")
	}
	// Bundled skill should NOT have a location tag
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
