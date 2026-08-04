package main

import (
	"strings"
	"testing"
)

func testFlags(agent *string, port *int, plain *bool) *flagSet {
	fs := newFlags("wingman test")
	fs.String(agent, "--agent, -a name", "agent name")
	fs.Int(port, "--port N", "port to listen on")
	fs.Bool(plain, "--plain", "plain output")
	return fs
}

func TestFlagSetParse(t *testing.T) {
	var agent string
	var port int
	var plain bool

	fs := testFlags(&agent, &port, &plain)

	if err := fs.Parse([]string{"-a", "codex", "--port=8080", "--plain"}); err != nil {
		t.Fatal(err)
	}

	if agent != "codex" || port != 8080 || !plain {
		t.Fatalf("agent=%q port=%d plain=%v", agent, port, plain)
	}
}

func TestFlagSetParseSingleDash(t *testing.T) {
	var agent string
	var port int
	var plain bool

	fs := testFlags(&agent, &port, &plain)

	if err := fs.Parse([]string{"-port", "9090"}); err != nil {
		t.Fatal(err)
	}

	if port != 9090 {
		t.Fatalf("port = %d, want 9090", port)
	}
}

func TestFlagSetParseErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"unknown flag", []string{"--nope"}, "unknown flag"},
		{"unexpected argument", []string{"extra"}, "unexpected argument"},
		{"missing value", []string{"--agent"}, "requires a value"},
		{"bool with value", []string{"--plain=true"}, "does not take a value"},
		{"bad number", []string{"--port", "abc"}, "invalid value"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var agent string
			var port int
			var plain bool

			err := testFlags(&agent, &port, &plain).Parse(tt.args)

			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}
