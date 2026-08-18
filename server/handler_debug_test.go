package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

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

func TestDebugTargetsRejectsTrailingJSON(t *testing.T) {
	root := t.TempDir()
	writeDebugTestFile(t, root, "main.go", "package main\n\nfunc main() {}\n")
	app := newDebugTestServer(t, root)
	request := httptest.NewRequest(http.MethodPost, "/api/debug/targets", bytes.NewBufferString(`{"path":"main.go"} {"path":"main.go"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestDetectDebugTargetsPrefersCurrentFile(t *testing.T) {
	root := t.TempDir()
	writeDebugTestFile(t, root, "main.go", "package main\n\nfunc main() {}\n")
	writeDebugTestFile(t, root, "cmd/other/main.go", "package main\n\nfunc main() {}\n")
	app := newDebugTestServer(t, root)
	targets, err := app.detectDebugTargets(context.Background(), "main.go", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Path != "main.go" {
		t.Fatalf("targets = %#v", targets)
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
	if inspection.Session != nil || inspection.Output != "" || inspection.Threads == nil || inspection.Frames == nil {
		t.Fatalf("inspection = %#v", inspection)
	}
}

func TestLiveDebugInspectionReturnsGoVariables(t *testing.T) {
	if os.Getenv("WINGMAN_LIVE_DAP") == "" {
		t.Skip("set WINGMAN_LIVE_DAP=1 to run a real Delve session")
	}
	root := t.TempDir()
	writeDebugTestFile(t, root, "go.mod", "module example.com/debuginspection\n\ngo 1.24\n")
	writeDebugTestFile(t, root, "main.go", `package main

import "fmt"

func work(value int) int {
	return value + 1
}

func main() {
	fmt.Println(work(41))
}
`)
	app := newDebugTestServer(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var session *dap.Session
	if err := app.workspace.WithDAPManager(func(manager *dap.Manager) error {
		adapters, err := manager.Adapters(ctx)
		if err != nil {
			return err
		}
		if len(adapters) == 0 {
			t.Skip("Delve is not installed")
		}
		session, err = manager.Start(ctx, dap.StartOptions{
			Adapter: "delve",
			Configuration: map[string]any{
				"mode":    "debug",
				"program": filepath.Join(root, "main.go"),
			},
			Breakpoints: map[string][]dap.SourceBreakpoint{
				filepath.Join(root, "main.go"): {{Line: 6}},
			},
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	status := session.Status()
	if status.State != dap.StateStopped {
		status, _ = session.WaitForStop(ctx, session.StateEpoch())
	}
	if status.State != dap.StateStopped {
		t.Fatalf("status = %+v\noutput:\n%s", status, session.Output())
	}

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
	if inspection.Session == nil || len(inspection.Frames) == 0 {
		t.Fatalf("inspection did not return stack frames: %#v", inspection)
	}
	scopesBody, err := json.Marshal(map[string]any{
		"session_id": inspection.Session.SessionID,
		"frame_id":   inspection.Frames[0].ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/debug/scopes", bytes.NewReader(scopesBody))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("scopes status = %d, body = %s", response.Code, response.Body.String())
	}
	var result struct {
		Scopes []debugScopeInspection `json:"scopes"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	var variables []dap.Variable
	for _, scope := range result.Scopes {
		variables = append(variables, scope.Variables...)
	}
	if !slices.ContainsFunc(variables, func(variable dap.Variable) bool {
		return variable.Name == "value" && variable.Value == "41"
	}) {
		t.Fatalf("scopes did not return value=41: %#v", result.Scopes)
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
