package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/adrianliechti/wingman-agent/internal/testenv"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/adrianliechti/wingman-agent/pkg/dap"
	"github.com/adrianliechti/wingman-agent/pkg/debugadapter"
	"github.com/adrianliechti/wingman-agent/pkg/devtools"
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
	serveTestHTTP(app, response, request)
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

func TestDebugTargetsReturnsNativeCodeLensCandidates(t *testing.T) {
	for _, extension := range []string{".c", ".cpp", ".cc", ".cxx", ".c++", ".C"} {
		t.Run(extension, func(t *testing.T) {
			root := t.TempDir()
			path := "main" + extension
			writeDebugTestFile(t, root, path, "int main() { return 0; }\n")
			app := newDebugTestServer(t, root)
			// Code lenses use the editor buffer, which may differ from disk.
			body, err := json.Marshal(map[string]string{"path": path, "content": "// draft\n\nint main() { return 42; }\n"})
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			serveTestHTTP(app, response, httptest.NewRequest(http.MethodPost, "/api/debug/targets", bytes.NewReader(body)))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			var result struct {
				Targets []debugadapter.Target `json:"targets"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Targets) != 1 || result.Targets[0].Name != "main" || result.Targets[0].Line != 3 || result.Targets[0].Language != "C/C++" {
				t.Fatalf("native targets = %+v", result.Targets)
			}
		})
	}
}

func TestDebugTargetsRejectsTrailingJSON(t *testing.T) {
	root := t.TempDir()
	writeDebugTestFile(t, root, "main.go", "package main\n\nfunc main() {}\n")
	app := newDebugTestServer(t, root)
	request := httptest.NewRequest(http.MethodPost, "/api/debug/targets", bytes.NewBufferString(`{"path":"main.go"} {"path":"main.go"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	serveTestHTTP(app, response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestDebugPlanRequiresCodeLensTarget(t *testing.T) {
	root := t.TempDir()
	app := newDebugTestServer(t, root)
	request := httptest.NewRequest(http.MethodPost, "/api/debug/plan", bytes.NewBufferString(`{"action":"debug"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	serveTestHTTP(app, response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "target_id and current_path are required") {
		t.Fatalf("body = %q", response.Body.String())
	}
}

func TestDebugPlanRequiresConfirmationBeforeInstallingSelectedDebugger(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test installer uses a shell script")
	}
	testenv.UserHome(t)
	testenv.WingmanHome(t)
	t.Setenv("WINGMAN_MANAGED_TOOLS", "on")
	bin := t.TempDir()
	goCommand := filepath.Join(bin, "go")
	if err := os.WriteFile(goCommand, []byte(`#!/bin/sh
if [ "$1" != "install" ] || [ "$2" != "github.com/go-delve/delve/cmd/dlv@latest" ]; then
  exit 1
fi
printf '#!/bin/sh\nexit 0\n' > "$GOBIN/dlv"
/bin/chmod +x "$GOBIN/dlv"
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	root := t.TempDir()
	writeDebugTestFile(t, root, "go.mod", "module example.com/demo\n\ngo 1.25\n")
	writeDebugTestFile(t, root, "main.go", "package main\n\nfunc main() {}\n")
	writeDebugTestFile(t, root, "main.py", "print('hello')\n")
	for _, path := range []string{filepath.Join(bin, "dlv"), filepath.Join(root, "node_modules", ".bin", "dlv")} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	app := newDebugTestServer(t, root)
	if app.debugAvailable(context.Background()) {
		t.Fatal("system or project debugger was treated as managed")
	}

	response := httptest.NewRecorder()
	serveTestHTTP(app, response, httptest.NewRequest(http.MethodPost, "/api/debug/targets", strings.NewReader(`{"path":"main.go"}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("targets status = %d: %s", response.Code, response.Body.String())
	}
	var targets struct {
		Targets []debugadapter.Target `json:"targets"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &targets); err != nil || len(targets.Targets) != 1 {
		t.Fatalf("targets = %+v, error = %v", targets, err)
	}
	if app.workspace.DevTools.Resolve("dlv") != "" {
		t.Fatal("debugger was installed before requesting a launch plan")
	}
	body, err := json.Marshal(debugPlanRequest{Action: "debug", TargetID: targets.Targets[0].ID, CurrentPath: "main.go"})
	if err != nil {
		t.Fatal(err)
	}
	for _, accept := range []string{"application/json", "application/x-ndjson"} {
		request := httptest.NewRequest(http.MethodPost, "/api/debug/plan", bytes.NewReader(body))
		request.Header.Set("Accept", accept)
		response = httptest.NewRecorder()
		serveTestHTTP(app, response, request)
		var result debugPlanEvent
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatalf("%s: %v: %s", accept, err, response.Body.String())
		}
		if result.Type != "installation_required" || len(result.Tools) != 1 || result.Tools[0].Tool != "delve" || result.Tools[0].Installed || !result.Tools[0].Installable {
			t.Fatalf("%s: installation prompt = %+v", accept, result)
		}
		if app.workspace.DevTools.Resolve("dlv") != "" {
			t.Fatal("opening the debug popup installed a debugger without confirmation")
		}
	}
	confirmedBody, err := json.Marshal(debugPlanRequest{Action: "debug", TargetID: targets.Targets[0].ID, CurrentPath: "main.go", Install: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("busy debugger does not install tools", func(t *testing.T) {
		release, err := app.workspace.DAP.BeginPreparation()
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		for _, accept := range []string{"application/json", "application/x-ndjson"} {
			request := httptest.NewRequest(http.MethodPost, "/api/debug/plan", bytes.NewReader(confirmedBody))
			request.Header.Set("Accept", accept)
			response := httptest.NewRecorder()
			serveTestHTTP(app, response, request)
			if !strings.Contains(response.Body.String(), dap.ErrBusy.Error()) {
				t.Fatalf("%s: busy response = %d: %s", accept, response.Code, response.Body.String())
			}
			if accept == "application/json" && response.Code != http.StatusConflict {
				t.Fatalf("busy status = %d, want conflict", response.Code)
			}
			if app.workspace.DevTools.Resolve("dlv") != "" {
				t.Fatal("setup installed tools while debugger was busy")
			}
		}
	})
	request := httptest.NewRequest(http.MethodPost, "/api/debug/plan", bytes.NewReader(confirmedBody))
	request.Header.Set("Accept", "application/x-ndjson")
	response = httptest.NewRecorder()
	serveTestHTTP(app, response, request)
	if response.Code != http.StatusOK || !response.Flushed || response.Header().Get("Content-Type") != "application/x-ndjson" {
		t.Fatalf("plan response = %d, %v: %s", response.Code, response.Header(), response.Body.String())
	}
	decoder := json.NewDecoder(response.Body)
	var phases []devtools.ProgressPhase
	var plan *debugLaunchPlan
	var statuses []devtools.ToolStatus
	for {
		var event debugPlanEvent
		if err := decoder.Decode(&event); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		switch event.Type {
		case "tools":
			statuses = event.Tools
		case "progress":
			if event.Progress == nil || event.Progress.Tool != "delve" {
				t.Fatalf("unexpected tool progress: %+v", event)
			}
			phases = append(phases, event.Progress.Phase)
		case "plan":
			plan = event.Plan
		default:
			t.Fatalf("unexpected event: %+v", event)
		}
	}
	if !slices.Equal(phases, []devtools.ProgressPhase{devtools.ProgressChecking, devtools.ProgressInstalling}) {
		t.Fatalf("installation phases = %v", phases)
	}
	if plan == nil || plan.Adapter != "delve" || plan.Action != "debug" {
		t.Fatalf("plan = %+v", plan)
	}
	if len(statuses) != 1 || !statuses[0].Installed {
		t.Fatalf("installed tool status = %+v", statuses)
	}
	if app.workspace.DevTools.Resolve("dlv") == "" || app.workspace.DevTools.Resolve("debugpy-adapter") != "" {
		t.Fatal("launch preparation did not install only the selected debugger")
	}

	// JSON clients still receive a plan, and the installed debugger is reused.
	if err := os.WriteFile(goCommand, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/debug/plan", bytes.NewReader(body))
	response = httptest.NewRecorder()
	serveTestHTTP(app, response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("reused plan status = %d: %s", response.Code, response.Body.String())
	}
	var reused debugLaunchPlan
	if err := json.Unmarshal(response.Body.Bytes(), &reused); err != nil || reused.Adapter != "delve" {
		t.Fatalf("JSON plan = %+v, error = %v", reused, err)
	}

	// A due update is attempted without another install confirmation. A failed
	// refresh keeps the existing managed debugger and reports the failure.
	statusPath := filepath.Join(app.workspace.DevTools.ToolDir("delve"), ".status")
	if err := os.WriteFile(statusPath, []byte(time.Now().Add(-48*time.Hour).UTC().Format(time.RFC3339Nano)), 0o600); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/debug/plan", bytes.NewReader(body))
	request.Header.Set("Accept", "application/x-ndjson")
	response = httptest.NewRecorder()
	serveTestHTTP(app, response, request)
	decoder = json.NewDecoder(response.Body)
	updating := false
	var warning string
	plan = nil
	for {
		var event debugPlanEvent
		if err := decoder.Decode(&event); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if event.Progress != nil && event.Progress.Phase == devtools.ProgressUpdating {
			updating = true
		}
		if event.Type == "plan" {
			plan, warning = event.Plan, event.Warning
		}
		if event.Type == "error" {
			t.Fatalf("failed refresh blocked the installed debugger: %s", event.Error)
		}
	}
	if !updating || plan == nil || !strings.Contains(warning, "Could not update debugger tools") {
		t.Fatalf("update = %v, plan = %+v, warning = %q", updating, plan, warning)
	}
	if app.workspace.DevTools.Resolve("dlv") == "" {
		t.Fatal("failed refresh removed the managed debugger")
	}
}

func TestDebugPlanFindsToolInstalledAfterCachedDiscovery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test executable uses a shell script")
	}
	testenv.UserHome(t)
	testenv.WingmanHome(t)
	t.Setenv("WINGMAN_MANAGED_TOOLS", "on")
	root := t.TempDir()
	writeDebugTestFile(t, root, "go.mod", "module example.com/demo\n\ngo 1.25\n")
	writeDebugTestFile(t, root, "main.go", "package main\n\nfunc main() {}\n")
	app := newDebugTestServer(t, root)
	if app.debugAvailable(context.Background()) {
		t.Fatal("debugger was available before installation")
	}
	// Simulate another workspace installing a fresh copy while this workspace
	// still has cached discovery results for the missing debugger.
	directory := filepath.Join(app.workspace.DevTools.Root(), "delve")
	if err := os.MkdirAll(filepath.Join(directory, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "bin", "dlv"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, ".status"), []byte(time.Now().UTC().Format(time.RFC3339Nano)), 0o600); err != nil {
		t.Fatal(err)
	}
	targets, err := app.detectDebugTargets("main.go")
	if err != nil || len(targets) != 1 {
		t.Fatalf("targets = %+v, error = %v", targets, err)
	}
	body, err := json.Marshal(debugPlanRequest{Action: "debug", TargetID: targets[0].ID, CurrentPath: "main.go"})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	serveTestHTTP(app, response, httptest.NewRequest(http.MethodPost, "/api/debug/plan", bytes.NewReader(body)))
	var plan debugLaunchPlan
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &plan) != nil || plan.Adapter != "delve" {
		t.Fatalf("plan = %d: %s", response.Code, response.Body.String())
	}
}

type debugPlanRecorder struct {
	*httptest.ResponseRecorder
	afterTools func()
}

func (recorder *debugPlanRecorder) Write(data []byte) (int, error) {
	n, err := recorder.ResponseRecorder.Write(data)
	var event debugPlanEvent
	if recorder.afterTools != nil && json.Unmarshal(data, &event) == nil && event.Type == "tools" {
		callback := recorder.afterTools
		recorder.afterTools = nil
		callback()
	}
	return n, err
}

func TestDebugPlanRechecksHostAfterUpdateLockFailure(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires POSIX file permissions")
	}
	for _, install := range []bool{false, true} {
		t.Run(fmt.Sprintf("install=%t", install), func(t *testing.T) {
			testenv.UserHome(t)
			testenv.WingmanHome(t)
			t.Setenv("WINGMAN_MANAGED_TOOLS", "on")
			root := t.TempDir()
			writeDebugTestFile(t, root, "pom.xml", "<project/>\n")
			writeDebugTestFile(t, root, "Main.java", "public class Main { public static void main(String[] args) {} }\n")
			app := newDebugTestServer(t, root)
			tools := app.workspace.DevTools.Root()
			for _, path := range []string{"jdtls/bin/jdtls", "java-debug/bin/java-debug-adapter", "java-debug/java-debug/com.microsoft.java.debug.plugin-test.jar"} {
				writeDebugTestFile(t, tools, path, "#!/bin/sh\nexit 0\n")
				if err := os.Chmod(filepath.Join(tools, path), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			targets, err := app.detectDebugTargets("Main.java")
			if err != nil || len(targets) != 1 {
				t.Fatalf("targets = %+v, error = %v", targets, err)
			}
			body, err := json.Marshal(debugPlanRequest{Action: "debug", TargetID: targets[0].ID, CurrentPath: "Main.java", Install: install})
			if err != nil {
				t.Fatal(err)
			}
			response := &debugPlanRecorder{ResponseRecorder: httptest.NewRecorder(), afterTools: func() {
				// The host disappears after preflight, and a permissions error
				// prevents the updater from even acquiring its installation lock.
				if err := os.Remove(filepath.Join(tools, "jdtls", "bin", "jdtls")); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(tools, 0o555); err != nil {
					t.Fatal(err)
				}
			}}
			defer os.Chmod(tools, 0o755)
			request := httptest.NewRequest(http.MethodPost, "/api/debug/plan", bytes.NewReader(body))
			request.Header.Set("Accept", "application/x-ndjson")
			serveTestHTTP(app, response, request)
			decoder := json.NewDecoder(response.Body)
			var last debugPlanEvent
			for decoder.More() {
				if err := decoder.Decode(&last); err != nil {
					t.Fatal(err)
				}
				if last.Type == "plan" {
					t.Fatal("offered a Java launch plan without its language server host")
				}
			}
			want := "installation_required"
			if install {
				want = "error"
			}
			if response.afterTools != nil || last.Type != want {
				t.Fatalf("last event = %+v, want %s after host disappeared", last, want)
			}
		})
	}
}

func TestDetectDebugTargetsUsesCurrentFile(t *testing.T) {
	root := t.TempDir()
	writeDebugTestFile(t, root, "main.go", "package main\n\nfunc main() {}\n")
	writeDebugTestFile(t, root, "cmd/other/main.go", "package main\n\nfunc main() {}\n")
	app := newDebugTestServer(t, root)
	targets, err := app.detectDebugTargets("main.go")
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
	serveTestHTTP(app, response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("set status = %d, body = %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/debug/state?path=main.go", nil)
	response = httptest.NewRecorder()
	serveTestHTTP(app, response, request)
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
	serveTestHTTP(app, response, request)
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

func TestDebugSessionWithoutActiveSession(t *testing.T) {
	root := t.TempDir()
	app := newDebugTestServer(t, root)
	request := httptest.NewRequest(http.MethodGet, "/api/debug/session", nil)
	response := httptest.NewRecorder()
	serveTestHTTP(app, response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var result debugSessionResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Session != nil {
		t.Fatalf("session = %#v", result.Session)
	}
}

func TestDebugFrameInspectionRequiresStateVersion(t *testing.T) {
	root := t.TempDir()
	app := newDebugTestServer(t, root)
	tests := []struct {
		path string
		body string
	}{
		{path: "/api/debug/evaluate", body: `{"expression":"value","frame_id":1}`},
		{path: "/api/debug/scopes", body: `{"frame_id":1}`},
		{path: "/api/debug/variables", body: `{"variables_reference":1}`},
	}
	for _, test := range tests {
		request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		serveTestHTTP(app, response, request)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "state_version") {
			t.Fatalf("%s: status = %d, body = %q", test.path, response.Code, response.Body.String())
		}
	}
}

func TestDebugStatusChangedUsesSessionEpoch(t *testing.T) {
	base := dap.Status{SessionID: "session", StateVersion: 7, State: dap.StateStopped}
	if debugStatusChanged(base, base) {
		t.Fatal("identical debugger status was reported as changed")
	}
	newer := base
	newer.StateVersion++
	newer.State = dap.StateRunning
	if !debugStatusChanged(base, newer) {
		t.Fatal("new debugger state epoch was not detected")
	}
	replacement := base
	replacement.SessionID = "replacement"
	if !debugStatusChanged(base, replacement) {
		t.Fatal("replacement debugger session was not detected")
	}
}

func TestDebugInspectionErrorUsesConflictAndTimeoutStatuses(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{err: errDebugStateChanged, want: http.StatusConflict},
		{err: context.DeadlineExceeded, want: http.StatusGatewayTimeout},
		{err: errors.New("invalid request"), want: http.StatusBadRequest},
	}
	for _, test := range tests {
		response := httptest.NewRecorder()
		writeDebugInspectionError(response, test.err)
		if response.Code != test.want {
			t.Fatalf("error %v: status = %d, want %d", test.err, response.Code, test.want)
		}
	}
}

func TestNormalizeDebugFrameResolvesWorkspaceSymlinks(t *testing.T) {
	realRoot := t.TempDir()
	writeDebugTestFile(t, realRoot, "main.go", "package main\n")
	linkRoot := filepath.Join(t.TempDir(), "workspace")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Skipf("workspace symlinks are unavailable: %v", err)
	}

	frame := dap.StackFrame{Source: &dap.Source{Path: filepath.Join(realRoot, "main.go")}}
	normalizeDebugFrame(linkRoot, &frame)
	if frame.Source.Path != "main.go" {
		t.Fatalf("normalized path = %q, want main.go", frame.Source.Path)
	}

	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("package outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(realRoot, "linked.go")); err != nil {
		t.Skipf("source symlinks are unavailable: %v", err)
	}
	frame.Source.Path = filepath.Join(linkRoot, "linked.go")
	normalizeDebugFrame(linkRoot, &frame)
	if frame.Source.Path != "" {
		t.Fatalf("outside symlink normalized to %q", frame.Source.Path)
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
	serveTestHTTP(app, response, request)
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
		"session_id":    inspection.Session.SessionID,
		"state_version": inspection.Session.StateVersion,
		"frame_id":      inspection.Frames[0].ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/debug/scopes", bytes.NewReader(scopesBody))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	serveTestHTTP(app, response, request)
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

func TestValidateDebugPlanRejectsAmbiguousLanguage(t *testing.T) {
	root := t.TempDir()
	app := newDebugTestServer(t, root)
	plan := debugLaunchPlan{
		Action: "debug", Adapter: "Test", ProjectDir: ".", Request: "launch",
	}
	adapters := []dap.AdapterInfo{
		{Name: "first", Language: "Test", Projects: []string{root}},
		{Name: "second", Language: "Test", Projects: []string{root}},
	}
	if err := app.validateDebugPlan(&plan, adapters); err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("validation error = %v, want ambiguity", err)
	}

	plan.Adapter = "second"
	if err := app.validateDebugPlan(&plan, adapters); err != nil {
		t.Fatalf("named adapter: %v", err)
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

func TestDebugControlWaitForUsesServerPolicy(t *testing.T) {
	if got := debugControlWaitFor("pause"); got != 750*time.Millisecond {
		t.Fatalf("pause wait = %v", got)
	}
	if got := debugControlWaitFor("next"); got != 150*time.Millisecond {
		t.Fatalf("step wait = %v", got)
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
