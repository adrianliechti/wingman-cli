package skill_test

import (
	"strings"
	"testing"

	. "github.com/adrianliechti/wingman-agent/pkg/skill"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		text     string
		wantName string
		wantArgs string
		wantOK   bool
	}{
		{"/simplify", "simplify", "", true},
		{"/simplify keep the api", "simplify", "keep the api", true},
		{"/simplify\nkeep the api", "simplify", "keep the api", true},
		{"/", "", "", true},
		{"no command", "", "", false},
	}

	for _, tt := range tests {
		name, args, ok := ParseCommand(tt.text)
		if name != tt.wantName || args != tt.wantArgs || ok != tt.wantOK {
			t.Errorf("ParseCommand(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.text, name, args, ok, tt.wantName, tt.wantArgs, tt.wantOK)
		}
	}
}

func TestInvocations(t *testing.T) {
	skills := []Skill{
		{Name: "simplify", Description: "s"},
		{Name: "code-review", Description: "r"},
	}

	tests := []struct {
		text     string
		want     []string
		wantArgs string
	}{
		{"/simplify keep the api", []string{"simplify"}, "keep the api"},
		{"/simplify then /code-review it", []string{"simplify"}, "then /code-review it"},
		{"/simplify\nmultiline args", []string{"simplify"}, "multiline args"},
		{"please /simplify this function", []string{"simplify"}, ""},
		{"run /simplify then /code-review it", []string{"simplify", "code-review"}, ""},
		{"/simplify and /simplify again", []string{"simplify"}, "and /simplify again"},
		{"case /Simplify matches", []string{"simplify"}, ""},
		{"end with /code-review.", []string{"code-review"}, ""},
		{"/unknown but /simplify still counts", []string{"simplify"}, ""},
		{"look at /Users/adrian/simplify", nil, ""},
		{"see https://example.com/simplify", nil, ""},
		{"path/simplify is not a mention", nil, ""},
		{"no mention at all", nil, ""},
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
		if len(invs) > 0 && invs[0].Args != tt.wantArgs {
			t.Fatalf("Invocations(%q) args = %q, want %q", tt.text, invs[0].Args, tt.wantArgs)
		}
	}
}

func TestInstructions(t *testing.T) {
	s := Skill{
		Name:        "greet",
		Description: "greets",
		Content:     "Say hello to $ARGUMENTS!",
	}

	block, err := Invocation{Skill: &s, Args: "the team"}.Instructions("")
	if err != nil {
		t.Fatalf("Instructions: %v", err)
	}

	if !strings.HasPrefix(block, `<skill-instructions skill="greet">`) {
		t.Fatalf("missing opening tag: %q", block)
	}
	if !strings.HasSuffix(block, "</skill-instructions>") {
		t.Fatalf("missing closing tag: %q", block)
	}
	if !strings.Contains(block, "Say hello to the team!") {
		t.Fatalf("arguments not applied: %q", block)
	}
	if !strings.Contains(block, "invoked the /greet skill") {
		t.Fatalf("missing preamble: %q", block)
	}
}

func TestInstructionsIdentifySkillResourceDirectory(t *testing.T) {
	dir := t.TempDir()
	s := Skill{Name: "resourceful", Description: "uses resources", Content: "Read references/guide.md.", Location: dir}

	block, err := (Invocation{Skill: &s}).Instructions("")
	if err != nil {
		t.Fatalf("Instructions: %v", err)
	}
	if !strings.Contains(block, "Skill directory: "+dir+". Resolve relative resources from this directory.") {
		t.Fatalf("missing resource directory: %q", block)
	}
}
