package main

import (
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/code"
)

func TestParseTUIArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want tuiOptions
	}{
		{
			name: "defaults to wingman",
			want: tuiOptions{Agent: "wingman"},
		},
		{
			name: "codex",
			args: []string{"--agent", "codex"},
			want: tuiOptions{Agent: "codex"},
		},
		{
			name: "claude latest",
			args: []string{"--agent", "claude", "--resume"},
			want: tuiOptions{Agent: "claude", SessionID: "latest"},
		},
		{
			name: "specific codex session",
			args: []string{"--resume", "session-123", "-a", "codex"},
			want: tuiOptions{Agent: "codex", SessionID: "session-123"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTUIArgs(tt.args)
			if err != nil {
				t.Fatalf("parseTUIArgs() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("parseTUIArgs() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParseTUIArgsRejectsMissingAgent(t *testing.T) {
	if _, err := parseTUIArgs([]string{"--agent"}); err == nil {
		t.Fatal("parseTUIArgs() succeeded without an agent name")
	}
}

func TestParseTUIArgsAcceptsConfiguredAgentName(t *testing.T) {
	got, err := parseTUIArgs([]string{"--agent", "custom-agent"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Agent != "custom-agent" {
		t.Fatalf("agent = %q, want custom-agent", got.Agent)
	}
}

func TestLatestSessionID(t *testing.T) {
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		sessions []code.SessionInfo
		want     string
	}{
		{
			name: "empty",
		},
		{
			name: "picks most recent regardless of order",
			sessions: []code.SessionInfo{
				{ID: "old", UpdatedAt: base},
				{ID: "newest", UpdatedAt: base.Add(2 * time.Hour)},
				{ID: "newer", UpdatedAt: base.Add(time.Hour)},
			},
			want: "newest",
		},
		{
			name: "falls back to first without timestamps",
			sessions: []code.SessionInfo{
				{ID: "first"},
				{ID: "second"},
			},
			want: "first",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := latestSessionID(tt.sessions); got != tt.want {
				t.Fatalf("latestSessionID() = %q, want %q", got, tt.want)
			}
		})
	}
}
