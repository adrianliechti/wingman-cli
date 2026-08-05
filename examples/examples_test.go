package examples_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/subagent"
	"github.com/adrianliechti/wingman-agent/pkg/mcp"
	"github.com/adrianliechti/wingman-agent/pkg/skill"
)

func TestMCPExampleMatchesConfigSchema(t *testing.T) {
	data, err := os.ReadFile("mcp.json")
	if err != nil {
		t.Fatal(err)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var cfg mcp.Config
	if err := decoder.Decode(&cfg); err != nil {
		t.Fatalf("mcp.json does not match mcp.Config: %v", err)
	}

	if len(cfg.Servers) == 0 {
		t.Fatal("mcp.json must define at least one server")
	}
	for name, server := range cfg.Servers {
		if server.Command == "" && server.URL == "" {
			t.Errorf("server %q needs a command or url", name)
		}
	}
}

func TestAgentExamplesParse(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("agents", "*.md"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no agent examples found: %v", err)
	}

	defs := map[string]subagent.Definition{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		def, err := subagent.ParseDefinition(string(data))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		defs[def.Name] = def
	}

	if def := defs["db-expert"]; def.Access != "read-only" {
		t.Fatalf("db-expert = %+v", def)
	}
	if def := defs["release-verifier"]; def.Access != "verify" || def.Model != "utility" {
		t.Fatalf("release-verifier = %+v", def)
	}
}

func TestSkillExamplesParse(t *testing.T) {
	skills, err := skill.LoadBundled(os.DirFS("."), "skills")
	if err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir("skills")
	if err != nil {
		t.Fatal(err)
	}
	// LoadBundled skips unparsable files silently; the count check turns a
	// broken example into a test failure instead.
	if len(skills) != len(entries) {
		t.Fatalf("parsed %d of %d skill examples", len(skills), len(entries))
	}

	byName := map[string]skill.Skill{}
	for _, s := range skills {
		byName[s.Name] = s
	}

	runTests, ok := byName["run-tests"]
	if !ok {
		t.Fatalf("run-tests skill missing, got %v", byName)
	}
	if runTests.Description == "" || runTests.WhenToUse == "" || runTests.Content == "" {
		t.Fatalf("run-tests = %+v", runTests)
	}
	if len(runTests.Arguments) != 1 || runTests.Arguments[0] != "package" {
		t.Fatalf("run-tests arguments = %v", runTests.Arguments)
	}
}
