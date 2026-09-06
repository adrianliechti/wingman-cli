package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/dap"
	"github.com/adrianliechti/wingman-agent/pkg/debugadapter"
	"github.com/adrianliechti/wingman-agent/pkg/devtools"
	"github.com/adrianliechti/wingman-agent/pkg/terminal"
)

const (
	debugRequestLimit       = 2 << 20
	debugControlWait        = 150 * time.Millisecond
	debugPauseWait          = 750 * time.Millisecond
	debugStateRequestBudget = 2 * time.Second
	debugInspectionBudget   = 5 * time.Second
)

var errDebugStateChanged = errors.New("debug session state changed; refresh and try again")

type debugPlanBreakpoint struct {
	FilePath     string `json:"file_path"`
	Line         int    `json:"line"`
	Column       int    `json:"column,omitempty"`
	Condition    string `json:"condition,omitempty"`
	HitCondition string `json:"hit_condition,omitempty"`
	LogMessage   string `json:"log_message,omitempty"`
}

type debugLaunchPlan struct {
	Action              string                `json:"action"`
	Title               string                `json:"title"`
	Adapter             string                `json:"adapter"`
	TerminalAvailable   bool                  `json:"terminal_available"`
	ProjectDir          string                `json:"project_dir"`
	Request             string                `json:"request"`
	IO                  string                `json:"io"`
	Configuration       map[string]any        `json:"configuration"`
	Breakpoints         []debugPlanBreakpoint `json:"breakpoints"`
	FunctionBreakpoints []string              `json:"function_breakpoints"`
	PreLaunch           *dap.ProcessLaunch    `json:"prelaunch,omitempty"`
}

type debugStateResponse struct {
	Available   bool                   `json:"available"`
	Session     *dap.Status            `json:"session,omitempty"`
	Frame       *dap.StackFrame        `json:"frame,omitempty"`
	Breakpoints []dap.SourceBreakpoint `json:"breakpoints"`
	FrameError  string                 `json:"frame_error,omitempty"`
}

type debugSessionResponse struct {
	Session *dap.Status `json:"session,omitempty"`
}

type debugScopeInspection struct {
	Scope     dap.Scope      `json:"scope"`
	Variables []dap.Variable `json:"variables"`
	Error     string         `json:"error,omitempty"`
}

type debugInspectionResponse struct {
	Session *dap.Status      `json:"session,omitempty"`
	Output  string           `json:"output"`
	Threads []dap.Thread     `json:"threads"`
	Frames  []dap.StackFrame `json:"frames"`
	Error   string           `json:"error,omitempty"`
}

func (s *Server) handleDebugTargets(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path    string  `json:"path"`
		Content *string `json:"content,omitempty"`
	}
	if err := decodeDebugJSON(w, r, &body); err != nil {
		return
	}
	rel, ok := s.resolveExistingRegularFile(w, body.Path)
	if !ok {
		return
	}
	var source []byte
	if body.Content != nil {
		source = []byte(*body.Content)
	} else {
		var err error
		source, err = s.workspace.Root.ReadFile(rel)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	targets, err := s.workspace.DebugRegistry().DetectFile(filepath.ToSlash(rel), source)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	writeJSON(w, map[string]any{"targets": nonNilDebugTargets(targets)})
}

type debugPlanRequest struct {
	Action      string `json:"action"`
	TargetID    string `json:"target_id"`
	CurrentPath string `json:"current_path"`
	Install     bool   `json:"install,omitempty"`
}

type debugPlanEvent struct {
	Type     string                `json:"type"`
	Progress *devtools.Progress    `json:"progress,omitempty"`
	Plan     *debugLaunchPlan      `json:"plan,omitempty"`
	Error    string                `json:"error,omitempty"`
	Tools    []devtools.ToolStatus `json:"tools,omitempty"`
	Warning  string                `json:"warning,omitempty"`
}

func (s *Server) handleDebugPlan(w http.ResponseWriter, r *http.Request) {
	var request debugPlanRequest
	if err := decodeDebugJSON(w, r, &request); err != nil {
		return
	}
	request.Action = strings.ToLower(strings.TrimSpace(request.Action))
	if request.Action == "" {
		request.Action = "debug"
	}
	if request.Action != "run" && request.Action != "debug" {
		http.Error(w, "action must be run or debug", http.StatusBadRequest)
		return
	}
	request.TargetID = strings.TrimSpace(request.TargetID)
	request.CurrentPath = strings.TrimSpace(request.CurrentPath)
	if request.TargetID == "" || request.CurrentPath == "" {
		http.Error(w, "target_id and current_path are required", http.StatusBadRequest)
		return
	}
	currentFile, ok := s.resolveExistingRegularFile(w, request.CurrentPath)
	if !ok {
		return
	}
	targets, err := s.detectDebugTargets(currentFile)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var selected *debugadapter.Target
	for i := range targets {
		if targets[i].ID == request.TargetID {
			candidate := targets[i]
			selected = &candidate
			break
		}
	}
	if selected == nil {
		http.Error(w, "debug target is no longer available", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	if s.ctx != nil {
		stop := context.AfterFunc(s.ctx, cancel)
		defer stop()
	}
	streaming := strings.Contains(r.Header.Get("Accept"), "application/x-ndjson")
	encoder := json.NewEncoder(w)
	controller := http.NewResponseController(w)
	emit := func(event debugPlanEvent) {
		if !streaming {
			writeJSON(w, event)
			return
		}
		if err := encoder.Encode(event); err != nil {
			cancel()
			return
		}
		if err := controller.Flush(); err != nil {
			cancel()
		}
	}
	fail := func(err error, status int) {
		if streaming {
			emit(debugPlanEvent{Type: "error", Error: err.Error()})
		} else {
			http.Error(w, err.Error(), status)
		}
	}
	if streaming {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Accel-Buffering", "no")
		if err := controller.Flush(); err != nil {
			return
		}
	}
	statuses, err := s.workspace.DebugToolStatus(ctx, selected.Language)
	if err != nil {
		fail(err, http.StatusServiceUnavailable)
		return
	}
	missing := slices.ContainsFunc(statuses, func(status devtools.ToolStatus) bool { return !status.Installed })
	if missing && !request.Install {
		emit(debugPlanEvent{Type: "installation_required", Tools: statuses})
		return
	}
	if streaming {
		emit(debugPlanEvent{Type: "tools", Tools: statuses})
	}
	if slices.ContainsFunc(statuses, func(status devtools.ToolStatus) bool { return !status.Installed && !status.Installable }) {
		fail(errors.New("managed debugger installation is disabled or unavailable; enable managed tool installation and retry"), http.StatusServiceUnavailable)
		return
	}
	previousTool := ""
	changed, installErr := s.workspace.UpdateDebugTools(ctx, selected.Language, request.Install, func(progress devtools.Progress) {
		if streaming {
			// A completed dependency should show its installed state while
			// setup continues with the next tool (for example Java's host).
			if progress.Phase == devtools.ProgressChecking && previousTool != "" {
				if snapshot, err := s.workspace.DebugToolStatus(ctx, selected.Language); err == nil {
					emit(debugPlanEvent{Type: "tools", Tools: snapshot})
				}
			}
			previousTool = progress.Tool
			emit(debugPlanEvent{Type: "progress", Progress: &progress})
		}
	})
	if changed {
		s.broadcast(Frame{Type: EvtCapabilitiesChanged})
	}
	if ctx.Err() != nil {
		return
	}
	if errors.Is(installErr, dap.ErrActiveSession) || errors.Is(installErr, dap.ErrBusy) {
		fail(installErr, http.StatusConflict)
		return
	}
	statuses, err = s.workspace.DebugToolStatus(ctx, selected.Language)
	if err != nil {
		fail(err, http.StatusServiceUnavailable)
		return
	}
	missing = slices.ContainsFunc(statuses, func(status devtools.ToolStatus) bool { return !status.Installed })
	if missing && !request.Install {
		emit(debugPlanEvent{Type: "installation_required", Tools: statuses})
		return
	}
	if streaming {
		emit(debugPlanEvent{Type: "tools", Tools: statuses})
	}
	if missing {
		if installErr == nil {
			installErr = errors.New("required debugger tools are unavailable; retry installation")
		}
		fail(installErr, http.StatusServiceUnavailable)
		return
	}
	var adapterInfo []dap.AdapterInfo
	err = s.workspace.WithDAPManager(func(manager *dap.Manager) error {
		values, err := manager.Adapters(ctx)
		adapterInfo = values
		return err
	})
	if err != nil {
		fail(errors.Join(err, installErr), http.StatusServiceUnavailable)
		return
	}
	adapterInfo, err = selectTargetDebugAdapter(adapterInfo, *selected)
	if err != nil {
		fail(errors.Join(err, installErr), http.StatusBadRequest)
		return
	}
	projectDir, err := selectTargetDebugProject(s.workspace.RootPath, adapterInfo[0], *selected)
	if err != nil {
		fail(err, http.StatusBadRequest)
		return
	}
	profile, err := s.workspace.DebugRegistry().Plan(adapterInfo[0].Language, debugadapter.Request{
		Action: request.Action, WorkspaceDir: s.workspace.RootPath, ProjectDir: projectDir,
		Target: *selected,
	})
	if err != nil {
		fail(err, http.StatusUnprocessableEntity)
		return
	}
	plan := debugLaunchPlan{
		Action: request.Action, Title: profile.Title,
		Adapter: adapterInfo[0].Name, ProjectDir: profile.ProjectDir, Request: profile.Request,
		TerminalAvailable: profile.SupportsTerminal && adapterInfo[0].TerminalStrategy != dap.TerminalUnsupported && terminal.Supported(),
		IO:                string(profile.IO), Configuration: profile.Configuration,
		FunctionBreakpoints: profile.FunctionBreakpoints,
		PreLaunch:           profile.PreLaunch,
	}
	for _, breakpoint := range profile.Breakpoints {
		plan.Breakpoints = append(plan.Breakpoints, debugPlanBreakpoint{
			FilePath: breakpoint.FilePath, Line: breakpoint.Line, Column: breakpoint.Column,
		})
	}
	if err := s.validateDebugPlan(&plan, adapterInfo); err != nil {
		fail(fmt.Errorf("invalid deterministic debug configuration: %w", err), http.StatusUnprocessableEntity)
		return
	}
	if streaming {
		warning := ""
		if installErr != nil {
			warning = "Could not update debugger tools. Using the installed version.\n" + installErr.Error()
		}
		emit(debugPlanEvent{Type: "plan", Plan: &plan, Warning: warning})
	} else {
		writeJSON(w, plan)
	}
}

func (s *Server) handleDebugStart(w http.ResponseWriter, r *http.Request) {
	var plan debugLaunchPlan
	if err := decodeDebugJSON(w, r, &plan); err != nil {
		return
	}
	var status dap.Status
	err := s.workspace.WithDAPManager(func(manager *dap.Manager) error {
		adapters, err := manager.Adapters(r.Context())
		if err != nil {
			return err
		}
		if err := s.validateDebugPlan(&plan, adapters); err != nil {
			return err
		}
		options := s.debugStartOptions(plan)
		session, err := manager.Start(r.Context(), options)
		if err != nil {
			return err
		}
		status = session.Status()
		return nil
	})
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, dap.ErrActiveSession) || errors.Is(err, dap.ErrBusy) {
			code = http.StatusConflict
		}
		http.Error(w, err.Error(), code)
		return
	}
	writeJSON(w, status)
}

func (s *Server) handleDebugSession(w http.ResponseWriter, _ *http.Request) {
	response := debugSessionResponse{}
	err := s.workspace.WithDAPManager(func(manager *dap.Manager) error {
		if session := manager.ActiveSession(); session != nil {
			status := session.Status()
			response.Session = &status
		}
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, response)
}

func (s *Server) handleDebugState(w http.ResponseWriter, r *http.Request) {
	var sourcePath string
	if requested := r.URL.Query().Get("path"); requested != "" {
		rel, ok := s.resolveExistingRegularFile(w, requested)
		if !ok {
			return
		}
		sourcePath = filepath.Join(s.workspace.RootPath, rel)
	}
	response := debugStateResponse{Breakpoints: []dap.SourceBreakpoint{}}
	err := s.workspace.WithDAPManager(func(manager *dap.Manager) error {
		if sourcePath != "" {
			response.Breakpoints = manager.Breakpoints(sourcePath)
		}
		session := manager.ActiveSession()
		if session == nil {
			adapters, err := manager.Adapters(r.Context())
			if err != nil {
				return err
			}
			response.Available = len(adapters) > 0
			return nil
		}
		response.Available = true
		status := session.Status()
		response.Session = &status
		if status.State != dap.StateStopped {
			return nil
		}
		requestCtx, cancel := context.WithTimeout(r.Context(), debugStateRequestBudget)
		defer cancel()
		threadID := 0
		if status.Stop != nil {
			threadID = status.Stop.ThreadID
		}
		frames, _, frameErr := session.StackTrace(requestCtx, threadID, 0, 1)
		current := session.Status()
		if debugStatusChanged(status, current) {
			// A resume, another stop, or termination can overtake stackTrace.
			// Never pair a frame from that newer state with the stale stop epoch.
			response.Session = &current
			return nil
		}
		if frameErr != nil {
			response.FrameError = frameErr.Error()
			return nil
		}
		if len(frames) > 0 {
			frame := frames[0]
			normalizeDebugFrame(s.workspace.RootPath, &frame)
			response.Frame = &frame
		}
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, response)
}

func (s *Server) handleDebugInspection(w http.ResponseWriter, r *http.Request) {
	response := debugInspectionResponse{
		Output:  "",
		Threads: []dap.Thread{},
		Frames:  []dap.StackFrame{},
	}
	err := s.workspace.WithDAPManager(func(manager *dap.Manager) error {
		session := manager.ActiveSession()
		if session == nil {
			return nil
		}
		status := session.Status()
		response.Session = &status
		response.Output = session.Output()
		if r.URL.Query().Get("details") == "false" {
			return nil
		}
		if status.State != dap.StateStopped {
			return nil
		}

		requestCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		threads, threadsErr := session.Threads(requestCtx)
		current := session.Status()
		if debugStatusChanged(status, current) {
			response.Session = &current
			return nil
		}
		if threadsErr != nil {
			response.Error = threadsErr.Error()
			return nil
		}
		response.Threads = threads
		threadID := 0
		if status.Stop != nil {
			threadID = status.Stop.ThreadID
		}
		frames, _, framesErr := session.StackTrace(requestCtx, threadID, 0, 100)
		current = session.Status()
		if debugStatusChanged(status, current) {
			response.Session = &current
			response.Threads = []dap.Thread{}
			return nil
		}
		if framesErr != nil {
			response.Error = framesErr.Error()
			return nil
		}
		for index := range frames {
			normalizeDebugFrame(s.workspace.RootPath, &frames[index])
		}
		response.Frames = frames
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, response)
}

func (s *Server) handleDebugBreakpoints(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path        string                 `json:"path"`
		Breakpoints []dap.SourceBreakpoint `json:"breakpoints"`
	}
	if err := decodeDebugJSON(w, r, &body); err != nil {
		return
	}
	rel, ok := s.resolveExistingRegularFile(w, body.Path)
	if !ok {
		return
	}
	for _, breakpoint := range body.Breakpoints {
		if breakpoint.Line < 1 || breakpoint.Column < 0 {
			http.Error(w, "breakpoint lines must be positive", http.StatusBadRequest)
			return
		}
	}
	path := filepath.Join(s.workspace.RootPath, rel)
	var resolved []dap.Breakpoint
	err := s.workspace.WithDAPManager(func(manager *dap.Manager) error {
		var err error
		resolved, err = manager.SetBreakpoints(r.Context(), path, body.Breakpoints)
		return err
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{
		"breakpoints": nonNilSourceBreakpoints(body.Breakpoints),
		"resolved":    nonNilDAPBreakpoints(resolved),
	})
}

func (s *Server) handleDebugControl(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Operation string `json:"operation"`
		SessionID string `json:"session_id,omitempty"`
		ThreadID  int    `json:"thread_id,omitempty"`
	}
	if err := decodeDebugJSON(w, r, &body); err != nil {
		return
	}
	body.Operation = strings.TrimSpace(body.Operation)
	waitTimeout := debugControlWaitFor(body.Operation)
	var status *dap.Status
	err := s.workspace.WithDAPManager(func(manager *dap.Manager) error {
		session, err := manager.Session(strings.TrimSpace(body.SessionID))
		if err != nil {
			return err
		}
		before := session.StateEpoch()
		switch body.Operation {
		case "continue":
			err = session.Continue(r.Context(), body.ThreadID)
		case "next":
			err = session.Next(r.Context(), body.ThreadID)
		case "stepIn":
			err = session.StepIn(r.Context(), body.ThreadID)
		case "stepOut":
			err = session.StepOut(r.Context(), body.ThreadID)
		case "stepBack":
			err = session.StepBack(r.Context(), body.ThreadID)
		case "pause":
			err = session.Pause(r.Context(), body.ThreadID)
		case "stop":
			err = manager.Stop(r.Context(), body.SessionID)
			current := session.Status()
			status = &current
		default:
			return fmt.Errorf("unknown debug operation %q", body.Operation)
		}
		if err != nil || body.Operation == "stop" {
			return err
		}
		if waitTimeout > 0 {
			waitCtx, cancel := context.WithTimeout(r.Context(), waitTimeout)
			_, _ = session.WaitForStop(waitCtx, before)
			cancel()
		}
		current := session.Status()
		status = &current
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"session": status})
}

func debugControlWaitFor(operation string) time.Duration {
	if operation == "pause" {
		return debugPauseWait
	}
	return debugControlWait
}

func (s *Server) handleDebugEvaluate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID    string `json:"session_id,omitempty"`
		StateVersion uint64 `json:"state_version,omitempty"`
		Expression   string `json:"expression"`
		FrameID      int    `json:"frame_id,omitempty"`
		Context      string `json:"context,omitempty"`
	}
	if err := decodeDebugJSON(w, r, &body); err != nil {
		return
	}
	if body.Context == "" {
		body.Context = "hover"
	}
	if body.Context != "hover" && body.Context != "watch" && body.Context != "repl" {
		http.Error(w, "invalid evaluate context", http.StatusBadRequest)
		return
	}
	if body.FrameID > 0 && body.StateVersion == 0 {
		http.Error(w, "state_version must be positive when frame_id is set", http.StatusBadRequest)
		return
	}
	requestCtx, cancel := context.WithTimeout(r.Context(), debugInspectionBudget)
	defer cancel()
	var evaluation dap.Evaluation
	err := s.workspace.WithDAPManager(func(manager *dap.Manager) error {
		session, err := manager.Session(strings.TrimSpace(body.SessionID))
		if err != nil {
			return err
		}
		var before dap.Status
		if body.StateVersion > 0 {
			before, err = debugStopAtVersion(session, body.StateVersion)
			if err != nil {
				return err
			}
		}
		evaluation, err = session.EvaluateContext(requestCtx, body.Expression, body.FrameID, body.Context)
		if body.StateVersion > 0 && debugStatusChanged(before, session.Status()) {
			return errDebugStateChanged
		}
		return err
	})
	if err != nil {
		writeDebugInspectionError(w, err)
		return
	}
	writeJSON(w, evaluation)
}

func (s *Server) handleDebugScopes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID    string `json:"session_id,omitempty"`
		StateVersion uint64 `json:"state_version"`
		FrameID      int    `json:"frame_id"`
	}
	if err := decodeDebugJSON(w, r, &body); err != nil {
		return
	}
	if body.FrameID <= 0 {
		http.Error(w, "frame_id must be positive", http.StatusBadRequest)
		return
	}
	if body.StateVersion == 0 {
		http.Error(w, "state_version must be positive", http.StatusBadRequest)
		return
	}
	requestCtx, cancel := context.WithTimeout(r.Context(), debugInspectionBudget)
	defer cancel()
	var scopes []debugScopeInspection
	err := s.workspace.WithDAPManager(func(manager *dap.Manager) error {
		session, err := manager.Session(strings.TrimSpace(body.SessionID))
		if err != nil {
			return err
		}
		before, err := debugStopAtVersion(session, body.StateVersion)
		if err != nil {
			return err
		}
		scopes, err = inspectDebugScopes(requestCtx, session, body.FrameID, before)
		return err
	})
	if err != nil {
		writeDebugInspectionError(w, err)
		return
	}
	if scopes == nil {
		scopes = []debugScopeInspection{}
	}
	writeJSON(w, map[string]any{"scopes": scopes})
}

func inspectDebugScopes(ctx context.Context, session *dap.Session, frameID int, before dap.Status) ([]debugScopeInspection, error) {
	scopes, err := session.Scopes(ctx, frameID)
	if debugStatusChanged(before, session.Status()) {
		return nil, errDebugStateChanged
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return []debugScopeInspection{{Error: err.Error()}}, nil
	}
	result := make([]debugScopeInspection, 0, len(scopes))
	for _, scope := range scopes {
		inspection := debugScopeInspection{Scope: scope, Variables: []dap.Variable{}}
		if scope.VariablesReference > 0 {
			variables, err := session.Variables(ctx, scope.VariablesReference, 0, 200)
			if debugStatusChanged(before, session.Status()) {
				return nil, errDebugStateChanged
			}
			if err != nil {
				inspection.Error = err.Error()
			} else {
				inspection.Variables = variables
			}
		}
		result = append(result, inspection)
	}
	return result, nil
}

func (s *Server) handleDebugVariables(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID    string `json:"session_id,omitempty"`
		StateVersion uint64 `json:"state_version"`
		Reference    int    `json:"variables_reference"`
		Start        int    `json:"start,omitempty"`
		Count        int    `json:"count,omitempty"`
	}
	if err := decodeDebugJSON(w, r, &body); err != nil {
		return
	}
	if body.Reference <= 0 || body.Start < 0 || body.Count < 0 || body.Count > 500 {
		http.Error(w, "invalid variables range", http.StatusBadRequest)
		return
	}
	if body.StateVersion == 0 {
		http.Error(w, "state_version must be positive", http.StatusBadRequest)
		return
	}
	if body.Count == 0 {
		body.Count = 200
	}
	requestCtx, cancel := context.WithTimeout(r.Context(), debugInspectionBudget)
	defer cancel()
	var variables []dap.Variable
	err := s.workspace.WithDAPManager(func(manager *dap.Manager) error {
		session, err := manager.Session(strings.TrimSpace(body.SessionID))
		if err != nil {
			return err
		}
		before, err := debugStopAtVersion(session, body.StateVersion)
		if err != nil {
			return err
		}
		variables, err = session.Variables(requestCtx, body.Reference, body.Start, body.Count)
		if debugStatusChanged(before, session.Status()) {
			return errDebugStateChanged
		}
		return err
	})
	if err != nil {
		writeDebugInspectionError(w, err)
		return
	}
	if variables == nil {
		variables = []dap.Variable{}
	}
	writeJSON(w, map[string]any{"variables": variables})
}

func debugStopAtVersion(session *dap.Session, version uint64) (dap.Status, error) {
	status := session.Status()
	if status.State != dap.StateStopped {
		return dap.Status{}, errors.New("debug session must be stopped to inspect it")
	}
	if status.StateVersion != version {
		return dap.Status{}, errDebugStateChanged
	}
	return status, nil
}

func writeDebugInspectionError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, errDebugStateChanged):
		status = http.StatusConflict
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
	}
	http.Error(w, err.Error(), status)
}

func decodeDebugJSON(w http.ResponseWriter, r *http.Request, target any) error {
	return decodeJSONRequest(w, r, target, debugRequestLimit)
}

func (s *Server) detectDebugTargets(currentFile string) ([]debugadapter.Target, error) {
	source, err := s.workspace.Root.ReadFile(currentFile)
	if err != nil {
		return nil, err
	}
	return s.workspace.DebugRegistry().DetectFile(filepath.ToSlash(currentFile), source)
}

func selectTargetDebugAdapter(values []dap.AdapterInfo, target debugadapter.Target) ([]dap.AdapterInfo, error) {
	matching := make([]dap.AdapterInfo, 0, 1)
	for _, adapter := range values {
		if strings.EqualFold(adapter.Language, target.Language) {
			matching = append(matching, adapter)
		}
	}
	if len(matching) == 0 {
		return nil, fmt.Errorf("no installed debug adapter supports %s", target.Language)
	}
	if len(matching) > 1 {
		return nil, fmt.Errorf("multiple %s debug adapters are available; choose one explicitly", target.Language)
	}
	return matching, nil
}

func selectTargetDebugProject(root string, adapter dap.AdapterInfo, target debugadapter.Target) (string, error) {
	targetDir := filepath.Join(root, filepath.FromSlash(target.Directory))
	best := ""
	for _, project := range adapter.Projects {
		rel, err := filepath.Rel(project, targetDir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if len(project) > len(best) {
			best = project
		}
	}
	if best == "" {
		return "", fmt.Errorf("target %s is not inside a detected %s project", target.Path, adapter.Language)
	}
	rel, err := filepath.Rel(root, best)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("detected debug project is outside the workspace")
	}
	if rel == "." {
		return ".", nil
	}
	return filepath.ToSlash(rel), nil
}

func nonNilDebugTargets(values []debugadapter.Target) []debugadapter.Target {
	if values == nil {
		return []debugadapter.Target{}
	}
	return values
}

func nonNilSourceBreakpoints(values []dap.SourceBreakpoint) []dap.SourceBreakpoint {
	if values == nil {
		return []dap.SourceBreakpoint{}
	}
	return values
}

func nonNilDAPBreakpoints(values []dap.Breakpoint) []dap.Breakpoint {
	if values == nil {
		return []dap.Breakpoint{}
	}
	return values
}

func normalizeDebugFrame(root string, frame *dap.StackFrame) {
	if frame == nil || frame.Source == nil || frame.Source.Path == "" {
		return
	}
	path := frame.Source.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	resolvedRoot, rootErr := filepath.EvalSymlinks(filepath.Clean(root))
	resolvedPath, pathErr := filepath.EvalSymlinks(filepath.Clean(path))
	if rootErr != nil || pathErr != nil {
		frame.Source.Path = ""
		return
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		frame.Source.Path = ""
		return
	}
	frame.Source.Path = filepath.ToSlash(rel)
}

func debugStatusChanged(before, after dap.Status) bool {
	return before.SessionID != after.SessionID || before.StateVersion != after.StateVersion
}

func (s *Server) debugStartOptions(plan debugLaunchPlan) dap.StartOptions {
	options := dap.StartOptions{
		Adapter:       plan.Adapter,
		ProjectDir:    plan.ProjectDir,
		Request:       plan.Request,
		IO:            dap.IOMode(plan.IO),
		Configuration: cloneJSONMap(plan.Configuration),
		PreLaunch:     cloneDebugProcessLaunch(plan.PreLaunch),
		Breakpoints:   make(map[string][]dap.SourceBreakpoint),
	}
	for _, breakpoint := range plan.Breakpoints {
		path := filepath.Join(s.workspace.RootPath, filepath.FromSlash(breakpoint.FilePath))
		options.Breakpoints[path] = append(options.Breakpoints[path], dap.SourceBreakpoint{
			Line: breakpoint.Line, Column: breakpoint.Column, Condition: breakpoint.Condition,
			HitCondition: breakpoint.HitCondition, LogMessage: breakpoint.LogMessage,
		})
	}
	for _, name := range plan.FunctionBreakpoints {
		if name = strings.TrimSpace(name); name != "" {
			options.FunctionBreakpoints = append(options.FunctionBreakpoints, dap.FunctionBreakpoint{Name: name})
		}
	}
	return options
}

func cloneDebugProcessLaunch(value *dap.ProcessLaunch) *dap.ProcessLaunch {
	if value == nil {
		return nil
	}
	clone := *value
	clone.Args = slices.Clone(value.Args)
	return &clone
}

func cloneJSONMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func (s *Server) validateDebugPlan(plan *debugLaunchPlan, adapters []dap.AdapterInfo) error {
	plan.Action = strings.ToLower(strings.TrimSpace(plan.Action))
	if plan.Action == "" {
		plan.Action = "debug"
	}
	if plan.Action != "run" && plan.Action != "debug" {
		return errors.New("action must be run or debug")
	}
	var selectedAdapter *dap.AdapterInfo
	for index := range adapters {
		adapter := &adapters[index]
		if strings.EqualFold(adapter.Name, plan.Adapter) {
			plan.Adapter = adapter.Name
			selectedAdapter = adapter
			break
		}
	}
	if selectedAdapter == nil {
		var matching []int
		for index := range adapters {
			if strings.EqualFold(adapters[index].Language, plan.Adapter) {
				matching = append(matching, index)
			}
		}
		if len(matching) > 1 {
			return fmt.Errorf("multiple %s debug adapters are available; choose one by name", plan.Adapter)
		}
		if len(matching) == 1 {
			selectedAdapter = &adapters[matching[0]]
			plan.Adapter = selectedAdapter.Name
		}
	}
	if selectedAdapter == nil {
		return fmt.Errorf("adapter %q is not available", plan.Adapter)
	}
	plan.IO = strings.TrimSpace(plan.IO)
	if plan.IO == "" {
		plan.IO = string(dap.IOOutput)
	}
	if plan.IO != string(dap.IOOutput) && plan.IO != string(dap.IOTerminal) {
		return fmt.Errorf("I/O mode must be %s or %s", dap.IOOutput, dap.IOTerminal)
	}
	if plan.IO == string(dap.IOTerminal) && (selectedAdapter.TerminalStrategy == dap.TerminalUnsupported || !terminal.Supported()) {
		return fmt.Errorf("adapter %s cannot use a terminal on this host", selectedAdapter.Name)
	}
	plan.Request = strings.ToLower(strings.TrimSpace(plan.Request))
	if plan.Request != "launch" && plan.Request != "attach" {
		return errors.New("request must be launch or attach")
	}
	if plan.Configuration == nil {
		plan.Configuration = map[string]any{}
	}
	if plan.PreLaunch != nil {
		plan.PreLaunch.Title = strings.TrimSpace(plan.PreLaunch.Title)
		plan.PreLaunch.Command = strings.TrimSpace(plan.PreLaunch.Command)
		if plan.PreLaunch.Title == "" || plan.PreLaunch.Command == "" || !filepath.IsAbs(plan.PreLaunch.Command) {
			return errors.New("prelaunch process requires a title and absolute command")
		}
		info, err := os.Stat(plan.PreLaunch.Command)
		if err != nil || info.IsDir() {
			return fmt.Errorf("prelaunch command %q is unavailable", plan.PreLaunch.Command)
		}
		if plan.PreLaunch.ReadyURL != "" {
			readyURL, err := url.Parse(plan.PreLaunch.ReadyURL)
			if err != nil || (readyURL.Scheme != "http" && readyURL.Scheme != "https") || !loopbackDebugHost(readyURL.Hostname()) {
				return errors.New("prelaunch ready_url must be an HTTP URL on this machine")
			}
			plan.PreLaunch.ReadyURL = readyURL.String()
		}
	}
	projectPath, err := dap.ResolveWorkspaceDirectory(s.workspace.RootPath, plan.ProjectDir)
	if err != nil {
		return err
	}
	project, err := filepath.Rel(s.workspace.RootPath, projectPath)
	if err != nil {
		return errors.New("project_dir must be inside the workspace")
	}
	if project == "." {
		plan.ProjectDir = "."
	} else {
		plan.ProjectDir = filepath.ToSlash(project)
	}
	for index := range plan.Breakpoints {
		breakpoint := &plan.Breakpoints[index]
		if breakpoint.Line < 1 || breakpoint.Column < 0 {
			return fmt.Errorf("breakpoint %d has an invalid position", index+1)
		}
		rel, ok := s.workspaceRel(breakpoint.FilePath)
		if !ok {
			return fmt.Errorf("breakpoint %d has an invalid path", index+1)
		}
		info, err := s.workspace.Root.Stat(rel)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("breakpoint path %q does not exist", breakpoint.FilePath)
		}
		breakpoint.FilePath = filepath.ToSlash(rel)
	}
	if _, err := dap.ResolveConfigurationPaths(s.workspace.RootPath, projectPath, selectedAdapter.ConfigurationPaths, plan.Configuration); err != nil {
		return err
	}
	plan.Title = strings.TrimSpace(plan.Title)
	if plan.Title == "" {
		plan.Title = "Debug session"
	}
	if plan.Breakpoints == nil {
		plan.Breakpoints = []debugPlanBreakpoint{}
	}
	if plan.FunctionBreakpoints == nil {
		plan.FunctionBreakpoints = []string{}
	}
	return nil
}

func loopbackDebugHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
