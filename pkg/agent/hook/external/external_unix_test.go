//go:build !windows

package external

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/agent/hook"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func TestLoadCodexHooksJSONShape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")
	data := `{
  "description": "Codex-compatible hooks",
  "hooks": {
    "PreToolUse": [{
      "matcher": "^Bash$",
      "hooks": [{
        "type": "command",
        "command": "python3 /tmp/pre.py",
        "timeout": 10,
        "statusMessage": "checking",
        "additionalContextLimit": 4096
      }]
    }],
    "Stop": [{"hooks": [{"type": "http", "url": "https://example.test/hook"}]}]
  }
}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Description != "Codex-compatible hooks" || cfg.RuleCount() != 2 {
		t.Fatalf("config = %+v", cfg)
	}
	handler := cfg.Hooks.PreToolUse[0].Hooks[0]
	if handler.Type != "command" || handler.Timeout != 10 || handler.AdditionalContextLimit == nil || *handler.AdditionalContextLimit != 4096 {
		t.Fatalf("handler = %+v", handler)
	}
}

func TestLoadRejectsFormerWingmanFlatShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	if err := os.WriteFile(path, []byte(`{"preToolUse":[{"command":"true"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected old format rejection, got %v", err)
	}
}

func TestLoadAcceptsCodexReservedHandlerShapes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	data := `{"hooks":{"PreToolUse":[{"hooks":[
  {"type":"command","command":"true","command_windows":"cmd /c exit 0"},
  {"type":"mcp_tool","server":"policy","tool":"check","input":{"strict":true}},
  {"type":"prompt"},
  {"type":"agent"}
]}]}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RuleCount() != 4 {
		t.Fatalf("rule count = %d, want 4", cfg.RuleCount())
	}
	handlers := cfg.Hooks.PreToolUse[0].Hooks
	if handlers[0].CommandWindowsSnake != "cmd /c exit 0" || handlers[1].Input["strict"] != true {
		t.Fatalf("handlers = %#v", handlers)
	}
}

func TestCodexMatcherSemantics(t *testing.T) {
	tests := []struct {
		matcher string
		input   []string
		want    bool
	}{
		{"", []string{"Bash"}, true},
		{"*", []string{"Bash"}, true},
		{"Edit|Write", []string{"Write"}, true},
		{"Bash", []string{"BashOutput"}, false},
		{"^Bash$", []string{"Bash"}, true},
		{"mcp__memory__.*", []string{"mcp__memory__write"}, true},
		{"^Write$", []string{"Edit", "Write"}, true},
	}
	for _, test := range tests {
		if got := groupMatches(test.matcher, test.input); got != test.want {
			t.Errorf("groupMatches(%q, %q) = %v, want %v", test.matcher, test.input, got, test.want)
		}
	}
}

func TestPreToolUseCodexPayloadAndStructuredDeny(t *testing.T) {
	dir := t.TempDir()
	payloadPath := filepath.Join(dir, "payload.json")
	command := "cat > " + payloadPath + `; printf '%s' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"destructive command"}}'`
	cfg := configFor("PreToolUse", "^Bash$", Handler{Type: "command", Command: command})
	ctx := hook.WithRuntime(context.Background(), hook.Runtime{
		SessionID:      "thread-1",
		TurnID:         "turn-1",
		CWD:            dir,
		Model:          "gpt-test",
		PermissionMode: "default",
	})

	outcome, err := cfg.Build(dir, nil).PreToolUse[0](ctx, tool.ToolCall{ID: "call-1", Name: "shell", Args: `{"command":"rm -rf build"}`})
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Block || outcome.Reason != "destructive command" {
		t.Fatalf("outcome = %+v", outcome)
	}

	payload := readJSONMap(t, payloadPath)
	wantFields := map[string]any{
		"session_id":      "thread-1",
		"turn_id":         "turn-1",
		"cwd":             dir,
		"hook_event_name": "PreToolUse",
		"model":           "gpt-test",
		"permission_mode": "default",
		"tool_name":       "Bash",
		"tool_use_id":     "call-1",
	}
	for key, want := range wantFields {
		if got := payload[key]; got != want {
			t.Errorf("payload[%q] = %#v, want %#v", key, got, want)
		}
	}
	if payload["transcript_path"] != nil {
		t.Fatalf("transcript_path = %#v, want null", payload["transcript_path"])
	}
	input := payload["tool_input"].(map[string]any)
	if input["command"] != "rm -rf build" {
		t.Fatalf("tool_input = %#v", input)
	}
}

func TestPreToolUseCodexRewriteRestoresWingmanExecInput(t *testing.T) {
	command := `printf '%s' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","updatedInput":{"command":"echo rewritten","yield_time_ms":1000}}}'`
	cfg := configFor("PreToolUse", "Bash", Handler{Type: "command", Command: command})
	outcome, err := cfg.Build(t.TempDir(), nil).PreToolUse[0](context.Background(), tool.ToolCall{
		Name: "exec_command",
		Args: `{"cmd":"echo original","yield_time_ms":1000}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	var updated map[string]any
	if err := json.Unmarshal(outcome.UpdatedInput, &updated); err != nil {
		t.Fatal(err)
	}
	if updated["cmd"] != "echo rewritten" || updated["command"] != nil {
		t.Fatalf("updated input = %#v", updated)
	}
}

func TestStructuredOutputRequiresMatchingCodexEventName(t *testing.T) {
	command := `printf '%s' '{"hookSpecificOutput":{"hookEventName":"PostToolUse","permissionDecision":"deny","permissionDecisionReason":"wrong event"}}'`
	cfg := configFor("PreToolUse", "Bash", Handler{Type: "command", Command: command})
	outcome, err := cfg.Build(t.TempDir(), nil).PreToolUse[0](context.Background(), tool.ToolCall{Name: "shell"})
	if err != nil || outcome.Block {
		t.Fatalf("mismatched output was applied: outcome = %+v, err = %v", outcome, err)
	}
}

func TestCodexAsyncCommandHookIsParsedButSkipped(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "done")
	cfg := configFor("PreToolUse", "Bash", Handler{
		Type:    "command",
		Command: "printf done > " + marker,
		Async:   true,
	})
	if _, err := cfg.Build(dir, nil).PreToolUse[0](context.Background(), tool.ToolCall{Name: "shell"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("Codex-compatible async hook unexpectedly ran: %v", err)
	}
}

func TestCodexExitCodeTwoBlocksButOneFailsOpen(t *testing.T) {
	blocked := configFor("PreToolUse", "Bash", Handler{Type: "command", Command: "echo denied >&2; exit 2"})
	outcome, err := blocked.Build(t.TempDir(), nil).PreToolUse[0](context.Background(), tool.ToolCall{Name: "shell"})
	if err != nil || !outcome.Block || outcome.Reason != "denied" {
		t.Fatalf("exit 2 outcome = %+v, err = %v", outcome, err)
	}

	failed := configFor("PreToolUse", "Bash", Handler{Type: "command", Command: "echo ordinary failure >&2; exit 1"})
	outcome, err = failed.Build(t.TempDir(), nil).PreToolUse[0](context.Background(), tool.ToolCall{Name: "shell"})
	if err != nil || outcome.Block {
		t.Fatalf("exit 1 outcome = %+v, err = %v", outcome, err)
	}
}

func TestPermissionRequestDenyWins(t *testing.T) {
	allow := Handler{Type: "command", Command: `printf '%s' '{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}'`}
	deny := Handler{Type: "command", Command: `printf '%s' '{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"deny","message":"policy"}}}'`}
	cfg := &Config{Hooks: Events{PermissionRequest: []MatcherGroup{{Matcher: "Bash", Hooks: []Handler{allow, deny}}}}}
	outcome, err := cfg.Build(t.TempDir(), nil).PermissionRequest[0](context.Background(), tool.ToolCall{Name: "shell"})
	if err != nil || outcome.Behavior != hook.PermissionDeny || outcome.Message != "policy" {
		t.Fatalf("outcome = %+v, err = %v", outcome, err)
	}
}

func TestUserPromptSubmitPlainStdoutIsContext(t *testing.T) {
	cfg := configFor("UserPromptSubmit", "ignored", Handler{Type: "command", Command: "printf 'extra context'"})
	outcome, err := cfg.Build(t.TempDir(), nil).UserPromptSubmit[0](context.Background(), "hello")
	if err != nil || len(outcome.AdditionalContext) != 1 || outcome.AdditionalContext[0] != "extra context" {
		t.Fatalf("outcome = %+v, err = %v", outcome, err)
	}
}

func TestSessionStartMatcherAndPayload(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "payload.json")
	cfg := configFor("SessionStart", "startup|resume", Handler{Type: "command", Command: "cat > " + marker})
	hooks := cfg.Build(dir, nil)
	if _, err := hooks.SessionStart[0](context.Background(), "compact"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("compact unexpectedly matched: %v", err)
	}
	if _, err := hooks.SessionStart[0](context.Background(), "resume"); err != nil {
		t.Fatal(err)
	}
	payload := readJSONMap(t, marker)
	if payload["hook_event_name"] != "SessionStart" || payload["source"] != "resume" {
		t.Fatalf("payload = %#v", payload)
	}
	if _, ok := payload["turn_id"]; ok {
		t.Fatalf("SessionStart payload must not contain turn_id: %#v", payload)
	}
}

func TestBuildOptionsInjectPluginEnvironment(t *testing.T) {
	cfg := configFor("SessionStart", "", Handler{
		Type:    "command",
		Command: `printf '%s|%s|%s|%s' "$PLUGIN_ROOT" "$PLUGIN_DATA" "$CLAUDE_PLUGIN_ROOT" "$CLAUDE_PLUGIN_DATA"`,
	})
	hooks := cfg.BuildWithOptions(t.TempDir(), BuildOptions{Environment: map[string]string{
		"PLUGIN_ROOT":        "/plugins/demo",
		"PLUGIN_DATA":        "/data/demo",
		"CLAUDE_PLUGIN_ROOT": "/plugins/demo",
		"CLAUDE_PLUGIN_DATA": "/data/demo",
	}})
	outcome, err := hooks.SessionStart[0](context.Background(), "startup")
	if err != nil {
		t.Fatal(err)
	}
	want := "/plugins/demo|/data/demo|/plugins/demo|/data/demo"
	if len(outcome.AdditionalContext) != 1 || outcome.AdditionalContext[0] != want {
		t.Fatalf("context = %#v, want %q", outcome.AdditionalContext, want)
	}
}

func configFor(event, matcher string, handler Handler) *Config {
	group := []MatcherGroup{{Matcher: matcher, Hooks: []Handler{handler}}}
	cfg := &Config{}
	switch event {
	case "PreToolUse":
		cfg.Hooks.PreToolUse = group
	case "UserPromptSubmit":
		cfg.Hooks.UserPromptSubmit = group
	case "SessionStart":
		cfg.Hooks.SessionStart = group
	}
	return cfg
}

func readJSONMap(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}
