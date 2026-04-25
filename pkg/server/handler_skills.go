package server

import (
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/skill"
)

// SkillEntry is the wire shape for the slash-command picker.
type SkillEntry struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	WhenToUse   string   `json:"when_to_use,omitempty"`
	Arguments   []string `json:"arguments,omitempty"`
}

// resolveSkill rewrites a "/skillname [args]" message into the skill's
// rendered content with arguments substituted. If text doesn't start with `/`
// or no skill matches, it's returned unchanged.
func (s *Server) resolveSkill(text string) string {
	if !strings.HasPrefix(text, "/") {
		return text
	}

	parts := strings.SplitN(text[1:], " ", 2)
	name := parts[0]
	args := ""
	if len(parts) > 1 {
		args = parts[1]
	}

	sk := skill.FindSkill(name, s.agent.Skills)
	if sk == nil {
		return text
	}

	content, err := sk.GetContent(s.agent.RootPath)
	if err != nil {
		return text
	}

	return sk.ApplyArguments(content, args)
}

func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	skills := s.agent.Skills

	result := make([]SkillEntry, 0, len(skills))
	for _, sk := range skills {
		result = append(result, SkillEntry{
			Name:        sk.Name,
			Description: sk.Description,
			WhenToUse:   sk.WhenToUse,
			Arguments:   sk.Arguments,
		})
	}

	writeJSON(w, result)
}
