package main

import (
	"bytes"
	"context"
	"errors"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/adrianliechti/wingman-agent/pkg/model"
)

func TestParseScriptArgs(t *testing.T) {
	opts, err := parseScriptArgs([]string{
		"--agent", "codex",
		"fix it",
		"--json",
		"--debug",
		"--model", "gpt-test",
		"--effort", "high",
		"--schema", "schema.json",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := scriptOptions{
		Agent: "codex", Prompt: "fix it", JSON: true, Debug: true,
		Model: "gpt-test", Effort: "high", Schema: "schema.json",
	}
	if opts != want {
		t.Fatalf("parseScriptArgs() = %#v, want %#v", opts, want)
	}
}

func TestParseScriptArgsDefaults(t *testing.T) {
	opts, err := parseScriptArgs([]string{"summarize"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Agent != code.BuiltinAgentName || opts.JSON {
		t.Fatalf("unexpected defaults: %#v", opts)
	}
}

func TestParseScriptArgsResume(t *testing.T) {
	tests := []struct {
		args      []string
		sessionID string
		prompt    string
	}{
		{[]string{"resume", "session-1", "continue fixing"}, "session-1", "continue fixing"},
		{[]string{"resume", "--last", "review it"}, "latest", "review it"},
		{[]string{"resume", "session-1"}, "session-1", ""},
	}
	for _, tt := range tests {
		opts, err := parseScriptArgs(tt.args)
		if err != nil {
			t.Fatalf("parseScriptArgs(%q): %v", tt.args, err)
		}
		if !opts.Resume || opts.SessionID != tt.sessionID || opts.Prompt != tt.prompt {
			t.Fatalf("parseScriptArgs(%q) = %#v", tt.args, opts)
		}
	}
}

func TestParseScriptArgsRejectsInvalidShapes(t *testing.T) {
	for _, args := range [][]string{
		{"one", "two"},
		{"--last", "hello"},
		{"resume"},
		{"resume", "--last", "--ephemeral"},
		{"hello", "--unknown"},
	} {
		if _, err := parseScriptArgs(args); err == nil {
			t.Fatalf("parseScriptArgs(%q) succeeded", args)
		}
	}
}

func TestScriptPromptCombinesPipedContextAndInstruction(t *testing.T) {
	got, err := scriptPrompt("explain the failure", strings.NewReader("build failed\n"), true, false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "build failed\n\nexplain the failure" {
		t.Fatalf("scriptPrompt() = %q", got)
	}
}

func TestScriptPromptDashReadsFullPrompt(t *testing.T) {
	got, err := scriptPrompt("-", strings.NewReader("do the thing\n"), true, false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "do the thing" {
		t.Fatalf("scriptPrompt() = %q", got)
	}
}

func TestScriptPromptAllowsEmptyResume(t *testing.T) {
	got, err := scriptPrompt("", strings.NewReader(""), false, true)
	if err != nil || got != "Continue." {
		t.Fatalf("scriptPrompt() = %q, %v", got, err)
	}
}

func TestScriptPromptRejectsOversizedInput(t *testing.T) {
	input := ioLimitByteReader{remaining: maxScriptStdinBytes + 1}
	if _, err := scriptPrompt("explain", &input, true, false); err == nil || !strings.Contains(err.Error(), "10 MiB") {
		t.Fatalf("scriptPrompt() error = %v", err)
	}
}

type ioLimitByteReader struct {
	remaining int
}

func (r *ioLimitByteReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, errors.New("unexpected read after limit")
	}
	n := min(len(p), r.remaining)
	for i := range p[:n] {
		p[i] = 'x'
	}
	r.remaining -= n
	return n, nil
}

func TestScriptTextCollectorDiscardsFailedAttempt(t *testing.T) {
	c := &scriptTextCollector{}
	c.Add(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{Text: "discard me"}}})
	c.Reset()
	c.Add(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{Text: "keep"}}})
	c.Commit()
	c.Add(agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{Text: " this"}}})

	if got := c.Text(); got != "keep this" {
		t.Fatalf("Text() = %q, want %q", got, "keep this")
	}
}

func TestScriptReporterShowsToolProgressOnlyInDebugMode(t *testing.T) {
	args := "{\n  \"file_path\": \"pkg/agent/models.go\",\n  \"offset\": 10\n}"
	toolCall := agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{ToolCall: &agent.ToolCall{
		ID: "call-1", Name: "read", Args: args,
	}}}}
	toolResult := agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{ToolResult: &agent.ToolResult{
		ID: "call-1", Name: "read", Args: args, Content: "first line\nsecond line",
	}}}}

	for _, tt := range []struct {
		debug bool
		want  string
	}{
		{debug: false, want: ""},
		{debug: true, want: "→ read /pkg/agent/models.go\n" +
			"  {\n" +
			"    \"file_path\": \"pkg/agent/models.go\",\n" +
			"    \"offset\": 10\n" +
			"  }\n" +
			"\n" +
			"✓ read /pkg/agent/models.go\n" +
			"  first line\n" +
			"  second line\n" +
			"\n"},
	} {
		var errOut bytes.Buffer
		reporter := newScriptReporter(tt.debug, &errOut)
		reporter.add(toolCall)
		reporter.commit()
		reporter.add(toolResult)
		if errOut.String() != tt.want {
			t.Fatalf("debug=%v stderr=%q, want %q", tt.debug, errOut.String(), tt.want)
		}
	}
}

func TestFinalAssistantTextUsesLatestTurn(t *testing.T) {
	messages := []agent.Message{
		{Role: agent.RoleAssistant, Content: []agent.Content{{Text: "old"}}},
		{Role: agent.RoleUser, Content: []agent.Content{{Text: "new question"}}},
		{Role: agent.RoleAssistant, Content: []agent.Content{{ToolResult: &agent.ToolResult{Name: "read"}}}},
		{Role: agent.RoleAssistant, Content: []agent.Content{{Text: "new"}, {Text: " answer"}}},
	}
	if got := finalAssistantText(messages); got != "new answer" {
		t.Fatalf("finalAssistantText() = %q", got)
	}

	if got := finalAssistantText(messages[:3]); got != "" {
		t.Fatalf("finalAssistantText() crossed the latest user boundary: %q", got)
	}
}

func TestExecuteScriptReturnsFinalJSONAndConfiguresSession(t *testing.T) {
	a := newFakeScriptAgent()
	a.response = `{"message":"hello back"}`
	opts := scriptOptions{
		Agent: "wingman", Prompt: "hello", JSON: true,
		Model: "test-model", Effort: "high",
	}

	var out, errOut bytes.Buffer
	if err := executeScript(context.Background(), a, opts, "hello", &out, &errOut); err != nil {
		t.Fatal(err)
	}

	if out.String() != "{\"message\":\"hello back\"}\n" {
		t.Fatalf("stdout = %q", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("JSON mode wrote stderr: %q", errOut.String())
	}
	if a.currentMode != code.UnattendedModeID || a.model != "test-model" || a.effort != "high" {
		t.Fatalf("session configuration: mode=%q model=%q effort=%q", a.currentMode, a.model, a.effort)
	}
	if a.sendCount != 2 || a.jsonCalls != 1 || len(a.inputs[0]) != 1 || a.inputs[0][0].Text != "hello" {
		t.Fatalf("sendCount=%d jsonCalls=%d inputs=%#v", a.sendCount, a.jsonCalls, a.inputs)
	}
}

func TestExecuteScriptWritesOnlyFinalOutputForSimpleTurn(t *testing.T) {
	a := newFakeScriptAgent()
	var out, errOut bytes.Buffer
	opts := scriptOptions{Prompt: "hello"}
	if err := executeScript(context.Background(), a, opts, "hello", &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if out.String() != "hello back\n" {
		t.Fatalf("stdout = %q", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q", errOut.String())
	}
	if a.saved != "new-session" {
		t.Fatalf("saved session = %q", a.saved)
	}
}

func TestExecuteScriptDeletesEphemeralSession(t *testing.T) {
	a := newFakeScriptAgent()
	opts := scriptOptions{Prompt: "hello", Ephemeral: true}
	if err := executeScript(context.Background(), a, opts, "hello", &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if a.deleted != "new-session" || a.saved != "" {
		t.Fatalf("deleted=%q saved=%q", a.deleted, a.saved)
	}
}

func TestExecuteScriptValidatesOutputSchema(t *testing.T) {
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.json")
	schema := `{
		"type":"object",
		"properties":{"name":{"type":"string"}},
		"required":["name"],
		"additionalProperties":false
	}`
	if err := os.WriteFile(schemaPath, []byte(schema), 0644); err != nil {
		t.Fatal(err)
	}

	a := newFakeScriptAgent()
	a.response = `{"name":"wingman"}`
	opts := scriptOptions{Prompt: "metadata", JSON: true, Schema: schemaPath}
	var out bytes.Buffer
	if err := executeScript(context.Background(), a, opts, "metadata", &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "{\"name\":\"wingman\"}\n" {
		t.Fatalf("stdout = %q", out.String())
	}
	if a.sendCount != 2 || a.schemaCalls != 1 || a.jsonCalls != 0 {
		t.Fatalf("sendCount=%d schemaCalls=%d jsonCalls=%d", a.sendCount, a.schemaCalls, a.jsonCalls)
	}
	if len(a.inputs) != 2 || len(a.inputs[0]) != 1 || len(a.inputs[1]) != 2 || !a.inputs[1][1].Hidden || !strings.Contains(a.inputs[1][1].Text, "<output-schema>") {
		t.Fatalf("schema finalization inputs = %#v", a.inputs)
	}
}

func TestExecuteScriptRejectsOutputThatDoesNotMatchSchema(t *testing.T) {
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(schemaPath, []byte(`{"type":"object","required":["name"]}`), 0644); err != nil {
		t.Fatal(err)
	}

	a := newFakeScriptAgent()
	a.response = `{"other":true}`
	opts := scriptOptions{Prompt: "metadata", Schema: schemaPath}
	var out bytes.Buffer
	err := executeScript(context.Background(), a, opts, "metadata", &out, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "does not match output schema") {
		t.Fatalf("executeScript() error = %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty on validation failure", out.String())
	}
}

func TestLoadScriptOutputSchemaRejectsInvalidSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema.json")
	if err := os.WriteFile(path, []byte(`{"$ref":"#/$defs/missing"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadScriptOutputSchema(path, ""); err == nil {
		t.Fatal("loadScriptOutputSchema() accepted an invalid schema")
	}
}

func TestExecuteScriptFailsWhenModeIsUnavailable(t *testing.T) {
	a := &fakeScriptAgent{currentMode: code.AgentModeID}
	opts := scriptOptions{Prompt: "hello"}
	err := executeScript(context.Background(), a, opts, "hello", &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "does not support session modes") {
		t.Fatalf("executeScript() error = %v", err)
	}
}

func newFakeScriptAgent() *fakeScriptAgent {
	return &fakeScriptAgent{
		modes: []code.Mode{
			{ID: code.AgentModeID},
			{ID: code.PlanModeID},
			{ID: code.UnattendedModeID},
		},
		currentMode: code.AgentModeID,
	}
}

type fakeScriptAgent struct {
	messages    []agent.Message
	input       []agent.Content
	modes       []code.Mode
	currentMode string
	model       string
	effort      string
	usage       agent.Usage
	loaded      string
	deleted     string
	response    string
	sendCount   int
	schemaCalls int
	jsonCalls   int
	inputs      [][]agent.Content
	saved       string
}

func (a *fakeScriptAgent) Name() string                          { return "fake" }
func (a *fakeScriptAgent) Workspace() *code.Workspace            { return nil }
func (a *fakeScriptAgent) Models(string) ([]model.Model, string) { return nil, a.model }
func (a *fakeScriptAgent) SetModel(_ context.Context, _ string, id string) error {
	a.model = id
	return nil
}
func (a *fakeScriptAgent) Effort(string) (string, []string) { return a.effort, nil }
func (a *fakeScriptAgent) SetEffort(_ context.Context, _, value string) error {
	a.effort = value
	return nil
}
func (a *fakeScriptAgent) Modes(string) ([]code.Mode, string) {
	return append([]code.Mode(nil), a.modes...), a.currentMode
}
func (a *fakeScriptAgent) SetMode(_ context.Context, _, mode string) error {
	a.currentMode = mode
	return nil
}
func (a *fakeScriptAgent) ListSessions(context.Context) ([]code.SessionInfo, error) {
	return []code.SessionInfo{{ID: "old"}, {ID: "latest", UpdatedAt: time.Now()}}, nil
}
func (a *fakeScriptAgent) NewSession(context.Context) (string, error) { return "new-session", nil }
func (a *fakeScriptAgent) LoadSession(_ context.Context, id string) error {
	a.loaded = id
	return nil
}
func (a *fakeScriptAgent) DeleteSession(_ context.Context, id string) error {
	a.deleted = id
	return nil
}
func (a *fakeScriptAgent) Save(id string) error {
	a.saved = id
	return nil
}
func (a *fakeScriptAgent) Messages(string) []agent.Message {
	return agent.CloneMessages(a.messages)
}
func (a *fakeScriptAgent) Usage(string) agent.Usage { return a.usage }
func (a *fakeScriptAgent) Send(ctx context.Context, _ string, input []agent.Content) (iter.Seq2[agent.Message, error], error) {
	a.sendCount++
	if schema, ok := agent.OutputSchemaFromContext(ctx); ok {
		a.schemaCalls++
		if len(schema) == 0 {
			a.jsonCalls++
		}
	}
	a.input = agent.CloneContent(input)
	a.inputs = append(a.inputs, agent.CloneContent(input))
	a.messages = append(a.messages, agent.Message{Role: agent.RoleUser, Content: agent.CloneContent(input)})
	return func(yield func(agent.Message, error) bool) {
		response := a.response
		if response == "" {
			response = "hello back"
		}
		message := agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{Text: response}}}
		if yield(message, nil) {
			a.messages = append(a.messages, message)
			a.usage = agent.Usage{InputTokens: 4, OutputTokens: 2}
		}
	}, nil
}
func (a *fakeScriptAgent) Cancel(string) {}
func (a *fakeScriptAgent) Close() error  { return nil }
