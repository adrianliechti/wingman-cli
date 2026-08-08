package skill

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

type Invocation struct {
	Skill *Skill
	Args  string
}

// A qualified "<plugin>:<skill>" mention is tried first; the bare alternative
// deliberately excludes dots so a sentence-final "/deploy." still resolves.
var inlinePattern = regexp.MustCompile(`(^|\s)(?:/|\$)([A-Za-z0-9][A-Za-z0-9_-]*(?:\.[A-Za-z0-9_-]+)*:[A-Za-z0-9][A-Za-z0-9_-]*|[A-Za-z0-9][A-Za-z0-9_-]*)`)

// ParseSlashCommand returns the name of a leading Wingman slash command.
func ParseSlashCommand(text string) (string, bool) {
	if !strings.HasPrefix(text, "/") {
		return "", false
	}
	rest := text[1:]
	if i := strings.IndexAny(rest, " \t\n"); i >= 0 {
		return rest[:i], true
	}
	return rest, true
}

// Invocations returns every /skill or Codex $skill mention, deduplicated in
// order of first appearance. A mention only counts when its sigil starts a
// word, so paths and URLs never match.
func Invocations(text string, skills []Skill) []Invocation {
	var invs []Invocation
	seen := make(map[string]bool)

	// Claude-style leading slash invocations may be stacked. The text after the
	// last recognized leading skill is passed unchanged to each stacked skill.
	leading, args := leadingInvocations(text, skills)
	for _, s := range leading {
		key := strings.ToLower(s.Qualified())
		seen[key] = true
		invs = append(invs, Invocation{Skill: s, Args: args})
	}

	for _, m := range inlinePattern.FindAllStringSubmatch(text, -1) {
		s := FindSkill(m[2], skills)
		if s == nil {
			continue
		}
		key := strings.ToLower(s.Qualified())
		if seen[key] {
			continue
		}
		seen[key] = true
		invs = append(invs, Invocation{Skill: s})
	}

	return invs
}

func leadingInvocations(text string, skills []Skill) ([]*Skill, string) {
	const maxStacked = 6
	position := 0
	var leading []*Skill

	for len(leading) < maxStacked && position < len(text) && text[position] == '/' {
		end := position + 1
		for end < len(text) && !strings.ContainsRune(" \t\r\n", rune(text[end])) {
			end++
		}
		s := FindSkill(text[position+1:end], skills)
		if s == nil {
			break
		}
		leading = append(leading, s)
		position = end
		for position < len(text) && strings.ContainsRune(" \t\r\n", rune(text[position])) {
			position++
		}
	}
	if len(leading) == 0 {
		return nil, ""
	}
	return leading, text[position:]
}

// Instructions loads the invoked skill into the <skill-instructions> block
// attached (hidden) to the message invoking it.
func (inv Invocation) Instructions(workDir string) (string, error) {
	s := inv.Skill

	content, err := s.GetContent(workDir)
	if err != nil {
		return "", err
	}

	projectDir := workDir
	if projectDir != "" {
		if absolute, err := filepath.Abs(projectDir); err == nil {
			projectDir = absolute
		}
	}
	skillDir := s.AbsoluteDir(projectDir)
	content = s.ApplySubstitutions(content, inv.Args, skillDir, projectDir)
	source := ""
	if skillDir != "" {
		source = fmt.Sprintf("\nSkill directory: %s. Resolve relative resources from this directory.", skillDir)
	}

	name := s.Qualified()

	return fmt.Sprintf("<skill-instructions skill=%q>\nThe user invoked the %s skill; follow these instructions for this request.%s\n\n%s\n</skill-instructions>", name, name, source, content), nil
}
