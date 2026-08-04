package server

import (
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/adrianliechti/wingman-agent/pkg/skill"
)

type SkillEntry struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	WhenToUse   string   `json:"when_to_use,omitempty"`
	Arguments   []string `json:"arguments,omitempty"`
	InputHint   string   `json:"input_hint,omitempty"`
}

// skillBlocks expands the skills text invokes — a leading "/name args"
// command or inline /name mentions — into hidden instruction blocks.
func (s *Server) skillBlocks(text string) []string {
	var blocks []string
	for _, inv := range skill.Invocations(text, s.workspace.Skills) {
		block, err := inv.Instructions(s.workspace.RootPath)
		if err != nil {
			continue
		}
		blocks = append(blocks, block)
	}
	return blocks
}

func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	skills := s.workspace.Skills
	result := make([]SkillEntry, 0, len(skills))
	for _, sk := range skills {
		result = append(result, SkillEntry{
			Name:        sk.Name,
			Description: sk.Description,
			WhenToUse:   sk.WhenToUse,
			Arguments:   sk.Arguments,
		})
	}
	if active := s.activeAgent(); active != nil {
		if provider, ok := active.(code.CommandProvider); ok {
			seen := make(map[string]bool, len(result))
			for _, item := range result {
				seen[item.Name] = true
			}
			for _, command := range provider.Commands(r.URL.Query().Get("session")) {
				name := strings.TrimPrefix(command.Name, "/")
				if name == "" || seen[name] {
					continue
				}
				entry := SkillEntry{Name: name, Description: command.Description, InputHint: command.InputHint}
				result = append(result, entry)
				seen[name] = true
			}
		}
	}

	writeJSON(w, result)
}
