package codex

import (
	"encoding/json"
	"testing"

	"github.com/coder/acp-go-sdk"
)

func TestWebSearchTitle(t *testing.T) {
	cases := map[string]string{
		`{"query":"go generics"}`: "Web search: go generics",
		`{"query":""}`:            "Web search",
		`{"query":"x","action":{"type":"search","query":"narrowed"}}`:    "Web search: narrowed",
		`{"query":"","action":{"type":"search","queries":["a","","b"]}}`: "Web search: a, b",
		`{"action":{"type":"openPage","url":"https://x.dev"}}`:           "Open page: https://x.dev",
		`{"action":{"type":"findInPage","url":"u","pattern":"p"}}`:       "Find in page for 'p' in u",
		`{"action":{"type":"other"}}`:                                    "Web search",
	}
	for in, want := range cases {
		var it webSearchItem
		if err := json.Unmarshal([]byte(in), &it); err != nil {
			t.Fatalf("unmarshal %s: %v", in, err)
		}
		if got := webSearchTitle(it); got != want {
			t.Errorf("webSearchTitle(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestJoinReasoning(t *testing.T) {
	if got := joinReasoning([]string{"a", "", "b"}, nil); got != "a\n\nb" {
		t.Errorf("summary join = %q", got)
	}
	if got := joinReasoning(nil, []string{"only-content"}); got != "only-content" {
		t.Errorf("content fallback = %q", got)
	}
	if got := joinReasoning(nil, nil); got != "" {
		t.Errorf("empty = %q", got)
	}
}

func TestImageGenStatus(t *testing.T) {
	cases := map[string]acp.ToolCallStatus{
		"completed":  acp.ToolCallStatusCompleted,
		"generating": acp.ToolCallStatusInProgress,
		"inProgress": acp.ToolCallStatusInProgress,
		"incomplete": acp.ToolCallStatusInProgress,
		"failed":     acp.ToolCallStatusFailed,
		"weird":      acp.ToolCallStatusCompleted,
	}
	for in, want := range cases {
		if got := imageGenStatus(in); got != want {
			t.Errorf("imageGenStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGuardianStatus(t *testing.T) {
	cases := map[string]acp.ToolCallStatus{
		"inProgress": acp.ToolCallStatusInProgress,
		"approved":   acp.ToolCallStatusCompleted,
		"denied":     acp.ToolCallStatusFailed,
		"timedOut":   acp.ToolCallStatusFailed,
	}
	for in, want := range cases {
		if got := guardianStatus(in); got != want {
			t.Errorf("guardianStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGuardianActionSummary(t *testing.T) {
	cases := map[string]string{
		`{"type":"command","command":"rm -rf /"}`:                  "shell rm -rf /",
		`{"type":"applyPatch","files":["a.go"]}`:                   "apply_patch touching a.go",
		`{"type":"applyPatch","files":["a.go","b.go"]}`:            "apply_patch touching 2 files",
		`{"type":"networkAccess","host":"example.com"}`:            "network access to example.com",
		`{"type":"mcpToolCall","server":"srv","toolName":"fetch"}`: "MCP fetch on srv",
		`{"type":"unknownKind"}`:                                   "",
	}
	for in, want := range cases {
		if got := guardianActionSummary([]byte(in)); got != want {
			t.Errorf("guardianActionSummary(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestSubAgentTitle(t *testing.T) {
	cases := map[string]string{
		`{"kind":"started","agentPath":"agents/researcher"}`:    "Start subagent researcher",
		`{"kind":"interacted","agentPath":"agents/researcher"}`: "Interact with subagent researcher",
		`{"kind":"interrupted","agentPath":"researcher/"}`:      "Interrupt subagent researcher",
		`{"kind":"started","agentPath":""}`:                     "Start subagent subagent",
		`{"kind":"other","agentPath":"a/b/c"}`:                  "Subagent c",
	}
	for in, want := range cases {
		var it subAgentItem
		if err := json.Unmarshal([]byte(in), &it); err != nil {
			t.Fatalf("unmarshal %s: %v", in, err)
		}
		if got := subAgentTitle(it); got != want {
			t.Errorf("subAgentTitle(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestSubAgentToolCalls(t *testing.T) {
	raw := json.RawMessage(`{"id":"sa-1","agentThreadId":"t-2","agentPath":"agents/worker","kind":"started"}`)

	start, ok := subAgentStartToolCall(raw, acp.ToolCallStatusInProgress)
	if !ok || start.ToolCall == nil {
		t.Fatalf("start = %#v, ok=%v", start, ok)
	}
	if start.ToolCall.Title != "Start subagent worker" || start.ToolCall.Status != acp.ToolCallStatusInProgress {
		t.Errorf("start tool call = %#v", start.ToolCall)
	}
	if in, _ := start.ToolCall.RawInput.(map[string]any); in["agentThreadId"] != "t-2" || in["activityKind"] != "started" {
		t.Errorf("rawInput = %#v", start.ToolCall.RawInput)
	}

	complete, ok := subAgentCompleteToolCall(raw)
	if !ok || complete.ToolCallUpdate == nil {
		t.Fatalf("complete = %#v, ok=%v", complete, ok)
	}
	if *complete.ToolCallUpdate.Status != acp.ToolCallStatusCompleted {
		t.Errorf("complete status = %#v", complete.ToolCallUpdate.Status)
	}

	if _, ok := subAgentStartToolCall(json.RawMessage(`{"kind":"started"}`), acp.ToolCallStatusInProgress); ok {
		t.Error("missing id should not produce a tool call")
	}
}

func TestMcpRawOutput(t *testing.T) {
	if out := mcpRawOutput([]byte(`null`), []byte(`null`)); out != nil {
		t.Errorf("both null should be nil, got %v", out)
	}
	if out := mcpRawOutput(nil, nil); out != nil {
		t.Errorf("both empty should be nil, got %v", out)
	}
	out := mcpRawOutput([]byte(`{"content":"ok"}`), []byte(`null`))
	if out == nil {
		t.Fatalf("expected non-nil output")
	}
	if _, ok := out["result"]; !ok {
		t.Errorf("missing result key: %v", out)
	}
}
