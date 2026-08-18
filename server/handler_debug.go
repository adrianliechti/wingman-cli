package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/dap"
	"github.com/adrianliechti/wingman-agent/pkg/debugtarget"
	"github.com/adrianliechti/wingman-agent/pkg/terminal"
)

const (
	debugRequestLimit       = 2 << 20
	debugIntentLimit        = 4_000
	debugManifestLimit      = 800
	debugEvidenceLimit      = 120 << 10
	debugControlMaxWait     = 30 * time.Second
	debugStateRequestBudget = 2 * time.Second
)

var errActiveDebugSession = errors.New("a debug session is already active")

type debugAdapter struct {
	Name               string   `json:"name"`
	Language           string   `json:"language"`
	Projects           []string `json:"projects"`
	ConfigurationHint  string   `json:"configuration_hint,omitempty"`
	IntegratedTerminal bool     `json:"integrated_terminal"`
}

type debugDiscoveryResponse struct {
	Adapters []debugAdapter       `json:"adapters"`
	Targets  []debugtarget.Target `json:"targets"`
	Session  *dap.Status          `json:"session,omitempty"`
}

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
	Summary             string                `json:"summary"`
	Adapter             string                `json:"adapter"`
	ProjectDir          string                `json:"project_dir"`
	Request             string                `json:"request"`
	Console             string                `json:"console"`
	Configuration       map[string]any        `json:"configuration"`
	Breakpoints         []debugPlanBreakpoint `json:"breakpoints"`
	FunctionBreakpoints []string              `json:"function_breakpoints"`
}

type debugStateResponse struct {
	Available   bool                   `json:"available"`
	Session     *dap.Status            `json:"session,omitempty"`
	Frame       *dap.StackFrame        `json:"frame,omitempty"`
	Breakpoints []dap.SourceBreakpoint `json:"breakpoints"`
	FrameError  string                 `json:"frame_error,omitempty"`
}

type debugScopeInspection struct {
	Scope     dap.Scope      `json:"scope"`
	Variables []dap.Variable `json:"variables"`
	Error     string         `json:"error,omitempty"`
}

type debugInspectionResponse struct {
	Session *dap.Status            `json:"session,omitempty"`
	Output  string                 `json:"output"`
	Threads []dap.Thread           `json:"threads"`
	Frames  []dap.StackFrame       `json:"frames"`
	Scopes  []debugScopeInspection `json:"scopes"`
	Error   string                 `json:"error,omitempty"`
}

func (s *Server) handleDebugDiscovery(w http.ResponseWriter, r *http.Request) {
	var adapterInfo []dap.AdapterInfo
	var session *dap.Status
	err := s.workspace.WithDAPManager(func(manager *dap.Manager) error {
		values, err := manager.Adapters(r.Context())
		if err != nil {
			return err
		}
		adapterInfo = values
		if active := manager.ActiveSession(); active != nil {
			status := active.Status()
			session = &status
		}
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	targets, err := debugtarget.NewRegistry().DetectWorkspace(r.Context(), s.workspace.RootPath)
	if err != nil && r.Context().Err() != nil {
		http.Error(w, r.Context().Err().Error(), http.StatusRequestTimeout)
		return
	}
	writeJSON(w, debugDiscoveryResponse{
		Adapters: publicDebugAdapters(s.workspace.RootPath, adapterInfo),
		Targets:  nonNilDebugTargets(targets),
		Session:  session,
	})
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
	targets, err := debugtarget.NewRegistry().DetectFile(filepath.ToSlash(rel), source)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	writeJSON(w, map[string]any{"targets": nonNilDebugTargets(targets)})
}

type debugPlanRequest struct {
	Action      string `json:"action"`
	Adapter     string `json:"adapter,omitempty"`
	TargetID    string `json:"target_id,omitempty"`
	Intent      string `json:"intent,omitempty"`
	CurrentPath string `json:"current_path,omitempty"`
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
	request.Intent = strings.TrimSpace(request.Intent)
	if len(request.Intent) > debugIntentLimit {
		http.Error(w, "intent is too long", http.StatusBadRequest)
		return
	}

	var adapterInfo []dap.AdapterInfo
	err := s.workspace.WithDAPManager(func(manager *dap.Manager) error {
		values, err := manager.Adapters(r.Context())
		adapterInfo = values
		return err
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	adapterInfo, err = selectDebugAdapters(adapterInfo, request.Adapter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(adapterInfo) == 0 {
		http.Error(w, "no debug adapter detected in this workspace", http.StatusNotFound)
		return
	}

	targets, err := debugtarget.NewRegistry().DetectWorkspace(r.Context(), s.workspace.RootPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var selected *debugtarget.Target
	if request.TargetID != "" {
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
	}
	if request.CurrentPath != "" {
		if _, ok := s.resolveExistingRegularFile(w, request.CurrentPath); !ok {
			return
		}
	}

	evidence, err := s.debugPlanningEvidence(r.Context(), targets, selected, request.CurrentPath, adapterInfo)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	plan, err := s.generateDebugPlan(r.Context(), request, selected, adapterInfo, evidence)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if validationErr := s.validateDebugPlan(&plan, adapterInfo); validationErr != nil {
		correctedEvidence := evidence + "\nCorrection required: the previous proposal was rejected because " + validationErr.Error() + ". Produce a corrected plan.\n"
		plan, err = s.generateDebugPlan(r.Context(), request, selected, adapterInfo, correctedEvidence)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if err := s.validateDebugPlan(&plan, adapterInfo); err != nil {
			http.Error(w, "AI proposed an invalid debug configuration: "+err.Error(), http.StatusUnprocessableEntity)
			return
		}
	}
	writeJSON(w, plan)
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
		if active := manager.ActiveSession(); active != nil {
			state := active.Status().State
			if state != dap.StateTerminated {
				return errActiveDebugSession
			}
		}
		options, err := s.debugStartOptions(plan)
		if err != nil {
			return err
		}
		session, err := manager.Start(r.Context(), options)
		if err != nil {
			return err
		}
		status = session.Status()
		return nil
	})
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, errActiveDebugSession) {
			code = http.StatusConflict
		}
		http.Error(w, err.Error(), code)
		return
	}
	writeJSON(w, status)
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
		adapters, err := manager.Adapters(r.Context())
		if err != nil {
			return err
		}
		response.Available = len(adapters) > 0
		if sourcePath != "" {
			response.Breakpoints = manager.Breakpoints(sourcePath)
		}
		session := manager.ActiveSession()
		if session == nil {
			return nil
		}
		status := session.Status()
		response.Session = &status
		if status.State != dap.StateStopped {
			return nil
		}
		requestCtx, cancel := context.WithTimeout(r.Context(), debugStateRequestBudget)
		defer cancel()
		frames, _, err := session.StackTrace(requestCtx, 0, 0, 1)
		if err != nil {
			response.FrameError = err.Error()
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
		Scopes:  []debugScopeInspection{},
	}
	err := s.workspace.WithDAPManager(func(manager *dap.Manager) error {
		session := manager.ActiveSession()
		if session == nil {
			return nil
		}
		status := session.Status()
		response.Session = &status
		response.Output = session.Output()
		if status.State != dap.StateStopped {
			return nil
		}

		requestCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		threads, err := session.Threads(requestCtx)
		if err != nil {
			response.Error = err.Error()
			return nil
		}
		response.Threads = threads
		threadID := 0
		if status.Stop != nil {
			threadID = status.Stop.ThreadID
		}
		frames, _, err := session.StackTrace(requestCtx, threadID, 0, 100)
		if err != nil {
			response.Error = err.Error()
			return nil
		}
		for index := range frames {
			normalizeDebugFrame(s.workspace.RootPath, &frames[index])
		}
		response.Frames = frames
		if len(frames) > 0 {
			response.Scopes = inspectDebugScopes(requestCtx, session, frames[0].ID)
		}
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
		Operation     string `json:"operation"`
		SessionID     string `json:"session_id,omitempty"`
		ThreadID      int    `json:"thread_id,omitempty"`
		WaitTimeoutMS int    `json:"wait_timeout_ms,omitempty"`
	}
	if err := decodeDebugJSON(w, r, &body); err != nil {
		return
	}
	body.Operation = strings.TrimSpace(body.Operation)
	if body.WaitTimeoutMS < 0 || time.Duration(body.WaitTimeoutMS)*time.Millisecond > debugControlMaxWait {
		http.Error(w, "wait_timeout_ms is out of range", http.StatusBadRequest)
		return
	}
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
			current := session.Status()
			err = manager.Stop(r.Context(), body.SessionID)
			current.State = dap.StateTerminated
			status = &current
		default:
			return fmt.Errorf("unknown debug operation %q", body.Operation)
		}
		if err != nil || body.Operation == "stop" {
			return err
		}
		if body.WaitTimeoutMS > 0 {
			waitCtx, cancel := context.WithTimeout(r.Context(), time.Duration(body.WaitTimeoutMS)*time.Millisecond)
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

func (s *Server) handleDebugEvaluate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID  string `json:"session_id,omitempty"`
		Expression string `json:"expression"`
		FrameID    int    `json:"frame_id,omitempty"`
		Context    string `json:"context,omitempty"`
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
	var evaluation dap.Evaluation
	err := s.workspace.WithDAPManager(func(manager *dap.Manager) error {
		session, err := manager.Session(strings.TrimSpace(body.SessionID))
		if err != nil {
			return err
		}
		evaluation, err = session.EvaluateContext(r.Context(), body.Expression, body.FrameID, body.Context)
		return err
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, evaluation)
}

func (s *Server) handleDebugScopes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string `json:"session_id,omitempty"`
		FrameID   int    `json:"frame_id"`
	}
	if err := decodeDebugJSON(w, r, &body); err != nil {
		return
	}
	if body.FrameID <= 0 {
		http.Error(w, "frame_id must be positive", http.StatusBadRequest)
		return
	}
	var scopes []debugScopeInspection
	err := s.workspace.WithDAPManager(func(manager *dap.Manager) error {
		session, err := manager.Session(strings.TrimSpace(body.SessionID))
		if err != nil {
			return err
		}
		if session.Status().State != dap.StateStopped {
			return errors.New("debug session must be stopped to inspect scopes")
		}
		scopes = inspectDebugScopes(r.Context(), session, body.FrameID)
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if scopes == nil {
		scopes = []debugScopeInspection{}
	}
	writeJSON(w, map[string]any{"scopes": scopes})
}

func inspectDebugScopes(ctx context.Context, session *dap.Session, frameID int) []debugScopeInspection {
	scopes, err := session.Scopes(ctx, frameID)
	if err != nil {
		return []debugScopeInspection{{Error: err.Error()}}
	}
	result := make([]debugScopeInspection, 0, len(scopes))
	for _, scope := range scopes {
		inspection := debugScopeInspection{Scope: scope, Variables: []dap.Variable{}}
		if scope.VariablesReference > 0 {
			variables, err := session.Variables(ctx, scope.VariablesReference, 0, 200)
			if err != nil {
				inspection.Error = err.Error()
			} else {
				inspection.Variables = variables
			}
		}
		result = append(result, inspection)
	}
	return result
}

func (s *Server) handleDebugVariables(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string `json:"session_id,omitempty"`
		Reference int    `json:"variables_reference"`
		Start     int    `json:"start,omitempty"`
		Count     int    `json:"count,omitempty"`
	}
	if err := decodeDebugJSON(w, r, &body); err != nil {
		return
	}
	if body.Reference <= 0 || body.Start < 0 || body.Count < 0 || body.Count > 500 {
		http.Error(w, "invalid variables range", http.StatusBadRequest)
		return
	}
	if body.Count == 0 {
		body.Count = 200
	}
	var variables []dap.Variable
	err := s.workspace.WithDAPManager(func(manager *dap.Manager) error {
		session, err := manager.Session(strings.TrimSpace(body.SessionID))
		if err != nil {
			return err
		}
		if session.Status().State != dap.StateStopped {
			return errors.New("debug session must be stopped to inspect variables")
		}
		variables, err = session.Variables(r.Context(), body.Reference, body.Start, body.Count)
		return err
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if variables == nil {
		variables = []dap.Variable{}
	}
	writeJSON(w, map[string]any{"variables": variables})
}

func decodeDebugJSON(w http.ResponseWriter, r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, debugRequestLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return err
	}
	return nil
}

func publicDebugAdapters(root string, values []dap.AdapterInfo) []debugAdapter {
	result := make([]debugAdapter, 0, len(values))
	for _, value := range values {
		projects := make([]string, 0, len(value.Projects))
		for _, project := range value.Projects {
			rel, err := filepath.Rel(root, project)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				continue
			}
			if rel == "." {
				projects = append(projects, ".")
			} else {
				projects = append(projects, filepath.ToSlash(rel))
			}
		}
		result = append(result, debugAdapter{
			Name:               value.Name,
			Language:           value.Language,
			Projects:           projects,
			ConfigurationHint:  value.ConfigurationHint,
			IntegratedTerminal: value.TerminalStrategy != dap.TerminalUnsupported && terminal.Supported(),
		})
	}
	if result == nil {
		return []debugAdapter{}
	}
	return result
}

func selectDebugAdapters(values []dap.AdapterInfo, requested string) ([]dap.AdapterInfo, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" || requested == "auto" {
		return values, nil
	}
	for _, value := range values {
		if strings.EqualFold(value.Name, requested) || strings.EqualFold(value.Language, requested) {
			return []dap.AdapterInfo{value}, nil
		}
	}
	return nil, fmt.Errorf("debug adapter %q is not available", requested)
}

func nonNilDebugTargets(values []debugtarget.Target) []debugtarget.Target {
	if values == nil {
		return []debugtarget.Target{}
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
	rel, err := filepath.Rel(root, filepath.Clean(path))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		frame.Source.Path = ""
		return
	}
	frame.Source.Path = filepath.ToSlash(rel)
}

func (s *Server) debugStartOptions(plan debugLaunchPlan) (dap.StartOptions, error) {
	options := dap.StartOptions{
		Adapter:       plan.Adapter,
		ProjectDir:    plan.ProjectDir,
		Request:       plan.Request,
		Console:       dap.Console(plan.Console),
		Configuration: cloneJSONMap(plan.Configuration),
		Breakpoints:   make(map[string][]dap.SourceBreakpoint),
	}
	for _, breakpoint := range plan.Breakpoints {
		rel, ok := s.workspaceRel(breakpoint.FilePath)
		if !ok {
			return options, fmt.Errorf("invalid breakpoint path %q", breakpoint.FilePath)
		}
		path := filepath.Join(s.workspace.RootPath, rel)
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return options, fmt.Errorf("breakpoint path %q does not exist", breakpoint.FilePath)
		}
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
	return options, nil
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
		if strings.EqualFold(adapter.Name, plan.Adapter) || strings.EqualFold(adapter.Language, plan.Adapter) {
			plan.Adapter = adapter.Name
			selectedAdapter = adapter
			break
		}
	}
	if selectedAdapter == nil {
		return fmt.Errorf("adapter %q is not available", plan.Adapter)
	}
	plan.Console = strings.TrimSpace(plan.Console)
	if plan.Console == "" {
		plan.Console = string(dap.ConsoleInternal)
	}
	if plan.Console != string(dap.ConsoleInternal) && plan.Console != string(dap.ConsoleIntegrated) {
		return fmt.Errorf("console must be %s or %s", dap.ConsoleInternal, dap.ConsoleIntegrated)
	}
	if plan.Console == string(dap.ConsoleIntegrated) && (selectedAdapter.TerminalStrategy == dap.TerminalUnsupported || !terminal.Supported()) {
		return fmt.Errorf("adapter %s cannot use an integrated terminal on this host", selectedAdapter.Name)
	}
	plan.Request = strings.ToLower(strings.TrimSpace(plan.Request))
	if plan.Request != "launch" && plan.Request != "attach" {
		return errors.New("request must be launch or attach")
	}
	if plan.Configuration == nil {
		plan.Configuration = map[string]any{}
	}
	project, err := debugWorkspaceDirectory(s.workspace.RootPath, plan.ProjectDir)
	if err != nil {
		return err
	}
	plan.ProjectDir = project
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
	projectPath := filepath.Join(s.workspace.RootPath, filepath.FromSlash(plan.ProjectDir))
	if _, err := dap.ResolveConfigurationPaths(s.workspace.RootPath, projectPath, selectedAdapter.ConfigurationPaths, plan.Configuration); err != nil {
		return err
	}
	plan.Title = strings.TrimSpace(plan.Title)
	plan.Summary = strings.TrimSpace(plan.Summary)
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

func debugWorkspaceDirectory(root, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "." {
		return ".", nil
	}
	if filepath.IsAbs(value) {
		rel, err := filepath.Rel(root, value)
		if err != nil {
			return "", errors.New("project_dir must be inside the workspace")
		}
		value = rel
	}
	cleaned := filepath.Clean(filepath.FromSlash(value))
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", errors.New("project_dir must be inside the workspace")
	}
	info, err := os.Stat(filepath.Join(root, cleaned))
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("project_dir %q is not a workspace directory", value)
	}
	return filepath.ToSlash(cleaned), nil
}

func (s *Server) generateDebugPlan(ctx context.Context, request debugPlanRequest, selected *debugtarget.Target, adapters []dap.AdapterInfo, evidence string) (debugLaunchPlan, error) {
	instructions := `You create one reviewed Debug Adapter Protocol launch or attach plan for a local code workspace.
The installed debug adapter owns all language-specific configuration semantics. Use only the adapter hints and workspace evidence provided. Never invent a path, target, function, process ID, command, or environment value.
Return adapter-specific arguments in configuration_json as a JSON object string. Do not include request in that object. Use concrete paths interpreted from project_dir, never editor variables such as ${workspaceFolder}. Breakpoint file_path values are always workspace-relative.
Choose internalConsole for ordinary programs. Choose integratedTerminal only when the user intent or target evidence indicates interactive stdin, terminal control sequences, or a full-screen TUI and the selected adapter advertises it.
For action=debug and a selected source target, normally add a source breakpoint at that target so execution stops in useful code. For action=run, do not add breakpoints and use the adapter's no-debug configuration when its hint supports one.
Keep the summary short and explain what will execute. If the intent asks to attach but gives no concrete process ID, produce a launch plan instead. Do not emit shell commands.`

	var input strings.Builder
	fmt.Fprintf(&input, "Action: %s\n", request.Action)
	if request.Intent != "" {
		fmt.Fprintf(&input, "User intent: %s\n", request.Intent)
	}
	if selected != nil {
		encoded, _ := json.Marshal(selected)
		fmt.Fprintf(&input, "Selected target: %s\n", encoded)
	} else {
		input.WriteString("Selected target: none; choose the most likely target supported by the evidence.\n")
	}
	input.WriteString("Installed adapters:\n")
	for _, adapter := range publicDebugAdapters(s.workspace.RootPath, adapters) {
		fmt.Fprintf(&input, "- %s (%s), projects=%s, integrated_terminal=%t\n  hint: %s\n", adapter.Name, adapter.Language, strings.Join(adapter.Projects, ", "), adapter.IntegratedTerminal, adapter.ConfigurationHint)
	}
	input.WriteString("\nWorkspace evidence:\n")
	input.WriteString(evidence)

	model, effort := s.generationTarget("plan")
	result, err := s.config.Generate(ctx, agent.GenerateOptions{
		Model:           model,
		Effort:          effort,
		Instructions:    instructions,
		Input:           input.String(),
		OutputSchema:    debugPlanOutputSchema(adapters),
		MaxOutputTokens: 4_000,
	})
	if err != nil {
		return debugLaunchPlan{}, fmt.Errorf("generate debug configuration: %w", err)
	}
	var generated struct {
		Title               string                `json:"title"`
		Summary             string                `json:"summary"`
		Adapter             string                `json:"adapter"`
		ProjectDir          string                `json:"project_dir"`
		Request             string                `json:"request"`
		Console             string                `json:"console"`
		ConfigurationJSON   string                `json:"configuration_json"`
		Breakpoints         []debugPlanBreakpoint `json:"breakpoints"`
		FunctionBreakpoints []string              `json:"function_breakpoints"`
	}
	if err := json.Unmarshal([]byte(result.Text), &generated); err != nil {
		return debugLaunchPlan{}, fmt.Errorf("decode generated debug plan: %w", err)
	}
	configuration := map[string]any{}
	if err := json.Unmarshal([]byte(generated.ConfigurationJSON), &configuration); err != nil {
		return debugLaunchPlan{}, fmt.Errorf("decode generated adapter configuration: %w", err)
	}
	return debugLaunchPlan{
		Action: request.Action, Title: generated.Title, Summary: generated.Summary,
		Adapter: generated.Adapter, ProjectDir: generated.ProjectDir, Request: generated.Request,
		Console:       generated.Console,
		Configuration: configuration, Breakpoints: generated.Breakpoints,
		FunctionBreakpoints: generated.FunctionBreakpoints,
	}, nil
}

func debugPlanOutputSchema(adapters []dap.AdapterInfo) map[string]any {
	names := make([]string, 0, len(adapters))
	for _, adapter := range adapters {
		names = append(names, adapter.Name)
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title":              map[string]any{"type": "string"},
			"summary":            map[string]any{"type": "string"},
			"adapter":            map[string]any{"type": "string", "enum": names},
			"project_dir":        map[string]any{"type": "string"},
			"request":            map[string]any{"type": "string", "enum": []string{"launch", "attach"}},
			"console":            map[string]any{"type": "string", "enum": []string{string(dap.ConsoleInternal), string(dap.ConsoleIntegrated)}},
			"configuration_json": map[string]any{"type": "string"},
			"breakpoints": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"file_path":     map[string]any{"type": "string"},
						"line":          map[string]any{"type": "integer"},
						"column":        map[string]any{"type": "integer"},
						"condition":     map[string]any{"type": "string"},
						"hit_condition": map[string]any{"type": "string"},
						"log_message":   map[string]any{"type": "string"},
					},
					"required":             []string{"file_path", "line", "column", "condition", "hit_condition", "log_message"},
					"additionalProperties": false,
				},
			},
			"function_breakpoints": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required":             []string{"title", "summary", "adapter", "project_dir", "request", "console", "configuration_json", "breakpoints", "function_breakpoints"},
		"additionalProperties": false,
	}
}

func (s *Server) debugPlanningEvidence(ctx context.Context, targets []debugtarget.Target, selected *debugtarget.Target, currentPath string, adapters []dap.AdapterInfo) (string, error) {
	var evidence strings.Builder
	evidence.WriteString("Runnable candidates:\n")
	for index, target := range targets {
		if index >= 250 {
			fmt.Fprintf(&evidence, "- ... %d more candidates\n", len(targets)-index)
			break
		}
		fmt.Fprintf(&evidence, "- %s: %s %s in %s at %s:%d\n", target.ID, target.Kind, target.Name, target.Directory, target.Path, target.Line)
	}

	manifest, err := debugWorkspaceManifest(ctx, s.workspace.RootPath)
	if err != nil {
		return "", err
	}
	evidence.WriteString("Workspace files:\n")
	for _, file := range manifest {
		fmt.Fprintf(&evidence, "- %s\n", file)
	}

	relevant := map[string]bool{}
	if selected != nil {
		relevant[selected.Path] = true
	}
	if currentPath != "" {
		relevant[filepath.ToSlash(filepath.Clean(filepath.FromSlash(currentPath)))] = true
	}
	for _, adapter := range adapters {
		for _, project := range adapter.Projects {
			entries, _ := os.ReadDir(project)
			for _, entry := range entries {
				if entry.IsDir() || !debugEvidenceFile(entry.Name()) {
					continue
				}
				rel, err := filepath.Rel(s.workspace.RootPath, filepath.Join(project, entry.Name()))
				if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					relevant[filepath.ToSlash(rel)] = true
				}
			}
		}
	}

	evidence.WriteString("Relevant file contents:\n")
	remaining := debugEvidenceLimit - evidence.Len()
	relevantPaths := mapsKeys(relevant)
	slices.Sort(relevantPaths)
	for _, path := range relevantPaths {
		if remaining <= 0 {
			break
		}
		rel, ok := s.workspaceRel(path)
		if !ok {
			continue
		}
		content, err := s.workspace.Root.ReadFile(rel)
		if err != nil {
			continue
		}
		if len(content) > remaining {
			content = content[:remaining]
		}
		fmt.Fprintf(&evidence, "--- %s ---\n%s\n", path, content)
		remaining = debugEvidenceLimit - evidence.Len()
	}
	if evidence.Len() > debugEvidenceLimit {
		value := evidence.String()
		return value[:debugEvidenceLimit], nil
	}
	return evidence.String(), nil
}

func debugWorkspaceManifest(ctx context.Context, root string) ([]string, error) {
	files := make([]string, 0, debugManifestLimit)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && skipDebugDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if len(files) >= debugManifestLimit {
			return fs.SkipAll
		}
		rel, err := filepath.Rel(root, path)
		if err == nil {
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(files)
	return files, nil
}

func skipDebugDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules", "vendor", "testdata", "target", "build", "dist", "__pycache__", "venv":
		return true
	default:
		return false
	}
}

func debugEvidenceFile(name string) bool {
	lower := strings.ToLower(name)
	switch lower {
	case "go.mod", "go.work", "package.json", "cargo.toml", "pyproject.toml", "pom.xml", "build.gradle", "build.gradle.kts", "makefile":
		return true
	}
	return strings.HasSuffix(lower, ".csproj") || strings.HasSuffix(lower, ".sln")
}

func mapsKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	return result
}
