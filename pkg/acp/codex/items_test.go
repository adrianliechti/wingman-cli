package codex

import (
	"encoding/json"
	"testing"

	"github.com/coder/acp-go-sdk"
)

func TestWebSearchTitle(t *testing.T) {
	cases := map[string]string{
		`{"query":"go generics"}`: "Web search",
		`{"query":""}`:            "Web search",
		`{"query":"x","action":{"type":"search","query":"narrowed"}}`:    "Web search",
		`{"query":"","action":{"type":"search","queries":["a","","b"]}}`: "Web search",
		`{"action":{"type":"openPage","url":"https://x.dev"}}`:           "Open page",
		`{"action":{"type":"findInPage","url":"u","pattern":"p"}}`:       "Find in page",
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

func TestWebSearchInputContainsOnlyVisibleArguments(t *testing.T) {
	cases := []struct {
		raw  string
		want map[string]any
	}{
		{`{"id":"1","query":"go generics"}`, map[string]any{"query": "go generics"}},
		{`{"id":"1","action":{"type":"search","query":"narrowed"}}`, map[string]any{"query": "narrowed"}},
		{`{"id":"1","action":{"type":"openPage","url":"https://x.dev"}}`, map[string]any{"url": "https://x.dev"}},
		{`{"id":"1","action":{"type":"findInPage","url":"u","pattern":"p"}}`, map[string]any{"pattern": "p", "url": "u"}},
	}
	for _, tc := range cases {
		var it webSearchItem
		if err := json.Unmarshal([]byte(tc.raw), &it); err != nil {
			t.Fatalf("unmarshal %s: %v", tc.raw, err)
		}
		got, _ := json.Marshal(webSearchInput(it))
		want, _ := json.Marshal(tc.want)
		if string(got) != string(want) {
			t.Errorf("webSearchInput(%s) = %s, want %s", tc.raw, got, want)
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

func TestCompactionToolCalls(t *testing.T) {
	start := compactionStartToolCall("compact-1")
	if start.ToolCall == nil {
		t.Fatal("missing compaction start")
	}
	if start.ToolCall.Title != "Compact conversation" || start.ToolCall.Kind != acp.ToolKindThink || start.ToolCall.Status != acp.ToolCallStatusInProgress {
		t.Errorf("start = %#v", start.ToolCall)
	}
	if meta, ok := start.ToolCall.Meta["contextCompaction"].(map[string]any); !ok || meta["version"] != 1 {
		t.Errorf("start meta = %#v", start.ToolCall.Meta)
	}

	complete := compactionCompleteToolCall("compact-1")
	if complete.ToolCallUpdate == nil || complete.ToolCallUpdate.Title == nil || *complete.ToolCallUpdate.Title != "Compact conversation" {
		t.Fatalf("complete = %#v", complete.ToolCallUpdate)
	}
	if complete.ToolCallUpdate.Status == nil || *complete.ToolCallUpdate.Status != acp.ToolCallStatusCompleted {
		t.Errorf("complete status = %#v", complete.ToolCallUpdate.Status)
	}
	if meta, ok := complete.ToolCallUpdate.Meta["contextCompaction"].(map[string]any); !ok || meta["version"] != 1 {
		t.Errorf("complete meta = %#v", complete.ToolCallUpdate.Meta)
	}

	history := completedCompactionToolCall("compact-2")
	if history.ToolCall == nil || history.ToolCall.Status != acp.ToolCallStatusCompleted || history.ToolCall.Kind != acp.ToolKindThink {
		t.Errorf("history = %#v", history.ToolCall)
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

func TestImageGenerationUsesSavedPathAsLocation(t *testing.T) {
	start := imageGenStartToolCall("gen-1")
	if start.ToolCall == nil || start.ToolCall.RawInput != nil {
		t.Fatalf("start = %#v", start)
	}

	raw := json.RawMessage(`{"id":"gen-1","status":"completed","revisedPrompt":"a fox","result":"opaque-image-data","savedPath":"/project/fox.png"}`)
	completed, ok := imageGenToolCall(raw)
	if !ok || completed.ToolCall == nil {
		t.Fatalf("completed = %#v, ok=%v", completed, ok)
	}
	if completed.ToolCall.RawOutput != nil {
		t.Fatalf("saved path duplicated as raw output: %#v", completed.ToolCall.RawOutput)
	}
	if len(completed.ToolCall.Locations) != 1 || completed.ToolCall.Locations[0].Path != "/project/fox.png" {
		t.Fatalf("locations = %#v", completed.ToolCall.Locations)
	}
	if completed.ToolCall.RawInput != nil {
		t.Fatalf("location was mirrored into raw input: %#v", completed.ToolCall.RawInput)
	}
	if len(completed.ToolCall.Content) != 2 {
		t.Fatalf("content = %#v", completed.ToolCall.Content)
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
		`{"type":"writeStdin","processId":"proc-1"}`:               "write stdin to process proc-1",
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
		`{"kind":"completed","agentPath":"agents/researcher"}`:  "Complete subagent researcher",
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
	if start.ToolCall.RawInput != nil {
		t.Errorf("subagent metadata should not be rendered as arguments: %#v", start.ToolCall.RawInput)
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

func TestCollabToolCallShowsPromptWithoutInternalState(t *testing.T) {
	raw := json.RawMessage(`{"id":"c-1","tool":"spawn_agent","status":"completed","prompt":"Review tools","senderThreadId":"root","receiverThreadIds":["worker"],"agentsStates":{"worker":"done"}}`)
	update, ok := collabStartToolCall(raw)
	if !ok || update.ToolCall == nil {
		t.Fatalf("update = %#v, ok=%v", update, ok)
	}
	input, _ := update.ToolCall.RawInput.(map[string]any)
	if len(input) != 1 || input["prompt"] != "Review tools" {
		t.Fatalf("raw input = %#v", update.ToolCall.RawInput)
	}

	withoutPrompt, ok := collabStartToolCall(json.RawMessage(`{"id":"c-2","tool":"wait","status":"completed","agentsStates":{"worker":"done"}}`))
	if !ok || withoutPrompt.ToolCall == nil || withoutPrompt.ToolCall.RawInput != nil {
		t.Fatalf("metadata-only input should be omitted: %#v", withoutPrompt)
	}
}

func TestImageViewUsesOneLocationRepresentation(t *testing.T) {
	update, ok := imageViewToolCall(json.RawMessage(`{"id":"img-1","path":"/project/image.png"}`))
	if !ok || update.ToolCall == nil {
		t.Fatalf("update = %#v, ok=%v", update, ok)
	}
	call := update.ToolCall
	if call.Title != "View image" || len(call.Locations) != 1 || call.Locations[0].Path != "/project/image.png" {
		t.Fatalf("tool call = %#v", call)
	}
	if call.RawInput != nil || len(call.Content) != 0 {
		t.Fatalf("path was represented outside the location chip: raw=%#v content=%#v", call.RawInput, call.Content)
	}
}

func TestGuardianUsesFormattedContentOnly(t *testing.T) {
	g := guardianNotif{ReviewID: "review-1"}
	g.Review.Status = "approved"
	g.Action = json.RawMessage(`{"type":"command","command":"go test ./..."}`)
	start := guardianStartToolCall(g)
	if start.ToolCall == nil || start.ToolCall.RawInput != nil || len(start.ToolCall.Content) != 1 {
		t.Fatalf("start = %#v", start)
	}
	update := guardianUpdateToolCall(g)
	if update.ToolCallUpdate == nil || update.ToolCallUpdate.RawOutput != nil || len(update.ToolCallUpdate.Content) != 1 {
		t.Fatalf("update = %#v", update)
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
