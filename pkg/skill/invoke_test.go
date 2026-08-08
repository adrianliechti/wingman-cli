package skill_test

import (
	"path/filepath"
	"strings"
	"testing"

	. "github.com/adrianliechti/wingman-agent/pkg/skill"
)

func TestParseSlashCommand(t *testing.T) {
	tests := []struct {
		text     string
		wantName string
		wantOK   bool
	}{
		{"/simplify", "simplify", true},
		{"/simplify keep the api", "simplify", true},
		{"/simplify\nkeep the api", "simplify", true},
		{"/", "", true},
		{"$simplify keep the api", "", false},
		{"no command", "", false},
	}

	for _, tt := range tests {
		name, ok := ParseSlashCommand(tt.text)
		if name != tt.wantName || ok != tt.wantOK {
			t.Errorf("ParseSlashCommand(%q) = (%q, %v), want (%q, %v)",
				tt.text, name, ok, tt.wantName, tt.wantOK)
		}
	}
}

func TestInvocations(t *testing.T) {
	skills := []Skill{
		{Name: "simplify", Description: "s"},
		{Name: "code-review", Description: "r"},
	}

	tests := []struct {
		text string
		want []string
	}{
		{"/simplify keep the api", []string{"simplify"}},
		{"/simplify then /code-review it", []string{"simplify", "code-review"}},
		{"/simplify\nmultiline context", []string{"simplify"}},
		{"please /simplify this function", []string{"simplify"}},
		{"please $simplify this function", []string{"simplify"}},
		{"run /simplify then /code-review it", []string{"simplify", "code-review"}},
		{"/simplify and /simplify again", []string{"simplify"}},
		{"/simplify /code-review src/api.go", []string{"simplify", "code-review"}},
		{"case /Simplify matches", []string{"simplify"}},
		{"end with /code-review.", []string{"code-review"}},
		{"/unknown but /simplify still counts", []string{"simplify"}},
		{"look at /Users/adrian/simplify", nil},
		{"see https://example.com/simplify", nil},
		{"path/simplify is not a mention", nil},
		{"no mention at all", nil},
	}

	for _, tt := range tests {
		invs := Invocations(tt.text, skills)
		var names []string
		for _, inv := range invs {
			names = append(names, inv.Skill.Name)
		}
		if len(names) != len(tt.want) {
			t.Fatalf("Invocations(%q) = %v, want %v", tt.text, names, tt.want)
		}
		for i := range names {
			if names[i] != tt.want[i] {
				t.Fatalf("Invocations(%q) = %v, want %v", tt.text, names, tt.want)
			}
		}
	}
}

func TestInstructions(t *testing.T) {
	s := Skill{
		Name:        "greet",
		Description: "greets",
		Content:     "Greet the audience identified in the user's message.",
	}

	block, err := Invocation{Skill: &s}.Instructions("")
	if err != nil {
		t.Fatalf("Instructions: %v", err)
	}

	if !strings.HasPrefix(block, `<skill-instructions skill="greet">`) {
		t.Fatalf("missing opening tag: %q", block)
	}
	if !strings.HasSuffix(block, "</skill-instructions>") {
		t.Fatalf("missing closing tag: %q", block)
	}
	if !strings.Contains(block, "Greet the audience identified in the user's message.") {
		t.Fatalf("instructions missing: %q", block)
	}
	if !strings.Contains(block, "invoked the greet skill") {
		t.Fatalf("missing preamble: %q", block)
	}
}

func TestStackedSlashSkillsShareTrailingArguments(t *testing.T) {
	skills := []Skill{{Name: "write-tests"}, {Name: "fix-issue"}}
	invs := Invocations(`/write-tests /fix-issue "123 456"`, skills)
	if len(invs) != 2 {
		t.Fatalf("invocations = %#v", invs)
	}
	for _, inv := range invs {
		if inv.Args != `"123 456"` {
			t.Fatalf("%s args = %q", inv.Skill.Name, inv.Args)
		}
	}
}

func TestInstructionsIdentifySkillResourceDirectory(t *testing.T) {
	projectDir := t.TempDir()
	skillDir := filepath.Join(projectDir, ".agents", "skills", "resourceful")
	s := Skill{
		Name:        "resourceful",
		Description: "uses resources",
		Content:     "Read ${SKILL_DIR}/references/guide.md from ${PROJECT_DIR}. Claude aliases: ${CLAUDE_SKILL_DIR} ${CLAUDE_PROJECT_DIR}.",
		Location:    skillDir,
	}

	block, err := (Invocation{Skill: &s}).Instructions(projectDir)
	if err != nil {
		t.Fatalf("Instructions: %v", err)
	}
	if !strings.Contains(block, "Skill directory: "+skillDir+". Resolve relative resources from this directory.") {
		t.Fatalf("missing resource directory: %q", block)
	}
	if !strings.Contains(block, "Read "+skillDir+"/references/guide.md from "+projectDir+". Claude aliases: "+skillDir+" "+projectDir+".") {
		t.Fatalf("directory substitutions missing: %q", block)
	}
}
