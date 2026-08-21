package server

import (
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
)

func TestGenerationTargetUsesBaseConfigRoleModel(t *testing.T) {
	s := &Server{config: &agent.Config{
		Model: func() string { return "main-model" },
		RoleModel: func(role string) (agent.ModelOption, bool) {
			switch role {
			case "utility":
				return agent.ModelOption{
					ID:      "utility-model",
					Efforts: []string{"none", "low", "high"},
				}, true
			case "":
				return agent.ModelOption{ID: "resolved-main"}, true
			default:
				return agent.ModelOption{}, false
			}
		},
	}}

	model, effort := s.generationTarget("utility")
	if model != "utility-model" || effort != "none" {
		t.Fatalf("utility target = %q/%q", model, effort)
	}
	model, effort = s.generationTarget("")
	if model != "resolved-main" || effort != "" {
		t.Fatalf("main target = %q/%q", model, effort)
	}
}
