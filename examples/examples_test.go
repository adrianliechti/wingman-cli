package examples_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/subagent"
	"github.com/adrianliechti/wingman-agent/pkg/debugadapter"
	"github.com/adrianliechti/wingman-agent/pkg/mcp"
	"github.com/adrianliechti/wingman-agent/pkg/plugin"
	"github.com/adrianliechti/wingman-agent/pkg/skill"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestDebuggerSamplesExposeEverySupportedLanguage(t *testing.T) {
	root, err := filepath.Abs("debug")
	if err != nil {
		t.Fatal(err)
	}
	targets, err := debugadapter.NewRegistry().DetectWorkspace(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	counts := make(map[string]int)
	for _, target := range targets {
		counts[target.Language]++
	}
	want := map[string]int{
		"Go":                    1,
		"Python":                1,
		"Java":                  1,
		"Rust":                  1,
		"C#/.NET":               1,
		"JavaScript/TypeScript": 3,
	}
	if !reflect.DeepEqual(counts, want) {
		t.Fatalf("debug sample targets = %#v, want %#v; all targets = %#v", counts, want, targets)
	}
}

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

func TestNonInteractiveOutputSchemaResolves(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("non-interactive", "project.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	if _, err := schema.Resolve(nil); err != nil {
		t.Fatalf("project.schema.json is invalid: %v", err)
	}
	if schema.Type != "object" || len(schema.Required) == 0 {
		t.Fatalf("project.schema.json must constrain an object: %+v", schema)
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

func TestPluginExamplesLoad(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("plugins", "*"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no plugin examples found: %v", err)
	}

	for _, path := range paths {
		p, notes, err := plugin.Load(path, t.TempDir())
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if len(notes) != 0 {
			t.Fatalf("%s: %v", path, notes)
		}

		if len(p.Skills) == 0 {
			t.Fatalf("%s: no skills discovered", path)
		}
		if len(p.Servers) == 0 {
			t.Fatalf("%s: no MCP servers discovered", path)
		}
		if p.Hooks.RuleCount() == 0 {
			t.Fatalf("%s: no plugin hooks discovered", path)
		}

		for _, sk := range p.Skills {
			if sk.Plugin != p.Name || sk.Qualified() != p.Name+":"+sk.Name {
				t.Fatalf("%s: plugin skill is not qualified: %+v", path, sk)
			}
			if !sk.Portable() {
				t.Fatalf("%s: plugin skill uses non-portable frontmatter: %+v", path, sk)
			}
		}

		if p.Name == "release-tools" {
			assertReleaseToolsPlugin(t, p)
		}
	}
}

func assertReleaseToolsPlugin(t *testing.T, p *plugin.Plugin) {
	t.Helper()

	wantTransports := map[string]string{
		"changelog":           "stdio",
		"release-api":         "streamable-http",
		"legacy-release-feed": "sse",
	}
	for name, transport := range wantTransports {
		server, ok := p.Servers[name]
		if !ok || server.Transport != transport {
			t.Fatalf("release-tools server %q = %+v, want transport %q", name, server, transport)
		}
	}

	stdio := p.Servers["changelog"]
	joinedArgs := strings.Join(stdio.Args, " ")
	if !strings.Contains(joinedArgs, p.Data) || stdio.Dir != p.Data {
		t.Fatalf("PLUGIN_DATA was not expanded in stdio server: %+v", stdio)
	}
	if !strings.Contains(stdio.Env["CHANGELOG_TEMPLATE"], p.Root) {
		t.Fatalf("PLUGIN_ROOT was not expanded in stdio server: %+v", stdio)
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
	if runTests.Description == "" || runTests.Content == "" {
		t.Fatalf("run-tests = %+v", runTests)
	}
	if runTests.Portable() || !reflect.DeepEqual(runTests.Arguments, []string{"package"}) {
		t.Fatalf("run-tests named arguments = %+v", runTests)
	}
	if runTests.InvocationHint() != "[package-or-./...]" {
		t.Fatalf("run-tests hint = %q", runTests.InvocationHint())
	}
	if runTests.License == "" || runTests.Compatibility == "" || len(runTests.Metadata) == 0 || len(runTests.AllowedTools) == 0 {
		t.Fatalf("run-tests optional metadata = %+v", runTests)
	}

	loaded, err := skill.LoadFile(filepath.Join("skills", "run-tests", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}

	projectDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	block, err := (skill.Invocation{Skill: &loaded, Args: "./pkg/skill"}).Instructions(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(block, "${SKILL_DIR}") || strings.Contains(block, "${PROJECT_DIR}") || strings.Contains(block, "$package") || strings.Contains(block, "$ARGUMENTS") {
		t.Fatalf("run-tests substitutions were not rendered: %s", block)
	}
	if !strings.Contains(block, filepath.Join(projectDir, "skills", "run-tests", "scripts", "run.sh")) || !strings.Contains(block, "./pkg/skill") {
		t.Fatalf("run-tests rendered instructions miss paths or arguments: %s", block)
	}

	for _, resource := range []string{
		filepath.Join("skills", "run-tests", "references", "reporting.md"),
		filepath.Join("skills", "run-tests", "scripts", "run.sh"),
	} {
		if info, err := os.Stat(resource); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("skill resource %s is missing or not a regular file: %v", resource, err)
		}
	}
}
