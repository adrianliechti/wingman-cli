package agent

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/code"
)

func TestConfirmWithoutUIFailsClosed(t *testing.T) {
	a := &Agent{}
	allowed, err := a.confirm(context.Background(), "run dangerous command?")
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("missing UI approved a confirmation")
	}
}

func TestEffortReturnsIndependentValues(t *testing.T) {
	a := &Agent{}
	_, values := a.Effort("")
	values[0] = "changed"
	_, again := a.Effort("")
	if again[0] != "auto" {
		t.Fatalf("caller mutated shared effort values: %v", again)
	}
}

func TestClosedAgentRejectsNewSession(t *testing.T) {
	a := &Agent{closed: true}
	if _, err := a.NewSession(context.Background()); err == nil {
		t.Fatal("closed agent created a session")
	}
}

func TestSessionFileRootsKeepSystemSkillsReadOnly(t *testing.T) {
	t.Setenv("WINGMAN_SANDBOX", "")
	volumeRoot := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	systemRoot := filepath.Join(volumeRoot, "wingman-system-skills", ".system")
	ws := &code.Workspace{
		RootPath:         t.TempDir(),
		ScratchPath:      t.TempDir(),
		SystemSkillsPath: systemRoot,
	}

	readRoots, writeRoots := sessionFileRoots(ws)
	if !pathCoveredByRoots(systemRoot, readRoots) {
		t.Fatalf("system skills root %q is not readable from %#v", systemRoot, readRoots)
	}
	if pathCoveredByRoots(systemRoot, writeRoots) {
		t.Fatalf("system skills root %q is writable from %#v", systemRoot, writeRoots)
	}
}

func pathCoveredByRoots(path string, roots []string) bool {
	for _, root := range roots {
		if root == "*" {
			return true
		}
		relative, err := filepath.Rel(root, path)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func TestUnattendedApprovesAndResolvesPromptsWithoutUI(t *testing.T) {
	s := &sessionState{}
	s.setMode(modeUnattended)
	a := &Agent{sessions: map[string]*sessionState{"s1": s}}
	ctx := code.WithSessionID(context.Background(), "s1")

	allowed, err := a.confirm(ctx, "edit a file?")
	if err != nil || !allowed {
		t.Fatalf("unattended confirmation = %v, %v", allowed, err)
	}
	res, err := a.elicit(ctx, tool.ElicitRequest{Fields: []tool.ElicitField{{
		Name: "choice", Required: true, Enum: []string{"Recommended", "Alternative"},
	}}})
	if err != nil || res.Action != tool.ElicitAccept || res.Content["choice"] != "Recommended" {
		t.Fatalf("unattended elicitation = %#v, %v", res, err)
	}
	res, err = a.elicit(ctx, tool.ElicitRequest{Fields: []tool.ElicitField{{
		Name: "detail", Required: true, Type: "string",
	}}})
	if err != nil || res.Action != tool.ElicitDecline {
		t.Fatalf("required free-text elicitation = %#v, %v", res, err)
	}
}

func TestModeSwitchReachesToolsWhileCatalogStaysPinned(t *testing.T) {
	s := &sessionState{
		parent: &Agent{workspace: &code.Workspace{}},
		toolSet: tool.NewSet(
			tool.Tool{Name: "elicit"},
			tool.Tool{Name: "read"},
		),
	}
	s.setMode(modeAgent)
	s.turnTools.Store([]tool.Tool{{Name: "mcp_search"}})

	has := func(name string) bool {
		return slices.ContainsFunc(s.tools(), func(t tool.Tool) bool { return t.Name == name })
	}

	if !has("elicit") || !has("mcp_search") {
		t.Fatalf("agent mode tools = %#v", s.tools())
	}

	s.setMode(modeUnattended)
	if has("elicit") {
		t.Fatalf("mode switch did not reach the tool set mid-turn: %#v", s.tools())
	}
	if !has("mcp_search") {
		t.Fatalf("pinned catalog lost mid-turn: %#v", s.tools())
	}

	s.clearCancel(s.cancelGen)
	if has("mcp_search") {
		t.Fatalf("catalog stayed pinned after the turn: %#v", s.tools())
	}
}

func TestUnattendedModeOwnsToolsAndInstructions(t *testing.T) {
	s := &sessionState{
		parent: &Agent{workspace: &code.Workspace{}},
		toolSet: tool.NewSet(
			tool.Tool{Name: "elicit"},
			tool.Tool{Name: "read"},
		),
	}
	s.setMode(modeUnattended)

	tools := s.tools()
	if len(tools) != 1 || tools[0].Name != "read" {
		t.Fatalf("unattended tools = %#v, want read without elicit", tools)
	}
	instructions := BuildInstructions("", s.instructionsData())
	if !strings.Contains(instructions, "Work unattended") || !strings.Contains(instructions, "Do not ask the user") {
		t.Fatalf("unattended instructions missing policy: %q", instructions)
	}

	instructions = BuildInstructions("gpt-5.6-sol", s.instructionsData())
	if !strings.Contains(instructions, "GPT 5.6 Sol") || !strings.Contains(instructions, "gpt-5.6-sol") || !strings.Contains(instructions, "## Autonomy and persistence") || !strings.Contains(instructions, "Work unattended") {
		t.Fatalf("gpt unattended instructions missing variant base or addendum: %q", instructions)
	}
}
