package dap

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	godap "github.com/google/go-dap"
)

func TestReadDAPMessageAcceptsAdapterSpecificEvent(t *testing.T) {
	var wire bytes.Buffer
	payload := []byte(`{"seq":4,"type":"event","event":"debugpySockets","body":{"sockets":[]}}`)
	if err := godap.WriteBaseMessage(&wire, payload); err != nil {
		t.Fatal(err)
	}
	message, err := readDAPMessage(bufio.NewReader(&wire))
	if err != nil {
		t.Fatal(err)
	}
	event, ok := message.(*godap.Event)
	if !ok || event.Event != "debugpySockets" || event.Seq != 4 {
		t.Fatalf("message = %#v", message)
	}
}

func TestSessionLaunchAndInspectionFlow(t *testing.T) {
	client, server := net.Pipe()
	adapter := newFakeAdapter(t, server)
	go adapter.serve()

	plan := Plan{
		Adapter: AdapterDescriptor{Name: "fake", Language: "Go"},
		Target:  "/workspace/main.go",
		Mode:    "debug",
		Request: "launch",
		Arguments: map[string]any{
			"request": "launch",
			"program": "/workspace/main.go",
		},
	}
	session := newConnectedSession("test", plan, client)
	defer session.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := session.initializeAndLaunch(ctx, StartOptions{
		Breakpoints: map[string][]SourceBreakpoint{
			"/workspace/main.go": {{Line: 7}},
		},
		FunctionBreakpoints: []FunctionBreakpoint{{Name: "main.work"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	status, ok := session.WaitForStop(ctx, 0)
	if !ok || status.State != StateStopped || status.Stop == nil || status.Stop.ThreadID != 11 {
		t.Fatalf("status = %+v, stopped = %v", status, ok)
	}
	if !status.Capabilities.SupportsStepBack {
		t.Fatal("adapter step-back capability was not retained")
	}
	if adapter.sourceBreakpointLine != 7 || adapter.functionBreakpoint != "main.work" {
		t.Fatalf("adapter breakpoints = line %d function %q", adapter.sourceBreakpointLine, adapter.functionBreakpoint)
	}

	frames, total, err := session.StackTrace(ctx, 0, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(frames) != 1 || frames[0].ID != 21 || frames[0].Name != "main.work" {
		t.Fatalf("frames = %+v total = %d", frames, total)
	}

	scopes, err := session.Scopes(ctx, frames[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(scopes) != 1 || scopes[0].VariablesReference != 31 {
		t.Fatalf("scopes = %+v", scopes)
	}
	variables, err := session.Variables(ctx, scopes[0].VariablesReference, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(variables) != 1 || variables[0].Name != "answer" || variables[0].Value != "42" {
		t.Fatalf("variables = %+v", variables)
	}
	evaluation, err := session.Evaluate(ctx, "answer + 1", frames[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.Result != "43" || evaluation.Type != "int" {
		t.Fatalf("evaluation = %+v", evaluation)
	}

	epoch := session.StateEpoch()
	if err := session.Continue(ctx, 0); err != nil {
		t.Fatal(err)
	}
	status, ok = session.WaitForStop(ctx, epoch)
	if !ok || status.State != StateStopped || status.Stop.Reason != "breakpoint" {
		t.Fatalf("status after continue = %+v, stopped = %v", status, ok)
	}

	epoch = session.StateEpoch()
	if err := session.StepBack(ctx, 0); err != nil {
		t.Fatal(err)
	}
	status, ok = session.WaitForStop(ctx, epoch)
	if !ok || status.State != StateStopped || status.Stop.Reason != "step" {
		t.Fatalf("status after step back = %+v, stopped = %v", status, ok)
	}
}

func TestSessionClosesConnectionAfterUnexpectedEOF(t *testing.T) {
	connection := &unexpectedEOFConnection{closed: make(chan struct{})}
	session := newConnectedSession("eof", Plan{
		Adapter: AdapterDescriptor{Name: "fake", Language: "Test"},
	}, connection)

	select {
	case <-connection.closed:
	case <-time.After(time.Second):
		t.Fatal("session left the failed adapter connection open")
	}
	status := session.Status()
	if status.State != StateTerminated || !strings.Contains(status.Error, "unexpected EOF") {
		t.Fatalf("status = %+v", status)
	}
}

func TestSessionReportsPlainEOFAsUnexpectedWhileRunning(t *testing.T) {
	connection := &unexpectedEOFConnection{closed: make(chan struct{}), readErr: io.EOF}
	session := newConnectedSession("eof", Plan{
		Adapter: AdapterDescriptor{Name: "fake", Language: "Test"},
	}, connection)

	select {
	case <-connection.closed:
	case <-time.After(time.Second):
		t.Fatal("session left the failed adapter connection open")
	}
	status := session.Status()
	if status.State != StateTerminated || !strings.Contains(status.Error, "connection closed unexpectedly") {
		t.Fatalf("status = %+v", status)
	}
}

func TestFriendlyStartErrorExplainsMissingBrowser(t *testing.T) {
	err := friendlyStartError(Plan{Adapter: AdapterDescriptor{Language: "JavaScript/TypeScript"}}, errors.New("connection closed"), `Unable to find an installation of the browser on your system`)
	if got := err.Error(); !strings.Contains(got, "CHROME_PATH") || strings.Contains(got, "connection closed") {
		t.Fatalf("friendlyStartError = %q", got)
	}
}

func TestSessionAllowsLaunchResponseBeforeInitializedEvent(t *testing.T) {
	client, server := net.Pipe()
	adapter := newFakeAdapter(t, server)
	done := make(chan error, 1)
	go func() {
		defer server.Close()
		message, err := godap.ReadProtocolMessage(adapter.reader)
		if err != nil {
			done <- err
			return
		}
		initialize := message.(*godap.InitializeRequest)
		adapter.send(&godap.InitializeResponse{Response: adapter.response(initialize.Seq, "initialize")})
		message, err = godap.ReadProtocolMessage(adapter.reader)
		if err != nil {
			done <- err
			return
		}
		launch := message.(*godap.LaunchRequest)
		adapter.send(&godap.LaunchResponse{Response: adapter.response(launch.Seq, "launch")})
		time.Sleep(10 * time.Millisecond)
		adapter.send(&godap.InitializedEvent{Event: adapter.event("initialized")})
		done <- nil
	}()

	session := newConnectedSession("early-launch", Plan{
		Adapter:   AdapterDescriptor{Name: "fake", Language: "Test"},
		Request:   "launch",
		Arguments: map[string]any{"request": "launch"},
	}, client)
	defer session.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := session.initializeAndLaunch(ctx, StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSessionLaunchStopsWaitingWhenAdapterClosesBeforeInitialized(t *testing.T) {
	client, server := net.Pipe()
	adapter := newFakeAdapter(t, server)
	done := make(chan error, 1)
	go func() {
		defer server.Close()
		message, err := godap.ReadProtocolMessage(adapter.reader)
		if err != nil {
			done <- err
			return
		}
		initialize := message.(*godap.InitializeRequest)
		adapter.send(&godap.InitializeResponse{Response: adapter.response(initialize.Seq, "initialize")})
		message, err = godap.ReadProtocolMessage(adapter.reader)
		if err != nil {
			done <- err
			return
		}
		launch := message.(*godap.LaunchRequest)
		adapter.send(&godap.LaunchResponse{Response: adapter.response(launch.Seq, "launch")})
		done <- nil
	}()

	session := newConnectedSession("closed-before-initialized", Plan{
		Adapter:   AdapterDescriptor{Name: "fake", Language: "Test"},
		Request:   "launch",
		Arguments: map[string]any{"request": "launch"},
	}, client)
	defer session.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	err := session.initializeAndLaunch(ctx, StartOptions{})
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("launch error = %v, want connection closure", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("launch noticed connection closure after %s", elapsed)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSessionSendsLaunchBeforeConfigurationWhenAdapterInitializesEarly(t *testing.T) {
	client, server := net.Pipe()
	adapter := newFakeAdapter(t, server)
	done := make(chan error, 1)
	go func() {
		defer server.Close()
		message, err := godap.ReadProtocolMessage(adapter.reader)
		if err != nil {
			done <- err
			return
		}
		initialize := message.(*godap.InitializeRequest)
		adapter.send(&godap.InitializeResponse{
			Response: adapter.response(initialize.Seq, "initialize"),
			Body: godap.Capabilities{
				SupportsConfigurationDoneRequest: true,
			},
		})
		adapter.send(&godap.InitializedEvent{Event: adapter.event("initialized")})

		message, err = godap.ReadProtocolMessage(adapter.reader)
		if err != nil {
			done <- err
			return
		}
		launch, ok := message.(*godap.LaunchRequest)
		if !ok {
			done <- fmt.Errorf("request after early initialized event = %T, want launch", message)
			return
		}
		message, err = godap.ReadProtocolMessage(adapter.reader)
		if err != nil {
			done <- err
			return
		}
		configurationDone, ok := message.(*godap.ConfigurationDoneRequest)
		if !ok {
			done <- fmt.Errorf("request after launch = %T, want configurationDone", message)
			return
		}
		adapter.send(&godap.ConfigurationDoneResponse{Response: adapter.response(configurationDone.Seq, "configurationDone")})
		adapter.send(&godap.LaunchResponse{Response: adapter.response(launch.Seq, "launch")})
		done <- nil
	}()

	session := newConnectedSession("early-initialized", Plan{
		Adapter:   AdapterDescriptor{Name: "fake", Language: "Test"},
		Request:   "launch",
		Arguments: map[string]any{"request": "launch"},
	}, client)
	defer session.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := session.initializeAndLaunch(ctx, StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSessionCleanAdapterExitIsUnexpectedWhileRunning(t *testing.T) {
	processDone := make(chan error, 1)
	connection := &unexpectedEOFConnection{closed: make(chan struct{})}
	session := &Session{
		plan:         Plan{Adapter: AdapterDescriptor{Name: "fake", Language: "Test"}},
		connection:   &adapterConnection{ReadWriteCloser: connection, processDone: processDone},
		pending:      make(map[int]chan responseResult),
		state:        StateRunning,
		stateChanged: make(chan struct{}),
		launchDone:   make(chan struct{}),
	}
	session.alive.Store(true)
	processDone <- nil
	close(processDone)
	session.watchProcess()
	status := session.Status()
	if status.State != StateTerminated || !strings.Contains(status.Error, "exited unexpectedly") {
		t.Fatalf("status = %+v", status)
	}
}

func TestSessionFinishWakesPendingRequestsWithClosedError(t *testing.T) {
	wait := make(chan responseResult, 1)
	session := &Session{
		pending:      map[int]chan responseResult{1: wait},
		state:        StateRunning,
		stateChanged: make(chan struct{}),
	}
	session.finish(nil)
	result := <-wait
	if !errors.Is(result.err, errSessionClosed) {
		t.Fatalf("pending error = %v, want errSessionClosed", result.err)
	}
}

func TestSessionFinishSuppressesExpectedDisconnectError(t *testing.T) {
	session := &Session{state: StateRunning, stateChanged: make(chan struct{})}
	session.disconnecting.Store(true)
	session.finish(errors.New("adapter process killed"))
	if status := session.Status(); status.State != StateTerminated || status.Error != "" {
		t.Fatalf("status = %+v", status)
	}
}

func TestBreakpointValidationDoesNotMutateStoredState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.go")
	manager := NewManager(t.TempDir())
	if _, err := manager.SetBreakpoints(context.Background(), path, []SourceBreakpoint{{Line: 0}}); err == nil {
		t.Fatal("manager accepted an invalid breakpoint")
	}
	if breakpoints := manager.Breakpoints(path); len(breakpoints) != 0 {
		t.Fatalf("manager retained invalid breakpoints: %#v", breakpoints)
	}

	session := &Session{
		breakpoints:  make(map[string][]SourceBreakpoint),
		stateChanged: make(chan struct{}),
	}
	if _, err := session.SetBreakpoints(context.Background(), path, []SourceBreakpoint{{Line: 1, Column: -1}}); err == nil {
		t.Fatal("session accepted a negative breakpoint column")
	}
	if breakpoints := session.breakpointSnapshot(); len(breakpoints) != 0 {
		t.Fatalf("session retained invalid breakpoints: %#v", breakpoints)
	}
	if _, err := session.SetFunctionBreakpoints(context.Background(), []FunctionBreakpoint{{Name: "  "}}); err == nil {
		t.Fatal("session accepted a blank function breakpoint")
	}
	if functions := session.functionBreakpointSnapshot(); len(functions) != 0 {
		t.Fatalf("session retained invalid function breakpoints: %#v", functions)
	}
	if _, err := manager.Start(context.Background(), StartOptions{
		Breakpoints: map[string][]SourceBreakpoint{path: {{Line: -1}}},
	}); err == nil || !strings.Contains(err.Error(), "positive") {
		t.Fatalf("start error = %v, want breakpoint validation before adapter discovery", err)
	}
}

func TestFailedResumeDoesNotOverwriteNewerAdapterState(t *testing.T) {
	original := &Stop{Reason: "breakpoint", ThreadID: 1}
	session := &Session{state: StateRunning, stateVersion: 4, stateChanged: make(chan struct{})}

	newer := &Stop{Reason: "exception", ThreadID: 2}
	session.setState(StateStopped, newer)
	session.restoreFailedResume(4, original)

	status := session.Status()
	if status.State != StateStopped || status.Stop == nil || status.Stop.Reason != newer.Reason || status.Stop.ThreadID != newer.ThreadID {
		t.Fatalf("status = %+v, want newer adapter stop", status)
	}
}

func TestBeginResumeChecksAndTransitionsStateAtomically(t *testing.T) {
	stop := &Stop{Reason: "breakpoint", HitBreakpointIDs: []int{7}}
	session := &Session{state: StateStopped, stop: stop, stateVersion: 2, stateChanged: make(chan struct{})}
	previous, epoch, err := session.beginResume("next")
	if err != nil {
		t.Fatal(err)
	}
	if session.state != StateRunning || session.stop != nil || epoch != 3 {
		t.Fatalf("state = %s, stop = %#v, epoch = %d", session.state, session.stop, epoch)
	}
	stop.HitBreakpointIDs[0] = 9
	if previous == nil || !slices.Equal(previous.HitBreakpointIDs, []int{7}) {
		t.Fatalf("previous stop was not isolated: %#v", previous)
	}
	if _, _, err := session.beginResume("next"); err == nil {
		t.Fatal("second resume transition accepted a running session")
	}
}

func TestFinishConfigurationPreservesEarlyStop(t *testing.T) {
	stop := &Stop{Reason: "entry"}
	session := &Session{state: StateStopped, stop: stop, stateVersion: 3, stateChanged: make(chan struct{})}
	session.finishConfiguration()
	if session.state != StateStopped || session.stop != stop || session.stateVersion != 3 {
		t.Fatalf("state = %s, stop = %#v, version = %d", session.state, session.stop, session.stateVersion)
	}
}

func TestSessionDisconnectForcesCleanupWhenAdapterDoesNotRespond(t *testing.T) {
	connection := &blockingDAPConnection{closed: make(chan struct{})}
	session := newConnectedSession("blocked-disconnect", Plan{
		Adapter: AdapterDescriptor{Name: "fake", Language: "Test"},
	}, connection)

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if err := session.Disconnect(ctx, true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-connection.closed:
	default:
		t.Fatal("timed-out disconnect left the adapter connection open")
	}
	status := session.Status()
	if status.State != StateTerminated || status.Error != "" {
		t.Fatalf("status = %+v", status)
	}
}

func TestAttachChildRejectsTerminatedParent(t *testing.T) {
	parent := &Session{state: StateTerminated, stateChanged: make(chan struct{})}
	child := &Session{state: StateRunning, stateChanged: make(chan struct{})}
	if previous, attached := parent.attachChild(child); attached || previous != nil {
		t.Fatalf("attachChild = (%v, %v), want (nil, false)", previous, attached)
	}
	if child.parent != nil {
		t.Fatal("rejected child retained its parent")
	}
	if parent.child != nil {
		t.Fatal("terminated parent retained the rejected child")
	}
}

func TestAttachChildReplacesOwnership(t *testing.T) {
	parent := &Session{state: StateRunning, stateChanged: make(chan struct{})}
	parent.alive.Store(true)
	oldChild := &Session{state: StateRunning, stateChanged: make(chan struct{}), parent: parent}
	newChild := &Session{state: StateRunning, stateChanged: make(chan struct{})}
	parent.child = oldChild

	previous, attached := parent.attachChild(newChild)
	if !attached || previous != oldChild {
		t.Fatalf("attachChild = (%p, %v), want (%p, true)", previous, attached, oldChild)
	}
	if parent.child != newChild || newChild.parent != parent {
		t.Fatal("new child ownership was not connected")
	}
	if oldChild.parent != nil {
		t.Fatal("replaced child retained its parent")
	}
}

type unexpectedEOFConnection struct {
	closed  chan struct{}
	once    sync.Once
	readErr error
}

type blockingDAPConnection struct {
	closed chan struct{}
	once   sync.Once
}

func (connection *blockingDAPConnection) Read([]byte) (int, error) {
	<-connection.closed
	return 0, io.ErrClosedPipe
}

func (connection *blockingDAPConnection) Write([]byte) (int, error) {
	<-connection.closed
	return 0, io.ErrClosedPipe
}

func (connection *blockingDAPConnection) Close() error {
	connection.once.Do(func() { close(connection.closed) })
	return nil
}

func (connection *unexpectedEOFConnection) Read([]byte) (int, error) {
	if connection.readErr != nil {
		return 0, connection.readErr
	}
	return 0, io.ErrUnexpectedEOF
}

func (connection *unexpectedEOFConnection) Write(buffer []byte) (int, error) {
	return len(buffer), nil
}

func (connection *unexpectedEOFConnection) Close() error {
	connection.once.Do(func() { close(connection.closed) })
	return nil
}

type fakeAdapter struct {
	t      *testing.T
	conn   net.Conn
	reader *bufio.Reader
	mu     sync.Mutex
	seq    int

	launchSeq            int
	sourceBreakpointLine int
	functionBreakpoint   string
}

func newFakeAdapter(t *testing.T, conn net.Conn) *fakeAdapter {
	return &fakeAdapter{t: t, conn: conn, reader: bufio.NewReader(conn)}
}

func (adapter *fakeAdapter) serve() {
	defer adapter.conn.Close()
	for {
		message, err := godap.ReadProtocolMessage(adapter.reader)
		if err != nil {
			if err != io.EOF {
				adapter.t.Logf("fake adapter read: %v", err)
			}
			return
		}
		switch request := message.(type) {
		case *godap.InitializeRequest:
			adapter.send(&godap.InitializeResponse{
				Response: adapter.response(request.Seq, "initialize"),
				Body: godap.Capabilities{
					SupportsConfigurationDoneRequest: true,
					SupportsFunctionBreakpoints:      true,
					SupportsStepBack:                 true,
				},
			})
		case *godap.LaunchRequest:
			adapter.launchSeq = request.Seq
			adapter.send(&godap.InitializedEvent{Event: adapter.event("initialized")})
		case *godap.SetBreakpointsRequest:
			if len(request.Arguments.Breakpoints) > 0 {
				adapter.sourceBreakpointLine = request.Arguments.Breakpoints[0].Line
			}
			adapter.send(&godap.SetBreakpointsResponse{
				Response: adapter.response(request.Seq, "setBreakpoints"),
				Body:     godap.SetBreakpointsResponseBody{Breakpoints: []godap.Breakpoint{{Id: 1, Verified: true, Line: adapter.sourceBreakpointLine}}},
			})
		case *godap.SetFunctionBreakpointsRequest:
			if len(request.Arguments.Breakpoints) > 0 {
				adapter.functionBreakpoint = request.Arguments.Breakpoints[0].Name
			}
			adapter.send(&godap.SetFunctionBreakpointsResponse{
				Response: adapter.response(request.Seq, "setFunctionBreakpoints"),
				Body:     godap.SetFunctionBreakpointsResponseBody{Breakpoints: []godap.Breakpoint{{Id: 2, Verified: true}}},
			})
		case *godap.ConfigurationDoneRequest:
			adapter.send(&godap.ConfigurationDoneResponse{Response: adapter.response(request.Seq, "configurationDone")})
			adapter.send(&godap.LaunchResponse{Response: adapter.response(adapter.launchSeq, "launch")})
			adapter.send(&godap.StoppedEvent{
				Event: adapter.event("stopped"),
				Body:  godap.StoppedEventBody{Reason: "entry", ThreadId: 11, AllThreadsStopped: true},
			})
		case *godap.ThreadsRequest:
			adapter.send(&godap.ThreadsResponse{
				Response: adapter.response(request.Seq, "threads"),
				Body:     godap.ThreadsResponseBody{Threads: []godap.Thread{{Id: 11, Name: "goroutine 1"}}},
			})
		case *godap.StackTraceRequest:
			adapter.send(&godap.StackTraceResponse{
				Response: adapter.response(request.Seq, "stackTrace"),
				Body: godap.StackTraceResponseBody{
					StackFrames: []godap.StackFrame{{Id: 21, Name: "main.work", Source: &godap.Source{Path: "/workspace/main.go"}, Line: 7, Column: 1}},
					TotalFrames: 1,
				},
			})
		case *godap.ScopesRequest:
			adapter.send(&godap.ScopesResponse{
				Response: adapter.response(request.Seq, "scopes"),
				Body: godap.ScopesResponseBody{Scopes: []godap.Scope{{
					Name: "Locals", VariablesReference: 31,
				}}},
			})
		case *godap.VariablesRequest:
			adapter.send(&godap.VariablesResponse{
				Response: adapter.response(request.Seq, "variables"),
				Body: godap.VariablesResponseBody{Variables: []godap.Variable{{
					Name: "answer", Value: "42", Type: "int",
				}}},
			})
		case *godap.EvaluateRequest:
			adapter.send(&godap.EvaluateResponse{
				Response: adapter.response(request.Seq, "evaluate"),
				Body:     godap.EvaluateResponseBody{Result: "43", Type: "int"},
			})
		case *godap.ContinueRequest:
			adapter.send(&godap.ContinueResponse{
				Response: adapter.response(request.Seq, "continue"),
				Body:     godap.ContinueResponseBody{AllThreadsContinued: true},
			})
			adapter.send(&godap.StoppedEvent{
				Event: adapter.event("stopped"),
				Body:  godap.StoppedEventBody{Reason: "breakpoint", ThreadId: 11, AllThreadsStopped: true},
			})
		case *godap.StepBackRequest:
			adapter.send(&godap.StepBackResponse{Response: adapter.response(request.Seq, "stepBack")})
			adapter.send(&godap.StoppedEvent{
				Event: adapter.event("stopped"),
				Body:  godap.StoppedEventBody{Reason: "step", ThreadId: 11, AllThreadsStopped: true},
			})
		case *godap.DisconnectRequest:
			adapter.send(&godap.DisconnectResponse{Response: adapter.response(request.Seq, "disconnect")})
			return
		}
	}
}

func (adapter *fakeAdapter) response(requestSeq int, command string) godap.Response {
	return godap.Response{
		ProtocolMessage: godap.ProtocolMessage{Type: "response"},
		RequestSeq:      requestSeq,
		Success:         true,
		Command:         command,
	}
}

func (adapter *fakeAdapter) event(name string) godap.Event {
	return godap.Event{ProtocolMessage: godap.ProtocolMessage{Type: "event"}, Event: name}
}

func (adapter *fakeAdapter) send(message godap.Message) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	adapter.seq++
	switch message := message.(type) {
	case godap.ResponseMessage:
		message.GetResponse().Seq = adapter.seq
	case godap.EventMessage:
		message.GetEvent().Seq = adapter.seq
	case godap.RequestMessage:
		message.GetRequest().Seq = adapter.seq
	}
	if err := godap.WriteProtocolMessage(adapter.conn, message); err != nil {
		adapter.t.Logf("fake adapter send: %v", err)
	}
}
