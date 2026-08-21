package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/internal/testenv"
	"github.com/adrianliechti/wingman-agent/pkg/code"
)

func TestHandleSkillsRefreshesGeneratedAgentSkills(t *testing.T) {
	testenv.UserHome(t)
	testenv.WingmanHome(t)

	workDir := t.TempDir()
	workspace, err := code.NewWorkspace(workDir)
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.Close()

	skillDir := filepath.Join(workDir, ".agents", "skills", "speckit-specify")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: speckit-specify
description: Create a feature specification
argument-hint: <feature description>
---
Specify $ARGUMENTS.
`), 0644); err != nil {
		t.Fatal(err)
	}

	server := &Server{workspace: workspace}
	response := httptest.NewRecorder()
	server.handleSkills(response, httptest.NewRequest("GET", "/api/skills", nil))
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}

	var entries []SkillEntry
	if err := json.Unmarshal(response.Body.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry.Name == "speckit-specify" &&
			entry.Description == "Create a feature specification" &&
			entry.InputHint == "<feature description>" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("generated skill missing from response: %#v", entries)
	}
}

func TestSkillBlocksRefreshesBeforeDirectInvocation(t *testing.T) {
	testenv.UserHome(t)
	testenv.WingmanHome(t)

	workDir := t.TempDir()
	workspace, err := code.NewWorkspace(workDir)
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.Close()

	skillDir := filepath.Join(workDir, ".agents", "skills", "speckit-plan")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: speckit-plan
description: Create an implementation plan
---
Plan $ARGUMENTS in small phases.
`), 0644); err != nil {
		t.Fatal(err)
	}

	server := &Server{workspace: workspace}
	blocks := server.skillBlocks("$speckit-plan authentication")
	if len(blocks) != 1 || !strings.Contains(blocks[0], "Plan authentication in small phases.") {
		t.Fatalf("skill blocks = %#v", blocks)
	}
}
