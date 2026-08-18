package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/adrianliechti/wingman-agent/pkg/dap"
	"github.com/adrianliechti/wingman-agent/pkg/debugadapter"
)

func TestDebugTargetsReturnsGoCodeLensCandidates(t *testing.T) {
	root := t.TempDir()
	writeDebugTestFile(t, root, "go.mod", "module example.com/demo\n\ngo 1.25\n")
	writeDebugTestFile(t, root, "main.go", "package main\n\nfunc main() {}\n")

	app := newDebugTestServer(t, root)
	body := bytes.NewBufferString(`{"path":"main.go","content":"package main\n\nfunc main() {}\n"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/debug/targets", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var result struct {
		Targets []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
			Line int    `json:"line"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Targets) != 1 || result.Targets[0].Name != "main" || result.Targets[0].Kind != "main" || result.Targets[0].Line != 3 {
		t.Fatalf("targets = %#v", result.Targets)
	}
}

func TestDebugBreakpointsPersistWithoutActiveSession(t *testing.T) {
	root := t.TempDir()
	writeDebugTestFile(t, root, "main.go", "package main\n\nfunc main() {}\n")
	app := newDebugTestServer(t, root)

	request := httptest.NewRequest(http.MethodPut, "/api/debug/breakpoints", bytes.NewBufferString(`{"path":"main.go","breakpoints":[{"line":3}]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("set status = %d, body = %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/debug/state?path=main.go", nil)
	response = httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("state status = %d, body = %s", response.Code, response.Body.String())
	}
	var state debugStateResponse
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Breakpoints) != 1 || state.Breakpoints[0].Line != 3 {
		t.Fatalf("breakpoints = %#v", state.Breakpoints)
	}
}

func TestDebugInspectionWithoutActiveSession(t *testing.T) {
	root := t.TempDir()
	app := newDebugTestServer(t, root)
	request := httptest.NewRequest(http.MethodGet, "/api/debug/inspection", nil)
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var inspection debugInspectionResponse
	if err := json.Unmarshal(response.Body.Bytes(), &inspection); err != nil {
		t.Fatal(err)
	}
	if inspection.Session != nil || inspection.Output != "" || inspection.Threads == nil || inspection.Frames == nil || inspection.Scopes == nil {
		t.Fatalf("inspection = %#v", inspection)
	}
}

func TestValidateDebugPlanConstrainsWorkspacePaths(t *testing.T) {
	root := t.TempDir()
	writeDebugTestFile(t, root, "main.go", "package main\nfunc main() {}\n")
	app := newDebugTestServer(t, root)
	adapters := []dap.AdapterInfo{{
		Name: "delve", Language: "Go", Projects: []string{root},
		ConfigurationPaths: []dap.ConfigurationPath{{Key: "program"}},
	}}

	plan := debugLaunchPlan{
		Action: "debug", Adapter: "delve", ProjectDir: ".", Request: "launch",
		Configuration: map[string]any{"mode": "debug", "program": "main.go"},
	}
	if err := app.validateDebugPlan(&plan, adapters); err != nil {
		t.Fatalf("valid plan: %v", err)
	}

	plan.Configuration["program"] = "../outside"
	if err := app.validateDebugPlan(&plan, adapters); err == nil {
		t.Fatal("outside program path was accepted")
	}
}

func TestSelectDeterministicDebugTargetUsesCurrentFileAndInstalledLanguage(t *testing.T) {
	targets := []debugadapter.Target{
		{ID: "go:main", Language: "Go", Path: "cmd/main.go"},
		{ID: "python:main", Language: "Python", Path: "tools/main.py"},
	}
	selected, err := selectDeterministicDebugTarget(targets, "tools/main.py", []dap.AdapterInfo{{Language: "Python"}})
	if err != nil {
		t.Fatal(err)
	}
	if selected.ID != "python:main" {
		t.Fatalf("selected = %#v", selected)
	}
}

func TestSelectTargetDebugProjectPrefersNearestProject(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "services", "api")
	project, err := selectTargetDebugProject(root, dap.AdapterInfo{
		Language: "Go", Projects: []string{root, nested},
	}, debugadapter.Target{Path: "services/api/main.go", Directory: "services/api"})
	if err != nil {
		t.Fatal(err)
	}
	if project != "services/api" {
		t.Fatalf("project = %q", project)
	}
}

func writeDebugTestFile(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newDebugTestServer(t *testing.T, root string) *Server {
	t.Helper()
	workspace, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	workspace.WarmUp()
	t.Cleanup(workspace.Close)
	server := &Server{workspace: workspace, ctx: context.Background()}
	router := chi.NewRouter()
	server.registerRoutes(router)
	server.mux = router
	server.handler = router
	return server
}
