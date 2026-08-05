package skill

import (
	"fmt"
	"regexp"
	"strings"
)

type Invocation struct {
	Skill *Skill
	Args  string
}

var inlinePattern = regexp.MustCompile(`(^|\s)/([A-Za-z0-9][A-Za-z0-9_-]*)`)

// ParseCommand splits a leading "/name args" invocation; args keep their
// original spacing and may span lines.
func ParseCommand(text string) (string, string, bool) {
	if !strings.HasPrefix(text, "/") {
		return "", "", false
	}
	rest := text[1:]
	if i := strings.IndexAny(rest, " \t\n"); i >= 0 {
		return rest[:i], rest[i+1:], true
	}
	return rest, "", true
}

// Invocations returns every skill text invokes: the whole text as a leading
// "/name args" command, or /name mentions anywhere inside it, deduplicated in
// order of first appearance. A mention only counts when the slash starts a
// word, so paths and URLs never match.
func Invocations(text string, skills []Skill) []Invocation {
	if name, args, ok := ParseCommand(text); ok {
		if s := FindSkill(name, skills); s != nil {
			return []Invocation{{Skill: s, Args: args}}
		}
	}

	var invs []Invocation
	seen := make(map[string]bool)

	for _, m := range inlinePattern.FindAllStringSubmatch(text, -1) {
		s := FindSkill(m[2], skills)
		if s == nil {
			continue
		}
		key := strings.ToLower(s.Name)
		if seen[key] {
			continue
		}
		seen[key] = true
		invs = append(invs, Invocation{Skill: s})
	}

	return invs
}

// Instructions loads and expands the invoked skill into the
// <skill-instructions> block that is attached (hidden) to the message
// invoking it.
func (inv Invocation) Instructions(workDir string) (string, error) {
	s := inv.Skill

	content, err := s.GetContent(workDir)
	if err != nil {
		return "", err
	}

	skillDir := s.AbsoluteDir(workDir)
	content = s.ApplyArguments(content, inv.Args, skillDir)

	source := ""
	if skillDir != "" {
		source = fmt.Sprintf("\nSkill directory: %s. Resolve relative resources from this directory.", skillDir)
	}

	return fmt.Sprintf("<skill-instructions skill=%q>\nThe user invoked the /%s skill; follow these instructions for this request.%s\n\n%s\n</skill-instructions>", s.Name, s.Name, source, content), nil
}
