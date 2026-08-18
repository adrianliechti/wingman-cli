package dap

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	godap "github.com/google/go-dap"
)

func TestRunInTerminalRequestUsesConfiguredHost(t *testing.T) {
	client, server := net.Pipe()
	adapter := newFakeAdapter(t, server)
	protocolDone := make(chan error, 1)
	go func() { protocolDone <- serveRunInTerminalAdapter(adapter) }()

	process := newFakeTerminalProcess()
	launcher := &fakeTerminalLauncher{process: process, launches: make(chan TerminalLaunch, 1)}
	plan := Plan{
		Adapter:    AdapterDescriptor{Name: "fake", Language: "Test", AdapterID: "fake", TerminalStrategy: TerminalRunInTerminal},
		ProjectDir: "/workspace",
		Request:    "launch",
		Console:    ConsoleIntegrated,
		Arguments:  map[string]any{"request": "launch"},
	}
	session := newConnectedSession("terminal-test", plan, client)
	session.terminalLauncher = launcher

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := session.initializeAndLaunch(ctx, StartOptions{}); err != nil {
		t.Fatal(err)
	}

	select {
	case launch := <-launcher.launches:
		if launch.Path != "/bin/example" || len(launch.Args) != 1 || launch.Args[0] != "argument" || launch.Dir != "/workspace/project" {
			t.Fatalf("terminal launch = %#v", launch)
		}
		if launch.Env["TOKEN"] == nil || *launch.Env["TOKEN"] != "value" || launch.Env["REMOVE"] != nil {
			t.Fatalf("terminal environment = %#v", launch.Env)
		}
	case <-ctx.Done():
		t.Fatal("adapter terminal request was not handled")
	}
	status := session.Status()
	if status.TerminalID != process.ID() || status.Console != ConsoleIntegrated {
		t.Fatalf("session status = %+v", status)
	}
	if !strings.Contains(session.Output(), "terminal ready") {
		t.Fatalf("terminal output = %q", session.Output())
	}
	select {
	case <-process.Done():
		status = session.Status()
		if status.State != StateTerminated || status.Error != "" {
			t.Fatalf("natural termination status = %+v", status)
		}
	case <-ctx.Done():
		t.Fatal("natural termination did not close the integrated terminal")
	}

	session.Close()
	select {
	case err := <-protocolDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("fake adapter did not finish")
	}
}

func serveRunInTerminalAdapter(adapter *fakeAdapter) error {
	message, err := godap.ReadProtocolMessage(adapter.reader)
	if err != nil {
		return err
	}
	initialize, ok := message.(*godap.InitializeRequest)
	if !ok {
		return fmt.Errorf("first request = %T", message)
	}
	if !initialize.Arguments.SupportsRunInTerminalRequest {
		return errors.New("client did not advertise runInTerminal")
	}
	adapter.send(&godap.InitializeResponse{Response: adapter.response(initialize.Seq, "initialize")})

	message, err = godap.ReadProtocolMessage(adapter.reader)
	if err != nil {
		return err
	}
	launch, ok := message.(*godap.LaunchRequest)
	if !ok {
		return fmt.Errorf("second request = %T", message)
	}
	adapter.send(&godap.InitializedEvent{Event: adapter.event("initialized")})
	adapter.send(&godap.RunInTerminalRequest{
		Request: godap.Request{ProtocolMessage: godap.ProtocolMessage{Type: "request"}, Command: "runInTerminal"},
		Arguments: godap.RunInTerminalRequestArguments{
			Kind:  "integrated",
			Title: "Interactive target",
			Cwd:   "/workspace/project",
			Args:  []string{"/bin/example", "argument"},
			Env:   map[string]any{"TOKEN": "value", "REMOVE": nil},
		},
	})

	message, err = godap.ReadProtocolMessage(adapter.reader)
	if err != nil {
		return err
	}
	terminalResponse, ok := message.(*godap.RunInTerminalResponse)
	if !ok || !terminalResponse.Success || terminalResponse.Body.ProcessId != 4242 {
		return fmt.Errorf("runInTerminal response = %#v", message)
	}
	adapter.send(&godap.LaunchResponse{Response: adapter.response(launch.Seq, "launch")})
	adapter.send(&godap.ExitedEvent{Event: adapter.event("exited"), Body: godap.ExitedEventBody{ExitCode: 0}})
	adapter.send(&godap.TerminatedEvent{Event: adapter.event("terminated")})
	if _, err := godap.ReadProtocolMessage(adapter.reader); err == nil {
		return errors.New("client left the adapter connection open after termination")
	}
	return nil
}

type fakeTerminalLauncher struct {
	process  *fakeTerminalProcess
	launches chan TerminalLaunch
}

func (launcher *fakeTerminalLauncher) LaunchTerminal(_ context.Context, launch TerminalLaunch) (TerminalProcess, error) {
	launcher.launches <- launch
	return launcher.process, nil
}

type fakeTerminalProcess struct {
	done   chan struct{}
	output chan []byte
	once   sync.Once
}

func newFakeTerminalProcess() *fakeTerminalProcess {
	return &fakeTerminalProcess{done: make(chan struct{}), output: make(chan []byte)}
}

func (process *fakeTerminalProcess) ID() string            { return "terminal-1" }
func (process *fakeTerminalProcess) ProcessID() int        { return 4242 }
func (process *fakeTerminalProcess) Done() <-chan struct{} { return process.done }
func (process *fakeTerminalProcess) Subscribe() ([]byte, <-chan []byte, func()) {
	return []byte("terminal ready\n"), process.output, func() {}
}
func (process *fakeTerminalProcess) Close() error {
	process.once.Do(func() {
		close(process.output)
		close(process.done)
	})
	return nil
}
