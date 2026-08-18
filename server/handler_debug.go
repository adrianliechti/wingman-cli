package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/dap"
	"github.com/adrianliechti/wingman-agent/pkg/debugadapter"
	"github.com/adrianliechti/wingman-agent/pkg/terminal"
)

const (
	debugRequestLimit       = 2 << 20
	debugControlMaxWait     = 30 * time.Second
	debugStateRequestBudget = 2 * time.Second
)

type debugAdapter struct {
	Name               string   `json:"name"`
	Language           string   `json:"language"`
	Projects           []string `json:"projects"`
	IntegratedTerminal bool     `json:"integrated_terminal"`
}

type debugDiscoveryResponse struct {
	Adapters []debugAdapter        `json:"adapters"`
	Targets  []debugadapter.Target `json:"targets"`
	Session  *dap.Status           `json:"session,omitempty"`
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

	var currentFile string
	if requested := strings.TrimSpace(r.URL.Query().Get("path")); requested != "" {
		rel, ok := s.resolveExistingRegularFile(w, requested)
		if !ok {
			return
		}
		currentFile = rel
	}
	targets, err := s.detectDebugTargets(r.Context(), currentFile, true)
	if err != nil {
		code := http.StatusInternalServerError
		if r.Context().Err() != nil {
			code = http.StatusRequestTimeout
		}
		http.Error(w, err.Error(), code)
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
	targets, err := debugadapter.NewRegistry().DetectFile(filepath.ToSlash(rel), source)
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

	var currentFile string
	if request.CurrentPath != "" {
		rel, ok := s.resolveExistingRegularFile(w, request.CurrentPath)
		if !ok {
			return
		}
		currentFile = rel
		request.CurrentPath = filepath.ToSlash(rel)
	}
	targets, err := s.detectDebugTargets(r.Context(), currentFile, request.TargetID == "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var selected *debugadapter.Target
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
	if selected == nil {
		selected, err = selectDeterministicDebugTarget(targets, request.CurrentPath, adapterInfo)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	adapterInfo, err = selectTargetDebugAdapter(adapterInfo, *selected)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	projectDir, err := selectTargetDebugProject(s.workspace.RootPath, adapterInfo[0], *selected)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	profile, err := debugadapter.NewRegistry().Plan(adapterInfo[0].Language, debugadapter.Request{
		Action: request.Action, ProjectDir: projectDir, Target: *selected,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	plan := debugLaunchPlan{
		Action: request.Action, Title: profile.Title, Summary: profile.Summary,
		Adapter: adapterInfo[0].Name, ProjectDir: profile.ProjectDir, Request: profile.Request,
		Console: profile.Console, Configuration: profile.Configuration,
		FunctionBreakpoints: profile.FunctionBreakpoints,
	}
	for _, breakpoint := range profile.Breakpoints {
		plan.Breakpoints = append(plan.Breakpoints, debugPlanBreakpoint{
			FilePath: breakpoint.FilePath, Line: breakpoint.Line, Column: breakpoint.Column,
		})
	}
	if err := s.validateDebugPlan(&plan, adapterInfo); err != nil {
		http.Error(w, "invalid deterministic debug configuration: "+err.Error(), http.StatusUnprocessableEntity)
		return
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
		if errors.Is(err, dap.ErrActiveSession) {
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
			err = manager.Stop(r.Context(), body.SessionID)
			current := session.Status()
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
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		http.Error(w, "invalid body: expected a single JSON value", http.StatusBadRequest)
		return errors.New("request body contains more than one JSON value")
	}
	return nil
}

func (s *Server) detectDebugTargets(ctx context.Context, currentFile string, fallback bool) ([]debugadapter.Target, error) {
	registry := debugadapter.NewRegistry()
	if currentFile != "" {
		source, err := s.workspace.Root.ReadFile(currentFile)
		if err != nil {
			return nil, err
		}
		targets, err := registry.DetectFile(filepath.ToSlash(currentFile), source)
		if err != nil || len(targets) > 0 || !fallback {
			return targets, err
		}
	}
	return registry.DetectWorkspace(ctx, s.workspace.RootPath)
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

func selectDeterministicDebugTarget(values []debugadapter.Target, currentPath string, adapters []dap.AdapterInfo) (*debugadapter.Target, error) {
	languages := make(map[string]bool, len(adapters))
	for _, adapter := range adapters {
		languages[strings.ToLower(adapter.Language)] = true
	}
	currentPath = strings.TrimSpace(currentPath)
	if currentPath != "" {
		currentPath = filepath.ToSlash(filepath.Clean(filepath.FromSlash(currentPath)))
	}
	candidates := make([]debugadapter.Target, 0, len(values))
	current := make([]debugadapter.Target, 0, 1)
	for _, target := range values {
		if !languages[strings.ToLower(target.Language)] {
			continue
		}
		candidates = append(candidates, target)
		if currentPath != "" && target.Path == currentPath {
			current = append(current, target)
		}
	}
	if len(current) == 1 {
		return &current[0], nil
	}
	if len(current) > 1 {
		return nil, fmt.Errorf("choose one of the %d debug targets in %s", len(current), currentPath)
	}
	if len(candidates) == 1 {
		return &candidates[0], nil
	}
	if len(candidates) == 0 {
		return nil, errors.New("no deterministic debug target was found for an installed adapter")
	}
	return nil, fmt.Errorf("choose a debug target; %d runnable targets were found", len(candidates))
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
	rel, err := filepath.Rel(root, filepath.Clean(path))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		frame.Source.Path = ""
		return
	}
	frame.Source.Path = filepath.ToSlash(rel)
}

func (s *Server) debugStartOptions(plan debugLaunchPlan) dap.StartOptions {
	options := dap.StartOptions{
		Adapter:       plan.Adapter,
		ProjectDir:    plan.ProjectDir,
		Request:       plan.Request,
		Console:       dap.Console(plan.Console),
		Configuration: cloneJSONMap(plan.Configuration),
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
