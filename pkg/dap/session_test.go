package dap

import (
	"bufio"
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	godap "github.com/google/go-dap"
)

func TestSessionLaunchAndInspectionFlow(t *testing.T) {
	client, server := net.Pipe()
	adapter := newFakeAdapter(t, server)
	go adapter.serve()

	plan := Plan{
		Adapter: Adapter{Name: "fake", Language: "Go"},
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
