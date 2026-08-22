package dap

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

type testAdapterConnector struct {
	connection io.ReadWriteCloser
}

func (connector *testAdapterConnector) ConnectAdapter(context.Context, Plan) (io.ReadWriteCloser, error) {
	return connector.connection, nil
}

func TestConnectedAdapterRetainsConnectorForNestedSessions(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	connector := &testAdapterConnector{connection: client}
	connection, err := startAdapter(context.Background(), Plan{
		Adapter: AdapterDescriptor{Name: "test", Transport: TransportConnect},
	}, func(string, string) {}, nil, connector)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if connection.childConnector() != connector {
		t.Fatal("connected child session lost its reusable adapter connector")
	}
}

func TestTCPAdapterOutputStreamsPromptWithoutNewline(t *testing.T) {
	reader, writer := io.Pipe()
	ready := make(chan readyResult, 1)
	output := make(chan string, 1)
	go readTCPAdapterOutput(reader, "ready:", func(_ string, value string) {
		output <- value
	}, ready)

	go func() {
		_, _ = writer.Write([]byte("ready: 127.0.0.1:1234\nprompt> "))
	}()
	select {
	case result := <-ready:
		if result.err != nil || result.address != "127.0.0.1:1234" {
			t.Fatalf("ready = %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("adapter readiness was not reported")
	}
	select {
	case value := <-output:
		if value != "prompt> " {
			t.Fatalf("output = %q", value)
		}
	case <-time.After(time.Second):
		t.Fatal("partial prompt was line-buffered")
	}
	_ = writer.Close()
}

func TestNormalizeAdapterAddressUsesLoopback(t *testing.T) {
	values := map[string]string{
		"4711":           "127.0.0.1:4711",
		"0.0.0.0:4711":   "127.0.0.1:4711",
		"127.0.0.1:4711": "127.0.0.1:4711",
		"localhost:4711": "127.0.0.1:4711",
		"[::]:4711":      "[::1]:4711",
		"[::1]:4711":     "[::1]:4711",
	}
	for input, want := range values {
		got, err := normalizeAdapterAddress(input)
		if err != nil {
			t.Fatalf("normalizeAdapterAddress(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("normalizeAdapterAddress(%q) = %q, want %q", input, got, want)
		}
	}
	for _, input := range []string{"", "0", "70000", "localhost", "remote.example:4711", "remote.example:abc"} {
		if _, err := normalizeAdapterAddress(input); err == nil {
			t.Fatalf("normalizeAdapterAddress(%q) succeeded", input)
		}
	}
}
