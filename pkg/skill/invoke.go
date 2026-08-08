package skill

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

type Invocation struct {
	Skill *Skill
	Args  string
}

// A qualified "<plugin>:<skill>" mention is tried first; the bare alternative
// deliberately excludes dots so a sentence-final "/deploy." still resolves.
var inlinePattern = regexp.MustCompile(`(^|\s)(?:/|\$)([A-Za-z0-9][A-Za-z0-9_-]*(?:\.[A-Za-z0-9_-]+)*:[A-Za-z0-9][A-Za-z0-9_-]*|[A-Za-z0-9][A-Za-z0-9_-]*)`)

// ParseCommand splits a leading "/name args" or Codex "$name args"
// invocation; args keep their
// original spacing and may span lines.
func ParseCommand(text string) (string, string, bool) {
	if !strings.HasPrefix(text, "/") && !strings.HasPrefix(text, "$") {
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
	if invocations, ok := stackedInvocations(text, skills); ok {
		return invocations
	}
	if name, args, ok := ParseCommand(text); ok {
		if s := FindSkill(name, skills); userCanInvoke(s) {
			return []Invocation{{Skill: s, Args: args}}
		}
	}

	var invs []Invocation
	seen := make(map[string]bool)

	for _, m := range inlinePattern.FindAllStringSubmatch(text, -1) {
		s := FindSkill(m[2], skills)
		if !userCanInvoke(s) {
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

func stackedInvocations(text string, skills []Skill) ([]Invocation, bool) {
	if !strings.HasPrefix(text, "/") {
		return nil, false
	}
	rest := text
	var invocations []Invocation
	for len(invocations) < 6 && strings.HasPrefix(rest, "/") {
		end := strings.IndexFunc(rest, unicode.IsSpace)
		if end < 0 {
			end = len(rest)
		}
		name := rest[1:end]
		found := FindSkill(name, skills)
		if !userCanInvoke(found) {
			break
		}
		invocations = append(invocations, Invocation{Skill: found})
		rest = strings.TrimLeftFunc(rest[end:], unicode.IsSpace)
		if found.Context == "fork" {
			break
		}
	}
	if len(invocations) == 0 {
		return nil, false
	}
	for i := range invocations {
		invocations[i].Args = rest
	}
	return invocations, true
}

func userCanInvoke(skill *Skill) bool {
	return skill != nil && (skill.UserInvocable == nil || *skill.UserInvocable)
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
	content = s.applyArguments(content, inv.Args, skillDir, workDir)

	source := ""
	if skillDir != "" {
		source = fmt.Sprintf("\nSkill directory: %s. Resolve relative resources from this directory.", skillDir)
	}

	name := s.Qualified()

	return fmt.Sprintf("<skill-instructions skill=%q>\nThe user invoked the /%s skill; follow these instructions for this request.%s\n\n%s\n</skill-instructions>", name, name, source, content), nil
}
