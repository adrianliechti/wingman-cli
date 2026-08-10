package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"iter"
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
		"-p", "fix it",
		"--output-format", "JSON",
		"--mode", "PLAN",
		"--model", "gpt-test",
		"--effort", "high",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := scriptOptions{
		Agent: "codex", Prompt: "fix it", OutputFormat: "json", Mode: "plan",
		Model: "gpt-test", Effort: "high",
	}
	if opts != want {
		t.Fatalf("parseScriptArgs() = %#v, want %#v", opts, want)
	}
}

func TestParseScriptArgsDefaults(t *testing.T) {
	opts, err := parseScriptArgs([]string{"-p", "summarize"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Agent != code.BuiltinAgentName || opts.OutputFormat != "text" || opts.Mode != code.UnattendedModeID {
		t.Fatalf("unexpected defaults: %#v", opts)
	}
}

func TestParseScriptArgsRejectsConflictsAndUnknownFormat(t *testing.T) {
	for _, args := range [][]string{
		{"-p", "hello", "--continue", "--resume", "session-1"},
		{"-p", "hello", "--output-format", "yaml"},
	} {
		if _, err := parseScriptArgs(args); err == nil {
			t.Fatalf("parseScriptArgs(%q) succeeded", args)
		}
	}
}

func TestScriptPromptCombinesPipedInput(t *testing.T) {
	got, err := scriptPrompt("explain the failure", strings.NewReader("build failed\n"), true)
	if err != nil {
		t.Fatal(err)
	}
	if got != "build failed\n\nexplain the failure" {
		t.Fatalf("scriptPrompt() = %q", got)
	}
}

func TestScriptPromptRejectsOversizedInput(t *testing.T) {
	input := ioLimitByteReader{remaining: maxScriptStdinBytes + 1}
	if _, err := scriptPrompt("explain", &input, true); err == nil || !strings.Contains(err.Error(), "10 MiB") {
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

func TestExecuteScriptReturnsJSONAndConfiguresSession(t *testing.T) {
	a := &fakeScriptAgent{
		modes: []code.Mode{
			{ID: code.AgentModeID},
			{ID: code.PlanModeID},
			{ID: code.UnattendedModeID},
		},
		currentMode: code.AgentModeID,
	}
	opts := scriptOptions{
		Agent: "wingman", Prompt: "hello", OutputFormat: "json", Mode: code.UnattendedModeID,
		Model: "test-model", Effort: "high",
	}

	var out bytes.Buffer
	if err := executeScript(context.Background(), a, opts, "hello", &out); err != nil {
		t.Fatal(err)
	}

	var got scriptResult
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if got.Type != "result" || got.Result != "hello back" || got.SessionID != "new-session" || got.Agent != "fake" {
		t.Fatalf("unexpected result: %#v", got)
	}
	if got.Usage.InputTokens != 4 || got.Usage.OutputTokens != 2 {
		t.Fatalf("usage = %#v", got.Usage)
	}
	if a.currentMode != code.UnattendedModeID || a.model != "test-model" || a.effort != "high" {
		t.Fatalf("session configuration: mode=%q model=%q effort=%q", a.currentMode, a.model, a.effort)
	}
	if len(a.input) != 1 || a.input[0].Text != "hello" {
		t.Fatalf("input = %#v", a.input)
	}
}

func TestExecuteScriptFailsWhenUnattendedModeIsUnavailable(t *testing.T) {
	a := &fakeScriptAgent{currentMode: code.AgentModeID}
	opts := scriptOptions{Prompt: "hello", OutputFormat: "text", Mode: code.UnattendedModeID}
	err := executeScript(context.Background(), a, opts, "hello", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "does not support session modes") {
		t.Fatalf("executeScript() error = %v", err)
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
func (a *fakeScriptAgent) NewSession(context.Context) (string, error)  { return "new-session", nil }
func (a *fakeScriptAgent) LoadSession(context.Context, string) error   { return nil }
func (a *fakeScriptAgent) DeleteSession(context.Context, string) error { return nil }
func (a *fakeScriptAgent) Messages(string) []agent.Message {
	return agent.CloneMessages(a.messages)
}
func (a *fakeScriptAgent) Usage(string) agent.Usage { return a.usage }
func (a *fakeScriptAgent) Send(_ context.Context, _ string, input []agent.Content) (iter.Seq2[agent.Message, error], error) {
	a.input = agent.CloneContent(input)
	a.messages = append(a.messages, agent.Message{Role: agent.RoleUser, Content: agent.CloneContent(input)})
	return func(yield func(agent.Message, error) bool) {
		message := agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{Text: "hello back"}}}
		if yield(message, nil) {
			a.messages = append(a.messages, message)
			a.usage = agent.Usage{InputTokens: 4, OutputTokens: 2}
		}
	}, nil
}
func (a *fakeScriptAgent) Cancel(string) {}
func (a *fakeScriptAgent) Close() error  { return nil }
