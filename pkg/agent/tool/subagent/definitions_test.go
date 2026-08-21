package subagent_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/internal/testenv"

	. "github.com/adrianliechti/wingman-agent/pkg/agent/tool/subagent"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func TestParseDefinition(t *testing.T) {
	def, err := ParseDefinition("---\nname: DB-Expert\ndescription: Postgres specialist\naccess: read-only\nmodel: plan\n---\n\nYou are a database expert.\n")
	if err != nil {
		t.Fatal(err)
	}
	if def.Name != "db-expert" {
		t.Fatalf("name = %q, want lowercased db-expert", def.Name)
	}
	if def.Description != "Postgres specialist" || def.Instructions != "You are a database expert." {
		t.Fatalf("def = %+v", def)
	}
	if def.Access != "read-only" || def.Model != "plan" {
		t.Fatalf("access = %q, model = %q", def.Access, def.Model)
	}
}

func TestParseDefinitionDefaultsAndValidation(t *testing.T) {
	def, err := ParseDefinition("---\nname: helper\ndescription: d\nmodel: haiku\n---\nbody")
	if err != nil {
		t.Fatal(err)
	}
	if def.Access != "all" {
		t.Fatalf("access = %q, want all default", def.Access)
	}
	if def.Model != "" {
		t.Fatalf("model = %q, want concrete model id dropped", def.Model)
	}

	cases := []struct{ name, data string }{
		{"missing name", "---\ndescription: d\n---\nbody"},
		{"invalid name", "---\nname: Not A Slug\ndescription: d\n---\nbody"},
		{"missing description", "---\nname: x\n---\nbody"},
		{"missing body", "---\nname: x\ndescription: d\n---\n"},
		{"unknown access", "---\nname: x\ndescription: d\naccess: sudo\n---\nbody"},
		{"no frontmatter", "just a prompt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseDefinition(tc.data); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestDiscoverProjectOverridesPersonal(t *testing.T) {
	home := testenv.UserHome(t)
	wingmanHome := testenv.WingmanHome(t)

	work := t.TempDir()

	writeDefinition(t, filepath.Join(work, ".wingman", "agents", "db.md"),
		"---\nname: db-expert\ndescription: project version\n---\nproject instructions")
	writeDefinition(t, filepath.Join(work, ".claude", "agents", "db2.md"),
		"---\nname: db-expert\ndescription: claude dupe\n---\nignored")
	writeDefinition(t, filepath.Join(home, ".claude", "agents", "personal.md"),
		"---\nname: reviewer-2\ndescription: personal agent\naccess: read-only\n---\npersonal instructions")
	writeDefinition(t, filepath.Join(wingmanHome, "agents", "broken.md"),
		"no frontmatter at all")

	defs := Discover(work)
	if len(defs) != 2 {
		t.Fatalf("defs = %+v, want 2", defs)
	}
	if defs[0].Name != "db-expert" || defs[0].Description != "project version" {
		t.Fatalf("defs[0] = %+v, want project db-expert", defs[0])
	}
	if defs[1].Name != "reviewer-2" || defs[1].Access != "read-only" {
		t.Fatalf("defs[1] = %+v, want personal reviewer-2", defs[1])
	}
}

func TestAgentToolCustomDefinitions(t *testing.T) {
	custom := []Definition{
		{Name: "db-expert", Description: "Postgres specialist", Instructions: "You are a database expert.", Access: "read-only"},
		{Name: "explore", Description: "custom explore", Instructions: "Custom explore prompt.", Access: "all"},
	}

	agentTool := Tools(&agent.Config{}, nil, nil, custom...)[0]

	properties := agentTool.Parameters["properties"].(map[string]any)
	enum := properties["agent_type"].(map[string]any)["enum"].([]string)
	if !contains(enum, "db-expert") || !contains(enum, "explore") {
		t.Fatalf("enum = %v", enum)
	}

	if !strings.Contains(agentTool.Description, "- db-expert: Postgres specialist") {
		t.Fatal("description missing custom agent blurb")
	}
	if !strings.Contains(agentTool.Description, "- explore: custom explore") || strings.Contains(agentTool.Description, "read-only codebase research") {
		t.Fatal("custom override must replace the built-in blurb")
	}

	if got := agentTool.Effect(map[string]any{"agent_type": "db-expert"}); got != tool.EffectReadOnly {
		t.Fatalf("custom read-only type effect = %q", got)
	}
	if got := agentTool.Effect(map[string]any{"agent_type": "explore"}); got != tool.EffectMutates {
		t.Fatalf("overridden explore effect = %q, want mutates for access all", got)
	}
	if got := agentTool.Effect(map[string]any{"agent_type": "security"}); got != tool.EffectReadOnly {
		t.Fatalf("built-in security effect = %q", got)
	}
}

func writeDefinition(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}
