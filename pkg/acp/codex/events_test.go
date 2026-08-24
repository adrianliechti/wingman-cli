package codex

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/coder/acp-go-sdk"
)

func TestFileChangeContentAddUnifiedDiff(t *testing.T) {
	raw := []byte(`{"changes":[{"path":"/p/NewFile.kt","kind":{"type":"add"},"diff":"--- /dev/null\n+++ /p/NewFile.kt\n@@ -0,0 +1,3 @@\n+package test\n+\n+class NewFile {}"}]}`)
	content := fileChangeContent(raw)
	if len(content) != 1 || content[0].Diff == nil {
		t.Fatalf("expected one diff block, got %#v", content)
	}
	d := content[0].Diff
	if d.Path != "/p/NewFile.kt" {
		t.Errorf("path = %q", d.Path)
	}
	if d.OldText != nil {
		t.Errorf("add should have nil oldText, got %q", *d.OldText)
	}
	if want := "package test\n\nclass NewFile {}"; d.NewText != want {
		t.Errorf("newText = %q, want %q", d.NewText, want)
	}
}

func TestMessageUpdatesCarryIDsAndPhase(t *testing.T) {
	user := userMessageUpdate(acp.TextBlock("hello"), "user-id")
	if user.UserMessageChunk == nil || user.UserMessageChunk.MessageId == nil || *user.UserMessageChunk.MessageId != "user-id" {
		t.Fatalf("user update = %#v", user)
	}

	agent := agentMessageUpdate("answer", "agent-id", "final_answer")
	if agent.AgentMessageChunk == nil || agent.AgentMessageChunk.MessageId == nil || *agent.AgentMessageChunk.MessageId != "agent-id" {
		t.Fatalf("agent update = %#v", agent)
	}
	b, err := json.Marshal(agent.AgentMessageChunk.Meta)
	if err != nil || string(b) != `{"codex":{"phase":"final_answer"}}` {
		t.Fatalf("agent meta = %s, err=%v", b, err)
	}

	thought := agentThoughtUpdate("thinking", "thought-id")
	if thought.AgentThoughtChunk == nil || thought.AgentThoughtChunk.MessageId == nil || *thought.AgentThoughtChunk.MessageId != "thought-id" {
		t.Fatalf("thought update = %#v", thought)
	}
}

func TestTurnCompletedFlushesThrottledPlanTail(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := newEventDispatcher(ctx, nil, "session-1")
	d.planUpdates = true

	d.handle("item/plan/delta", json.RawMessage(`{"itemId":"plan-1","delta":"first"}`))
	d.handle("item/plan/delta", json.RawMessage(`{"itemId":"plan-1","delta":" second"}`))
	if got := d.planEmitted["plan-1"]; got != "first" {
		t.Fatalf("plan before completion = %q, want first throttled snapshot", got)
	}

	d.handle("turn/completed", json.RawMessage(`{"threadId":"session-1","turn":{"id":"turn-1","status":"completed"}}`))
	if got := d.planEmitted["plan-1"]; got != "first second" {
		t.Fatalf("plan after completion = %q, want flushed snapshot", got)
	}
}

func TestElicitationParamsPreserveSchemaAndURLFields(t *testing.T) {
	var p elicitationParams
	err := json.Unmarshal([]byte(`{"threadId":"t","serverName":"mcp","mode":"form","message":"Choose","requestedSchema":{"type":"object","properties":{"answer":{"type":"string"}}},"url":"https://example.com","elicitationId":"e-1","_meta":{"persist":"session"}}`), &p)
	if err != nil {
		t.Fatal(err)
	}
	if p.URL != "https://example.com" || p.ElicitationID != "e-1" || p.Meta["persist"] != "session" {
		t.Fatalf("params = %#v", p)
	}
	var schema acp.UnstableElicitationSchema
	if err := json.Unmarshal(p.RequestedSchema, &schema); err != nil || schema.Properties["answer"] == nil {
		t.Fatalf("schema = %#v, err=%v", schema, err)
	}
}

func TestFileChangeContentAddRawContent(t *testing.T) {

	raw := []byte(`{"changes":[{"path":"/p/Raw.kt","kind":{"type":"add"},"diff":"fun main() {}\n"}]}`)
	content := fileChangeContent(raw)
	if len(content) != 1 || content[0].Diff == nil {
		t.Fatalf("expected one diff block, got %#v", content)
	}
	if got := content[0].Diff.NewText; got != "fun main() {}\n" {
		t.Errorf("newText = %q", got)
	}
	if content[0].Diff.OldText != nil {
		t.Errorf("add should have nil oldText")
	}
}

func TestFileChangeContentUpdate(t *testing.T) {
	raw := []byte(`{"changes":[{"path":"/p/a.go","kind":{"type":"update"},"diff":"--- a/p/a.go\n+++ b/p/a.go\n@@ -1,3 +1,3 @@\n line one\n-old line\n+new line\n line three"}]}`)
	content := fileChangeContent(raw)
	if len(content) != 1 || content[0].Diff == nil {
		t.Fatalf("expected one diff block, got %#v", content)
	}
	d := content[0].Diff
	if d.OldText == nil || *d.OldText != "line one\nold line\nline three" {
		t.Errorf("oldText = %v", d.OldText)
	}
	if d.NewText != "line one\nnew line\nline three" {
		t.Errorf("newText = %q", d.NewText)
	}
}

func TestFileChangeLocationsAreDistinct(t *testing.T) {
	raw := json.RawMessage(`{"changes":[{"path":"/p/a.go"},{"path":"/p/a.go"},{"path":"/p/b.go"},{"path":""}]}`)
	locations := fileChangeLocations(raw)
	if len(locations) != 2 || locations[0].Path != "/p/a.go" || locations[1].Path != "/p/b.go" {
		t.Fatalf("locations = %#v", locations)
	}
}

func TestCommandActionToolCall(t *testing.T) {
	title, kind, input, locs, ok := commandActionToolCall([]commandAction{{Type: "read", Path: "/x"}})
	if !ok || title != "Read file" || kind != acp.ToolKindRead || input != nil || len(locs) != 1 || locs[0].Path != "/x" {
		t.Errorf("read: got %q %v %v %v", title, kind, locs, ok)
	}

	title, kind, input, locs, ok = commandActionToolCall([]commandAction{{Type: "search", Query: "foo", Path: "/p"}})
	query, _ := input.(map[string]any)["query"].(string)
	if !ok || title != "Search files" || kind != acp.ToolKindSearch || query != "foo" || len(locs) != 1 || locs[0].Path != "/p" {
		t.Errorf("search: got %q %v %v %v", title, kind, locs, ok)
	}

	title, kind, input, locs, ok = commandActionToolCall([]commandAction{{Type: "listFiles", Path: "/p"}})
	if !ok || title != "List files" || kind != acp.ToolKindRead || len(locs) != 1 || locs[0].Path != "/p" {
		t.Errorf("listFiles: got %q %v %v %v", title, kind, locs, ok)
	}
	if title, _, _, _, _ := commandActionToolCall([]commandAction{{Type: "listFiles"}}); title != "List files" {
		t.Errorf("listFiles no path: got %q", title)
	}

	if _, _, _, _, ok := commandActionToolCall([]commandAction{{Type: "read", Path: "/x"}, {Type: "read", Path: "/y"}}); ok {
		t.Errorf("multiple actions should not resolve to a single mapping")
	}
	if _, _, _, _, ok := commandActionToolCall([]commandAction{{Type: "unknown"}}); ok {
		t.Errorf("unknown action should not resolve")
	}
}

func TestDisplayLocationsDoNotBecomeRawInput(t *testing.T) {
	opts := appendDisplayLocations(nil, []acp.ToolCallLocation{{Path: "/project/main.go"}})
	update := acp.StartToolCall("read-1", "Read file", opts...)
	if update.ToolCall == nil {
		t.Fatal("missing tool call")
	}
	if len(update.ToolCall.Locations) != 1 || update.ToolCall.Locations[0].Path != "/project/main.go" {
		t.Fatalf("locations = %#v", update.ToolCall.Locations)
	}
	if update.ToolCall.RawInput != nil {
		t.Fatalf("raw input duplicated the location: %#v", update.ToolCall.RawInput)
	}
}

func TestCommandRawInputOmitsEmptyWorkingDirectory(t *testing.T) {
	input := commandRawInput("go test ./...", "")
	if input["command"] != "go test ./..." {
		t.Fatalf("command = %#v", input["command"])
	}
	if _, ok := input["cwd"]; ok {
		t.Fatalf("empty cwd should be omitted: %#v", input)
	}
}

func TestStripShellPrefix(t *testing.T) {
	cases := map[string]string{
		"/bin/zsh -c npm install":                 "npm install",
		"/bin/bash -lc npm install":               "npm install",
		"zsh npm install":                         "npm install",
		"sh -c ls -la":                            "ls -la",
		"npm install":                             "npm install",
		"/bin/bash -lc './tests.cmd -D=v'":        "./tests.cmd -D=v",
		"/bin/zsh -c 'echo hello'":                "echo hello",
		`"/bin/zsh -lc sed -n '1,20p' README.md"`: `sed -n '1,20p' README.md`,
	}
	for in, want := range cases {
		if got := stripShellPrefix(in); got != want {
			t.Errorf("stripShellPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFileChangeContentDeleteMeta(t *testing.T) {
	raw := []byte(`{"changes":[{"path":"/p/gone.go","kind":{"type":"delete"},"diff":"--- a/p/gone.go\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-line one\n-line two"}]}`)
	content := fileChangeContent(raw)
	if len(content) != 1 || content[0].Diff == nil {
		t.Fatalf("expected one diff block, got %#v", content)
	}
	d := content[0].Diff
	if d.Meta["kind"] != "delete" {
		t.Errorf("meta kind = %v, want delete", d.Meta["kind"])
	}
	if d.NewText != "" {
		t.Errorf("delete newText = %q, want empty", d.NewText)
	}
	if d.OldText == nil || *d.OldText != "line one\nline two" {
		t.Errorf("delete oldText = %v", d.OldText)
	}
}

func TestIsFatalTurnError(t *testing.T) {
	cases := []struct {
		name string
		info string
		want bool
	}{
		{"unauthorized string", `"unauthorized"`, true},
		{"usage limit string", `"usageLimitExceeded"`, true},
		{"other string", `"somethingElse"`, false},
		{"http 401 object", `{"httpConnectionFailed":{"httpStatusCode":401}}`, true},
		{"stream disconnected 401", `{"responseStreamDisconnected":{"httpStatusCode":401}}`, true},
		{"http 500 object", `{"httpConnectionFailed":{"httpStatusCode":500}}`, false},
		{"empty", ``, false},
		{"null", `null`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isFatalTurnError([]byte(c.info)); got != c.want {
				t.Errorf("isFatalTurnError(%s) = %v, want %v", c.info, got, c.want)
			}
		})
	}
}

func TestTokenUsageComponentsMatchTotal(t *testing.T) {
	d := newEventDispatcher(context.Background(), nil, "session")
	d.handleTokenUsage([]byte(`{"tokenUsage":{"last":{"totalTokens":20,"inputTokens":15,"cachedInputTokens":5,"outputTokens":4,"reasoningOutputTokens":1}}}`))
	u := d.getUsage()
	if u == nil || u.CachedReadTokens == nil || u.ThoughtTokens == nil {
		t.Fatalf("usage = %#v", u)
	}
	if got := u.InputTokens + *u.CachedReadTokens + u.OutputTokens + *u.ThoughtTokens; got != u.TotalTokens {
		t.Fatalf("component sum = %d, total = %d", got, u.TotalTokens)
	}
}
