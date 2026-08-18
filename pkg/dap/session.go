package dap

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	godap "github.com/google/go-dap"
)

const maxOutputBytes = 64 * 1024

var errSessionClosed = errors.New("debug session closed")

type responseResult struct {
	message godap.ResponseMessage
	err     error
}

type outputBuffer struct {
	mu      sync.Mutex
	content string
}

type terminalBinding struct {
	process TerminalProcess
	cancel  func()
}

func (buffer *outputBuffer) append(category, value string) {
	if value == "" {
		return
	}
	if category != "" && category != "stdout" && category != "console" {
		value = "[" + category + "] " + value
	}
	buffer.mu.Lock()
	buffer.content += value
	if len(buffer.content) > maxOutputBytes {
		buffer.content = buffer.content[len(buffer.content)-maxOutputBytes:]
		buffer.content = "[earlier debugger output truncated]\n" + buffer.content
	}
	buffer.mu.Unlock()
}

func (buffer *outputBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.content
}

type Session struct {
	id         string
	plan       Plan
	connection *adapterConnection
	startedAt  time.Time
	output     outputBuffer

	writeMu sync.Mutex
	seq     int

	pendingMu sync.Mutex
	pending   map[int]chan responseResult

	mu           sync.Mutex
	state        State
	stop         *Stop
	exitCode     *int
	terminalErr  error
	capabilities Capabilities
	terminalID   string
	terminals    []terminalBinding
	stateVersion uint64
	stateChanged chan struct{}
	initialized  chan struct{}
	initOnce     sync.Once
	launchDone   chan struct{}
	launchOnce   sync.Once

	alive         atomic.Bool
	finishOnce    sync.Once
	closeOnce     sync.Once
	terminateOnce sync.Once

	terminalLauncher TerminalLauncher
}

func startSession(ctx context.Context, id string, plan Plan, options StartOptions) (*Session, error) {
	session := &Session{
		id:               id,
		plan:             plan,
		startedAt:        time.Now(),
		pending:          make(map[int]chan responseResult),
		state:            StateStarting,
		stateChanged:     make(chan struct{}),
		initialized:      make(chan struct{}),
		launchDone:       make(chan struct{}),
		terminalLauncher: options.terminalLauncher,
	}
	connection, err := startAdapter(ctx, plan, session.output.append, options.terminalLauncher)
	if err != nil {
		return nil, err
	}
	session.connection = connection
	session.terminalID = connection.terminalID
	session.alive.Store(true)
	go session.readLoop()
	go session.watchProcess()

	if err := session.initializeAndLaunch(ctx, options); err != nil {
		adapterOutput := strings.TrimSpace(session.Output())
		session.closeResources()
		if adapterOutput != "" {
			return nil, fmt.Errorf("start %s debug session: %w\n\nDebugger output:\n%s", plan.Adapter.Language, err, adapterOutput)
		}
		return nil, fmt.Errorf("start %s debug session: %w", plan.Adapter.Language, err)
	}
	return session, nil
}

// newConnectedSession is used by protocol tests and by future adapter hosts
// that provide an already-connected DAP stream.
func newConnectedSession(id string, plan Plan, connection io.ReadWriteCloser) *Session {
	session := &Session{
		id:           id,
		plan:         plan,
		connection:   &adapterConnection{ReadWriteCloser: connection},
		startedAt:    time.Now(),
		pending:      make(map[int]chan responseResult),
		state:        StateStarting,
		stateChanged: make(chan struct{}),
		initialized:  make(chan struct{}),
		launchDone:   make(chan struct{}),
	}
	session.alive.Store(true)
	go session.readLoop()
	return session
}

func (session *Session) ID() string { return session.id }

func (session *Session) Status() Status {
	session.mu.Lock()
	defer session.mu.Unlock()
	status := Status{
		SessionID:    session.id,
		Adapter:      session.plan.Adapter.Name,
		Language:     session.plan.Adapter.Language,
		Target:       session.plan.Target,
		Mode:         session.plan.Mode,
		Request:      session.plan.Request,
		Console:      session.plan.Console,
		TerminalID:   session.terminalID,
		Capabilities: session.capabilities,
		StateVersion: session.stateVersion,
		State:        session.state,
		ExitCode:     cloneInt(session.exitCode),
		StartedAt:    session.startedAt,
	}
	if session.stop != nil {
		stop := *session.stop
		stop.HitBreakpointIDs = slices.Clone(session.stop.HitBreakpointIDs)
		status.Stop = &stop
	}
	if session.terminalErr != nil &&
		!errors.Is(session.terminalErr, io.EOF) &&
		!errors.Is(session.terminalErr, errSessionClosed) {
		status.Error = session.terminalErr.Error()
	}
	return status
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func (session *Session) Output() string { return session.output.String() }

func (session *Session) initializeAndLaunch(ctx context.Context, options StartOptions) error {
	defer session.launchOnce.Do(func() { close(session.launchDone) })
	response, err := session.request(ctx, &godap.InitializeRequest{
		Request: request("initialize"),
		Arguments: godap.InitializeRequestArguments{
			ClientID:                     "wingman",
			ClientName:                   "Wingman",
			AdapterID:                    session.plan.Adapter.AdapterID,
			Locale:                       "en-US",
			LinesStartAt1:                true,
			ColumnsStartAt1:              true,
			PathFormat:                   "path",
			SupportsVariableType:         true,
			SupportsVariablePaging:       true,
			SupportsRunInTerminalRequest: session.plan.Console == ConsoleIntegrated && session.terminalLauncher != nil,
		},
	})
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	initialized, ok := response.(*godap.InitializeResponse)
	if !ok {
		return fmt.Errorf("initialize returned %T", response)
	}
	session.mu.Lock()
	session.capabilities = Capabilities{SupportsStepBack: initialized.Body.SupportsStepBack}
	session.mu.Unlock()
	session.setState(StateConfiguring, nil)

	arguments, err := json.Marshal(session.plan.Arguments)
	if err != nil {
		return fmt.Errorf("encode launch arguments: %w", err)
	}
	startResult := make(chan error, 1)
	go func() {
		var requestErr error
		switch session.plan.Request {
		case "attach":
			_, requestErr = session.request(ctx, &godap.AttachRequest{
				Request:   request("attach"),
				Arguments: arguments,
			})
		default:
			_, requestErr = session.request(ctx, &godap.LaunchRequest{
				Request:   request("launch"),
				Arguments: arguments,
			})
		}
		startResult <- requestErr
	}()

	select {
	case <-session.initialized:
	case requestErr := <-startResult:
		if requestErr != nil {
			return requestErr
		}
		return fmt.Errorf("adapter answered %s before announcing initialized", session.plan.Request)
	case <-ctx.Done():
		return ctx.Err()
	}

	breakpointSets := make(map[string][]SourceBreakpoint, len(options.Breakpoints))
	for path, breakpoints := range options.Breakpoints {
		breakpointSets[path] = slices.Clone(breakpoints)
	}
	paths := make([]string, 0, len(breakpointSets))
	for path := range breakpointSets {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		breakpoints := breakpointSets[path]
		if _, err := session.SetBreakpoints(ctx, path, breakpoints); err != nil {
			return err
		}
	}
	if len(options.FunctionBreakpoints) > 0 {
		if !initialized.Body.SupportsFunctionBreakpoints {
			return fmt.Errorf("adapter %s does not support function breakpoints", session.plan.Adapter.Name)
		}
		if _, err := session.SetFunctionBreakpoints(ctx, options.FunctionBreakpoints); err != nil {
			return err
		}
	}
	if initialized.Body.SupportsConfigurationDoneRequest {
		if _, err := session.request(ctx, &godap.ConfigurationDoneRequest{Request: request("configurationDone")}); err != nil {
			return fmt.Errorf("configurationDone: %w", err)
		}
	}

	select {
	case requestErr := <-startResult:
		if requestErr != nil {
			return requestErr
		}
	case <-ctx.Done():
		return ctx.Err()
	}

	status := session.Status()
	if status.State == StateConfiguring {
		session.setState(StateRunning, nil)
	}
	return nil
}

func request(command string) godap.Request {
	return godap.Request{ProtocolMessage: godap.ProtocolMessage{Type: "request"}, Command: command}
}

func (session *Session) request(ctx context.Context, message godap.RequestMessage) (godap.ResponseMessage, error) {
	if !session.alive.Load() {
		status := session.Status()
		if status.Error != "" {
			return nil, fmt.Errorf("debug session is closed: %s", status.Error)
		}
		return nil, errors.New("debug session is closed")
	}

	session.writeMu.Lock()
	session.seq++
	seq := session.seq
	message.GetRequest().ProtocolMessage.Seq = seq
	message.GetRequest().ProtocolMessage.Type = "request"
	wait := make(chan responseResult, 1)
	session.pendingMu.Lock()
	session.pending[seq] = wait
	session.pendingMu.Unlock()
	err := godap.WriteProtocolMessage(session.connection, message)
	session.writeMu.Unlock()
	if err != nil {
		session.removePending(seq)
		return nil, fmt.Errorf("send %s: %w", message.GetRequest().Command, err)
	}

	select {
	case result := <-wait:
		if result.err != nil {
			return nil, result.err
		}
		if result.message == nil {
			return nil, fmt.Errorf("%s returned an empty response", message.GetRequest().Command)
		}
		if response := result.message.GetResponse(); !response.Success {
			return nil, dapResponseError(result.message)
		}
		return result.message, nil
	case <-ctx.Done():
		session.removePending(seq)
		return nil, ctx.Err()
	}
}

func (session *Session) removePending(seq int) {
	session.pendingMu.Lock()
	delete(session.pending, seq)
	session.pendingMu.Unlock()
}

func dapResponseError(message godap.ResponseMessage) error {
	response := message.GetResponse()
	detail := response.Message
	if wire, ok := message.(*godap.ErrorResponse); ok && wire.Body.Error != nil {
		detail = wire.Body.Error.Format
		for key, value := range wire.Body.Error.Variables {
			detail = strings.ReplaceAll(detail, "{"+key+"}", value)
		}
	}
	if detail == "" {
		detail = "request failed"
	}
	return fmt.Errorf("%s: %s", response.Command, detail)
}

func (session *Session) readLoop() {
	reader := bufio.NewReader(session.connection)
	for {
		message, err := godap.ReadProtocolMessage(reader)
		if err != nil {
			session.closeResourcesWithError(err)
			return
		}
		switch message := message.(type) {
		case godap.ResponseMessage:
			session.pendingMu.Lock()
			wait := session.pending[message.GetResponse().RequestSeq]
			delete(session.pending, message.GetResponse().RequestSeq)
			session.pendingMu.Unlock()
			if wait != nil {
				wait <- responseResult{message: message}
			}
		case godap.EventMessage:
			session.handleEvent(message)
		case godap.RequestMessage:
			go session.handleAdapterRequest(message)
		}
	}
}

func (session *Session) handleEvent(message godap.EventMessage) {
	switch event := message.(type) {
	case *godap.InitializedEvent:
		session.initOnce.Do(func() { close(session.initialized) })
	case *godap.StoppedEvent:
		session.mu.Lock()
		session.state = StateStopped
		session.stop = &Stop{
			Reason:            event.Body.Reason,
			Description:       event.Body.Description,
			ThreadID:          event.Body.ThreadId,
			AllThreadsStopped: event.Body.AllThreadsStopped,
			HitBreakpointIDs:  slices.Clone(event.Body.HitBreakpointIds),
		}
		session.notifyStateLocked()
		session.mu.Unlock()
	case *godap.ContinuedEvent:
		session.setState(StateRunning, nil)
	case *godap.ExitedEvent:
		exitCode := event.Body.ExitCode
		session.mu.Lock()
		session.exitCode = &exitCode
		session.state = StateTerminated
		session.stop = nil
		session.notifyStateLocked()
		session.mu.Unlock()
		session.closeAfterTermination()
	case *godap.TerminatedEvent:
		session.setState(StateTerminated, nil)
		session.closeAfterTermination()
	case *godap.OutputEvent:
		session.output.append(event.Body.Category, event.Body.Output)
	}
}

func (session *Session) closeAfterTermination() {
	session.terminateOnce.Do(func() {
		go func() {
			<-session.launchDone
			session.closeResourcesWithError(nil)
		}()
	})
}

func (session *Session) handleAdapterRequest(message godap.RequestMessage) {
	if request, ok := message.(*godap.RunInTerminalRequest); ok {
		session.respondRunInTerminal(request)
		return
	}
	session.respondError(message.GetRequest(), "notSupported", "Wingman does not support adapter-initiated request {command}")
}

func (session *Session) respondRunInTerminal(request *godap.RunInTerminalRequest) {
	if session.plan.Console != ConsoleIntegrated || session.terminalLauncher == nil {
		session.respondError(&request.Request, "notSupported", "Integrated terminal launch is not available")
		return
	}
	if request.Arguments.ArgsCanBeInterpretedByShell {
		session.respondError(&request.Request, "notSupported", "Shell-interpreted terminal arguments are not supported")
		return
	}
	if len(request.Arguments.Args) == 0 || strings.TrimSpace(request.Arguments.Args[0]) == "" {
		session.respondError(&request.Request, "invalidArgs", "runInTerminal requires a command")
		return
	}
	environment, err := terminalEnvironment(request.Arguments.Env)
	if err != nil {
		session.respondError(&request.Request, "invalidArgs", err.Error())
		return
	}
	dir := strings.TrimSpace(request.Arguments.Cwd)
	if dir == "" {
		dir = session.plan.ProjectDir
	}
	process, err := session.terminalLauncher.LaunchTerminal(context.Background(), TerminalLaunch{
		Title: request.Arguments.Title,
		Path:  request.Arguments.Args[0],
		Args:  slices.Clone(request.Arguments.Args[1:]),
		Dir:   dir,
		Env:   environment,
	})
	if err != nil {
		session.respondError(&request.Request, "runInTerminal", err.Error())
		return
	}
	session.trackTerminal(process)
	session.respond(&godap.RunInTerminalResponse{
		Response: godap.Response{
			ProtocolMessage: godap.ProtocolMessage{Type: "response"},
			RequestSeq:      request.Seq,
			Success:         true,
			Command:         request.Command,
		},
		Body: godap.RunInTerminalResponseBody{ProcessId: process.ProcessID()},
	})
}

func terminalEnvironment(values map[string]any) (map[string]*string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	result := make(map[string]*string, len(values))
	for key, value := range values {
		if strings.TrimSpace(key) == "" || strings.Contains(key, "=") {
			return nil, fmt.Errorf("runInTerminal environment variable name %q is invalid", key)
		}
		if value == nil {
			result[key] = nil
			continue
		}
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("runInTerminal environment %q must be a string or null", key)
		}
		textCopy := text
		result[key] = &textCopy
	}
	return result, nil
}

func (session *Session) trackTerminal(process TerminalProcess) {
	snapshot, output, cancel := process.Subscribe()
	session.mu.Lock()
	session.terminalID = process.ID()
	session.terminals = append(session.terminals, terminalBinding{process: process, cancel: cancel})
	session.mu.Unlock()
	if len(snapshot) > 0 {
		session.output.append("stdout", string(snapshot))
	}
	go func() {
		for chunk := range output {
			session.output.append("stdout", string(chunk))
		}
	}()
}

func (session *Session) respondError(request *godap.Request, id, format string) {
	session.respond(&godap.ErrorResponse{
		Response: godap.Response{
			ProtocolMessage: godap.ProtocolMessage{Type: "response"},
			RequestSeq:      request.Seq,
			Success:         false,
			Command:         request.Command,
			Message:         id,
		},
		Body: godap.ErrorResponseBody{Error: &godap.ErrorMessage{
			Id:        1,
			Format:    format,
			Variables: map[string]string{"command": request.Command},
			ShowUser:  true,
		}},
	})
}

func (session *Session) respond(response godap.ResponseMessage) {
	session.writeMu.Lock()
	defer session.writeMu.Unlock()
	session.seq++
	response.GetResponse().Seq = session.seq
	response.GetResponse().Type = "response"
	_ = godap.WriteProtocolMessage(session.connection, response)
}

func (session *Session) watchProcess() {
	if session.connection == nil || session.connection.processDone == nil {
		return
	}
	err, ok := <-session.connection.processDone
	if ok && err != nil && session.Status().State != StateTerminated {
		session.closeResourcesWithError(fmt.Errorf("debug adapter exited: %w", err))
	}
}

func (session *Session) finish(err error) {
	session.finishOnce.Do(func() {
		session.alive.Store(false)
		session.mu.Lock()
		if session.state != StateTerminated {
			session.state = StateTerminated
		}
		session.stop = nil
		if err != nil {
			session.terminalErr = err
		}
		session.notifyStateLocked()
		session.mu.Unlock()

		session.pendingMu.Lock()
		pending := session.pending
		session.pending = make(map[int]chan responseResult)
		session.pendingMu.Unlock()
		for _, wait := range pending {
			wait <- responseResult{err: err}
		}
	})
}

func (session *Session) setState(state State, stop *Stop) {
	session.mu.Lock()
	session.state = state
	session.stop = stop
	session.notifyStateLocked()
	session.mu.Unlock()
}

func (session *Session) notifyStateLocked() {
	session.stateVersion++
	close(session.stateChanged)
	session.stateChanged = make(chan struct{})
}

func (session *Session) waitForStop(ctx context.Context, after uint64) (Status, bool) {
	for {
		session.mu.Lock()
		version := session.stateVersion
		state := session.state
		changed := session.stateChanged
		session.mu.Unlock()
		if version > after && (state == StateStopped || state == StateTerminated) {
			return session.Status(), true
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return session.Status(), false
		}
	}
}

func (session *Session) stateEpoch() uint64 {
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.stateVersion
}

func (session *Session) SetBreakpoints(ctx context.Context, path string, values []SourceBreakpoint) ([]Breakpoint, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(session.plan.ProjectDir, path)
	}
	path = filepath.Clean(path)
	wire := make([]godap.SourceBreakpoint, 0, len(values))
	for _, value := range values {
		if value.Line <= 0 {
			return nil, errors.New("breakpoint line must be a positive 1-based integer")
		}
		wire = append(wire, godap.SourceBreakpoint{
			Line:         value.Line,
			Column:       value.Column,
			Condition:    value.Condition,
			HitCondition: value.HitCondition,
			LogMessage:   value.LogMessage,
		})
	}
	response, err := session.request(ctx, &godap.SetBreakpointsRequest{
		Request: request("setBreakpoints"),
		Arguments: godap.SetBreakpointsArguments{
			Source:      godap.Source{Name: filepath.Base(path), Path: path},
			Breakpoints: wire,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("set breakpoints in %s: %w", path, err)
	}
	result, ok := response.(*godap.SetBreakpointsResponse)
	if !ok {
		return nil, fmt.Errorf("setBreakpoints returned %T", response)
	}
	return breakpointsFromWire(result.Body.Breakpoints), nil
}

func (session *Session) SetFunctionBreakpoints(ctx context.Context, values []FunctionBreakpoint) ([]Breakpoint, error) {
	wire := make([]godap.FunctionBreakpoint, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value.Name) == "" {
			return nil, errors.New("function breakpoint name is required")
		}
		wire = append(wire, godap.FunctionBreakpoint{Name: value.Name, Condition: value.Condition, HitCondition: value.HitCondition})
	}
	response, err := session.request(ctx, &godap.SetFunctionBreakpointsRequest{
		Request:   request("setFunctionBreakpoints"),
		Arguments: godap.SetFunctionBreakpointsArguments{Breakpoints: wire},
	})
	if err != nil {
		return nil, fmt.Errorf("set function breakpoints: %w", err)
	}
	result, ok := response.(*godap.SetFunctionBreakpointsResponse)
	if !ok {
		return nil, fmt.Errorf("setFunctionBreakpoints returned %T", response)
	}
	return breakpointsFromWire(result.Body.Breakpoints), nil
}

func breakpointsFromWire(values []godap.Breakpoint) []Breakpoint {
	result := make([]Breakpoint, 0, len(values))
	for _, value := range values {
		result = append(result, Breakpoint{ID: value.Id, Verified: value.Verified, Message: value.Message, Line: value.Line, Column: value.Column})
	}
	return result
}

func (session *Session) Continue(ctx context.Context, threadID int) error {
	threadID, err := session.resolveThreadID(ctx, threadID)
	if err != nil {
		return err
	}
	return session.resumeRequest(ctx, &godap.ContinueRequest{Request: request("continue"), Arguments: godap.ContinueArguments{ThreadId: threadID}})
}

func (session *Session) Next(ctx context.Context, threadID int) error {
	threadID, err := session.resolveThreadID(ctx, threadID)
	if err != nil {
		return err
	}
	return session.resumeRequest(ctx, &godap.NextRequest{Request: request("next"), Arguments: godap.NextArguments{ThreadId: threadID}})
}

func (session *Session) StepIn(ctx context.Context, threadID int) error {
	threadID, err := session.resolveThreadID(ctx, threadID)
	if err != nil {
		return err
	}
	return session.resumeRequest(ctx, &godap.StepInRequest{Request: request("stepIn"), Arguments: godap.StepInArguments{ThreadId: threadID}})
}

func (session *Session) StepOut(ctx context.Context, threadID int) error {
	threadID, err := session.resolveThreadID(ctx, threadID)
	if err != nil {
		return err
	}
	return session.resumeRequest(ctx, &godap.StepOutRequest{Request: request("stepOut"), Arguments: godap.StepOutArguments{ThreadId: threadID}})
}

func (session *Session) StepBack(ctx context.Context, threadID int) error {
	if !session.Status().Capabilities.SupportsStepBack {
		return fmt.Errorf("debug adapter %s does not support stepping backward", session.plan.Adapter.Name)
	}
	threadID, err := session.resolveThreadID(ctx, threadID)
	if err != nil {
		return err
	}
	return session.resumeRequest(ctx, &godap.StepBackRequest{
		Request:   request("stepBack"),
		Arguments: godap.StepBackArguments{ThreadId: threadID},
	})
}

func (session *Session) resumeRequest(ctx context.Context, message godap.RequestMessage) error {
	previous := session.Status()
	if previous.State != StateStopped {
		return fmt.Errorf("debug session must be stopped before %s", message.GetRequest().Command)
	}
	// Mark the session running before sending. A fast adapter can emit the next
	// stopped event immediately after its response; updating state afterwards
	// would race with and overwrite that newer event.
	session.setState(StateRunning, nil)
	_, err := session.request(ctx, message)
	if err != nil {
		if session.Status().State == StateRunning {
			session.setState(StateStopped, previous.Stop)
		}
		return err
	}
	return nil
}

func (session *Session) Pause(ctx context.Context, threadID int) error {
	threadID, err := session.resolveThreadID(ctx, threadID)
	if err != nil {
		return err
	}
	_, err = session.request(ctx, &godap.PauseRequest{Request: request("pause"), Arguments: godap.PauseArguments{ThreadId: threadID}})
	return err
}

func (session *Session) WaitForStop(ctx context.Context, after uint64) (Status, bool) {
	return session.waitForStop(ctx, after)
}

func (session *Session) StateEpoch() uint64 { return session.stateEpoch() }

func (session *Session) Threads(ctx context.Context) ([]Thread, error) {
	response, err := session.request(ctx, &godap.ThreadsRequest{Request: request("threads")})
	if err != nil {
		return nil, err
	}
	result, ok := response.(*godap.ThreadsResponse)
	if !ok {
		return nil, fmt.Errorf("threads returned %T", response)
	}
	threads := make([]Thread, 0, len(result.Body.Threads))
	for _, thread := range result.Body.Threads {
		threads = append(threads, Thread{ID: thread.Id, Name: thread.Name})
	}
	return threads, nil
}

func (session *Session) resolveThreadID(ctx context.Context, threadID int) (int, error) {
	if threadID > 0 {
		return threadID, nil
	}
	status := session.Status()
	if status.Stop != nil && status.Stop.ThreadID > 0 {
		return status.Stop.ThreadID, nil
	}
	threads, err := session.Threads(ctx)
	if err != nil {
		return 0, err
	}
	if len(threads) == 0 {
		return 0, errors.New("debug adapter reported no threads")
	}
	return threads[0].ID, nil
}

func (session *Session) StackTrace(ctx context.Context, threadID, start, levels int) ([]StackFrame, int, error) {
	threadID, err := session.resolveThreadID(ctx, threadID)
	if err != nil {
		return nil, 0, err
	}
	response, err := session.request(ctx, &godap.StackTraceRequest{
		Request:   request("stackTrace"),
		Arguments: godap.StackTraceArguments{ThreadId: threadID, StartFrame: start, Levels: levels},
	})
	if err != nil {
		return nil, 0, err
	}
	result, ok := response.(*godap.StackTraceResponse)
	if !ok {
		return nil, 0, fmt.Errorf("stackTrace returned %T", response)
	}
	frames := make([]StackFrame, 0, len(result.Body.StackFrames))
	for _, frame := range result.Body.StackFrames {
		converted := StackFrame{ID: frame.Id, Name: frame.Name, Line: frame.Line, Column: frame.Column}
		if frame.Source != nil {
			converted.Source = &Source{Name: frame.Source.Name, Path: frame.Source.Path}
		}
		frames = append(frames, converted)
	}
	return frames, result.Body.TotalFrames, nil
}

func (session *Session) Scopes(ctx context.Context, frameID int) ([]Scope, error) {
	if frameID <= 0 {
		frames, _, err := session.StackTrace(ctx, 0, 0, 1)
		if err != nil {
			return nil, err
		}
		if len(frames) == 0 {
			return nil, errors.New("current thread has no stack frames")
		}
		frameID = frames[0].ID
	}
	response, err := session.request(ctx, &godap.ScopesRequest{Request: request("scopes"), Arguments: godap.ScopesArguments{FrameId: frameID}})
	if err != nil {
		return nil, err
	}
	result, ok := response.(*godap.ScopesResponse)
	if !ok {
		return nil, fmt.Errorf("scopes returned %T", response)
	}
	scopes := make([]Scope, 0, len(result.Body.Scopes))
	for _, scope := range result.Body.Scopes {
		scopes = append(scopes, Scope{
			Name: scope.Name, VariablesReference: scope.VariablesReference,
			NamedVariables: scope.NamedVariables, IndexedVariables: scope.IndexedVariables, Expensive: scope.Expensive,
		})
	}
	return scopes, nil
}

func (session *Session) Variables(ctx context.Context, reference, start, count int) ([]Variable, error) {
	if reference <= 0 {
		return nil, errors.New("variables_reference must be a positive integer returned by scopes, variables, or evaluate")
	}
	response, err := session.request(ctx, &godap.VariablesRequest{
		Request:   request("variables"),
		Arguments: godap.VariablesArguments{VariablesReference: reference, Start: start, Count: count},
	})
	if err != nil {
		return nil, err
	}
	result, ok := response.(*godap.VariablesResponse)
	if !ok {
		return nil, fmt.Errorf("variables returned %T", response)
	}
	variables := make([]Variable, 0, len(result.Body.Variables))
	for _, variable := range result.Body.Variables {
		variables = append(variables, Variable{
			Name: variable.Name, Value: variable.Value, Type: variable.Type, EvaluateName: variable.EvaluateName,
			VariablesReference: variable.VariablesReference, NamedVariables: variable.NamedVariables, IndexedVariables: variable.IndexedVariables,
		})
	}
	return variables, nil
}

func (session *Session) Evaluate(ctx context.Context, expression string, frameID int) (Evaluation, error) {
	return session.EvaluateContext(ctx, expression, frameID, "repl")
}

// EvaluateContext evaluates an expression using a standard DAP context such
// as "repl", "hover", or "watch". Adapters may format or restrict results
// differently for each context.
func (session *Session) EvaluateContext(ctx context.Context, expression string, frameID int, evaluationContext string) (Evaluation, error) {
	if strings.TrimSpace(expression) == "" {
		return Evaluation{}, errors.New("expression is required")
	}
	evaluationContext = strings.TrimSpace(evaluationContext)
	if evaluationContext == "" {
		evaluationContext = "repl"
	}
	if frameID <= 0 && session.Status().State == StateStopped {
		frames, _, err := session.StackTrace(ctx, 0, 0, 1)
		if err != nil {
			return Evaluation{}, err
		}
		if len(frames) > 0 {
			frameID = frames[0].ID
		}
	}
	response, err := session.request(ctx, &godap.EvaluateRequest{
		Request:   request("evaluate"),
		Arguments: godap.EvaluateArguments{Expression: expression, FrameId: frameID, Context: evaluationContext},
	})
	if err != nil {
		return Evaluation{}, err
	}
	result, ok := response.(*godap.EvaluateResponse)
	if !ok {
		return Evaluation{}, fmt.Errorf("evaluate returned %T", response)
	}
	return Evaluation{
		Result: result.Body.Result, Type: result.Body.Type, VariablesReference: result.Body.VariablesReference,
		NamedVariables: result.Body.NamedVariables, IndexedVariables: result.Body.IndexedVariables,
	}, nil
}

func (session *Session) Disconnect(ctx context.Context, terminate bool) error {
	var requestErr error
	if session.alive.Load() {
		_, requestErr = session.request(ctx, &godap.DisconnectRequest{
			Request:   request("disconnect"),
			Arguments: &godap.DisconnectArguments{TerminateDebuggee: terminate},
		})
	}
	session.closeResources()
	return requestErr
}

func (session *Session) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = session.Disconnect(ctx, true)
}

func (session *Session) closeResources() {
	session.closeResourcesWithError(errSessionClosed)
}

func (session *Session) closeResourcesWithError(closeErr error) {
	session.closeOnce.Do(func() {
		session.launchOnce.Do(func() { close(session.launchDone) })
		session.alive.Store(false)
		if session.connection != nil {
			_ = session.connection.Close()
		}
		session.mu.Lock()
		terminals := slices.Clone(session.terminals)
		session.terminals = nil
		session.mu.Unlock()
		for _, terminal := range terminals {
			terminal.cancel()
			_ = terminal.process.Close()
		}
		session.finish(closeErr)
	})
}
