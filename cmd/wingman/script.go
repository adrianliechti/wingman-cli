package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/adrianliechti/wingman-agent/pkg/code/agents"
	"github.com/adrianliechti/wingman-agent/pkg/skill"
	"github.com/google/jsonschema-go/jsonschema"
)

const maxScriptStdinBytes = 10 << 20

type scriptOptions struct {
	Agent     string
	Prompt    string
	SessionID string
	Model     string
	Effort    string
	WorkDir   string
	Schema    string
	JSON      bool
	Debug     bool
	Ephemeral bool
	Resume    bool
}

func defaultScriptOptions() scriptOptions {
	return scriptOptions{Agent: code.BuiltinAgentName}
}

// parseScriptArgs parses the `wingman exec` command. Its shape intentionally
// follows other agent CLIs: the prompt is positional and resume is a
// subcommand rather than a pair of unrelated flags.
func parseScriptArgs(args []string) (scriptOptions, error) {
	opts := defaultScriptOptions()
	if len(args) > 0 && args[0] == "resume" {
		opts.Resume = true
		args = args[1:]
	}

	var latest bool
	fs := newFlags("wingman exec")
	fs.String(&opts.Agent, "--agent, -a NAME", "use wingman or any detected/configured agent")
	fs.Bool(&latest, "--last", "resume the latest session")
	fs.Bool(&opts.JSON, "--json", "return the final response as a JSON object")
	fs.Bool(&opts.Debug, "--debug", "print reasoning and tool details to stderr")
	fs.Bool(&opts.Ephemeral, "--ephemeral", "delete the session after the run")
	fs.String(&opts.Schema, "--schema PATH", "require the final response to match a JSON Schema")
	fs.String(&opts.WorkDir, "--cd, -C PATH", "set the workspace root")
	fs.String(&opts.Model, "--model, -m MODEL", "override the model")
	fs.String(&opts.Effort, "--effort LEVEL", "override the reasoning effort")

	positional, err := fs.ParseArgs(args)
	if err != nil {
		return scriptOptions{}, err
	}
	if opts.Resume {
		if opts.Ephemeral {
			return scriptOptions{}, errors.New("--ephemeral cannot be used with exec resume")
		}
		if latest {
			opts.SessionID = "latest"
			if len(positional) > 1 {
				return scriptOptions{}, errors.New("exec resume --last accepts at most one prompt")
			}
			if len(positional) == 1 {
				opts.Prompt = positional[0]
			}
		} else {
			if len(positional) == 0 {
				return scriptOptions{}, errors.New("exec resume requires a session ID or --last")
			}
			if len(positional) > 2 {
				return scriptOptions{}, errors.New("exec resume accepts a session ID and at most one prompt")
			}
			opts.SessionID = positional[0]
			if len(positional) == 2 {
				opts.Prompt = positional[1]
			}
		}
	} else {
		if latest {
			return scriptOptions{}, errors.New("--last can only be used with exec resume")
		}
		if len(positional) > 1 {
			return scriptOptions{}, errors.New("exec accepts at most one prompt argument")
		}
		if len(positional) == 1 {
			opts.Prompt = positional[0]
		}
	}

	return validateScriptOptions(opts)
}

func validateScriptOptions(opts scriptOptions) (scriptOptions, error) {
	opts.Agent = strings.TrimSpace(opts.Agent)
	if opts.Agent == "" {
		return scriptOptions{}, errors.New("agent name cannot be empty")
	}
	return opts, nil
}

func runExec(ctx context.Context, args []string) error {
	opts, err := parseScriptArgs(args)
	if err != nil {
		return err
	}
	return runScript(ctx, opts)
}

func runScript(ctx context.Context, opts scriptOptions) error {
	readStdin := false
	if info, statErr := os.Stdin.Stat(); statErr == nil {
		readStdin = info.Mode()&os.ModeCharDevice == 0
	}
	prompt, err := scriptPrompt(opts.Prompt, os.Stdin, readStdin, opts.Resume)
	if err != nil {
		return err
	}
	if opts.Prompt == "" || opts.Prompt == "-" {
		opts.Prompt = prompt
	}

	wd := opts.WorkDir
	if wd == "" {
		wd, err = os.Getwd()
	} else {
		wd, err = filepath.Abs(wd)
	}
	if err != nil {
		return err
	}
	opts.WorkDir = wd
	ws, err := code.NewWorkspace(wd)
	if err != nil {
		return err
	}
	defer ws.Close()

	a, err := agents.New(ctx, ws, opts.Agent, nil)
	if err != nil {
		return err
	}
	defer a.Close()

	return executeScript(ctx, a, opts, prompt, os.Stdout, os.Stderr)
}

func scriptPrompt(prompt string, stdin io.Reader, readStdin, allowEmpty bool) (string, error) {
	if !readStdin {
		switch {
		case prompt == "-":
			return "", errors.New("cannot read prompt from stdin: stdin is a terminal")
		case prompt == "" && allowEmpty:
			return "Continue.", nil
		case prompt == "":
			return "", errors.New("prompt is empty")
		default:
			return prompt, nil
		}
	}

	limited := io.LimitReader(stdin, maxScriptStdinBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	if len(data) > maxScriptStdinBytes {
		return "", fmt.Errorf("stdin exceeds the %d MiB limit", maxScriptStdinBytes>>20)
	}

	piped := strings.TrimRight(string(data), "\r\n")
	if prompt == "-" {
		prompt = ""
	}
	switch {
	case piped == "" && prompt == "" && allowEmpty:
		return "Continue.", nil
	case piped == "" && prompt == "":
		return "", errors.New("prompt and stdin are empty")
	case piped == "":
		return prompt, nil
	case prompt == "":
		return piped, nil
	default:
		// Piped data is context; the positional prompt remains the final
		// instruction, matching normal shell pipeline usage.
		return piped + "\n\n" + prompt, nil
	}
}

func executeScript(ctx context.Context, a code.Agent, opts scriptOptions, prompt string, out, errOut io.Writer) (retErr error) {
	outputSchema, err := loadScriptOutputSchema(opts.Schema, opts.WorkDir)
	if err != nil {
		return err
	}

	sessionID := opts.SessionID
	if sessionID == "latest" {
		sessions, err := a.ListSessions(ctx)
		if err != nil {
			return fmt.Errorf("list sessions: %w", err)
		}
		sessionID = latestSessionID(sessions)
		if sessionID == "" {
			return errors.New("no previous session to resume")
		}
	}

	created := sessionID == ""
	if created {
		var err error
		sessionID, err = a.NewSession(ctx)
		if err != nil {
			return fmt.Errorf("create session: %w", err)
		}
	} else if err := a.LoadSession(ctx, sessionID); err != nil {
		return fmt.Errorf("load session %s: %w", sessionID, err)
	}
	if created && opts.Ephemeral {
		defer func() {
			if err := a.DeleteSession(context.WithoutCancel(ctx), sessionID); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("delete ephemeral session: %w", err))
			}
		}()
	} else if saver, ok := a.(sessionSaver); ok {
		defer func() {
			if err := saver.Save(sessionID); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("save session: %w", err))
			}
		}()
	}

	if opts.Model != "" {
		if err := a.SetModel(ctx, sessionID, opts.Model); err != nil {
			return fmt.Errorf("set model %q: %w", opts.Model, err)
		}
	}
	if opts.Effort != "" {
		if err := a.SetEffort(ctx, sessionID, opts.Effort); err != nil {
			return fmt.Errorf("set effort %q: %w", opts.Effort, err)
		}
	}
	if err := setScriptMode(ctx, a, sessionID, code.UnattendedModeID); err != nil {
		return err
	}

	input := []agent.Content{{Text: prompt}}
	if ws := a.Workspace(); ws != nil {
		for _, invocation := range skill.Invocations(opts.Prompt, ws.Skills()) {
			block, err := invocation.Instructions(ws.RootPath)
			if err != nil {
				return fmt.Errorf("load skill %q: %w", invocation.Skill.Name, err)
			}
			input = append(input, agent.Content{Text: block, Hidden: true})
		}
	}

	reporter := newScriptReporter(opts.Debug, errOut)
	send := func(turnInput []agent.Content, schema map[string]any) error {
		streamCtx := code.WithSessionID(ctx, sessionID)
		if schema != nil {
			streamCtx = agent.WithOutputSchema(streamCtx, schema)
		}
		streamCtx = agent.WithStreamEventHandlers(streamCtx, agent.StreamEventHandlers{
			Reset:  reporter.reset,
			Commit: reporter.commit,
		})
		stream, err := a.Send(streamCtx, sessionID, turnInput)
		if err != nil {
			return fmt.Errorf("send prompt: %w", err)
		}
		if stream == nil {
			return errors.New("agent returned a nil turn stream")
		}
		for message, streamErr := range stream {
			if streamErr != nil {
				return streamErr
			}
			reporter.add(message)
			if reporter.err != nil {
				return reporter.err
			}
		}
		return nil
	}
	if err := send(input, nil); err != nil {
		return reporter.fail(err)
	}
	if opts.JSON || outputSchema != nil {
		finalize := []agent.Content{
			{Text: "Using the completed work from the previous turn, return the final result now."},
		}
		if outputSchema != nil {
			finalize = append(finalize, agent.Content{Text: outputSchema.instructions(), Hidden: true})
		} else {
			finalize = append(finalize, agent.Content{Text: jsonOutputInstructions(), Hidden: true})
		}
		schema := map[string]any{}
		if outputSchema != nil {
			schema = outputSchema.raw
		}
		if err := send(finalize, schema); err != nil {
			return reporter.fail(err)
		}
	}

	result := finalAssistantText(a.Messages(sessionID))
	if result == "" {
		result = reporter.collector.Text()
	}
	if outputSchema != nil {
		if err := outputSchema.validate(result); err != nil {
			return reporter.fail(err)
		}
	} else if opts.JSON {
		if err := validateJSONOutput(result); err != nil {
			return reporter.fail(err)
		}
	}
	if err := reporter.finish(); err != nil {
		return err
	}
	return writeScriptText(out, result)
}

type scriptOutputSchema struct {
	raw      map[string]any
	resolved *jsonschema.Resolved
	text     string
}

// sessionSaver is optional because the built-in agent exposes explicit local
// persistence, while external agents save through their own transports.
type sessionSaver interface {
	Save(string) error
}

func loadScriptOutputSchema(path, workDir string) (*scriptOutputSchema, error) {
	if path == "" {
		return nil, nil
	}
	if !filepath.IsAbs(path) && workDir != "" {
		path = filepath.Join(workDir, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read output schema: %w", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse output schema: %w", err)
	}
	if raw == nil {
		return nil, errors.New("parse output schema: root must be a JSON object")
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("parse output schema: %w", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return nil, fmt.Errorf("resolve output schema: %w", err)
	}
	compact := &bytes.Buffer{}
	if err := json.Compact(compact, data); err != nil {
		return nil, fmt.Errorf("compact output schema: %w", err)
	}
	return &scriptOutputSchema{raw: raw, resolved: resolved, text: compact.String()}, nil
}

func (s *scriptOutputSchema) instructions() string {
	return "<output-schema>\nReturn only JSON matching this schema as the final response. " +
		"Do not wrap the JSON in a Markdown code fence.\n" + s.text + "\n</output-schema>"
}

func (s *scriptOutputSchema) validate(result string) error {
	var value any
	if err := json.Unmarshal([]byte(result), &value); err != nil {
		return fmt.Errorf("final response is not valid JSON: %w", err)
	}
	if err := s.resolved.Validate(value); err != nil {
		return fmt.Errorf("final response does not match output schema: %w", err)
	}
	return nil
}

func jsonOutputInstructions() string {
	return "<output-format>\nReturn only one valid JSON object as the final response. " +
		"Choose concise, meaningful field names and do not wrap the JSON in a Markdown code fence.\n</output-format>"
}

func validateJSONOutput(result string) error {
	var value map[string]any
	if err := json.Unmarshal([]byte(result), &value); err != nil {
		return fmt.Errorf("final response is not a valid JSON object: %w", err)
	}
	if value == nil {
		return errors.New("final response is not a valid JSON object")
	}
	return nil
}

func setScriptMode(ctx context.Context, a code.Agent, sessionID, wanted string) error {
	available, current := a.Modes(sessionID)
	if current == wanted {
		return nil
	}
	if !slices.ContainsFunc(available, func(mode code.Mode) bool { return mode.ID == wanted }) {
		ids := make([]string, 0, len(available))
		for _, mode := range available {
			ids = append(ids, mode.ID)
		}
		if len(ids) == 0 {
			return fmt.Errorf("agent %q does not support session modes required for non-interactive runs", a.Name())
		}
		return fmt.Errorf("agent %q does not support mode %q (available: %s)", a.Name(), wanted, strings.Join(ids, ", "))
	}
	if err := a.SetMode(ctx, sessionID, wanted); err != nil {
		return fmt.Errorf("set mode %q: %w", wanted, err)
	}
	return nil
}

type scriptReporter struct {
	debug     bool
	errOut    io.Writer
	collector scriptTextCollector
	pending   map[string]scriptToolCall
	order     []string
	started   map[string]bool
	reasoning strings.Builder
	nextID    int
	err       error
}

type scriptToolCall struct {
	name    string
	args    string
	partial bool
}

func newScriptReporter(debug bool, errOut io.Writer) *scriptReporter {
	return &scriptReporter{
		debug:   debug,
		errOut:  errOut,
		pending: map[string]scriptToolCall{},
		started: map[string]bool{},
	}
}

func (r *scriptReporter) add(message agent.Message) {
	r.collector.Add(message)
	for _, content := range message.Content {
		if content.Hidden {
			continue
		}
		if content.Reasoning != nil {
			r.reasoning.WriteString(content.Reasoning.Summary)
		}
		if content.ToolCall != nil {
			call := content.ToolCall
			id := call.ID
			if id == "" {
				id = r.itemID("tool")
			}
			if _, present := r.pending[id]; !present {
				r.pending[id] = scriptToolCall{name: call.Name, args: call.Args}
				r.order = append(r.order, id)
			}
			pending := r.pending[id]
			if call.Name != "" {
				pending.name = call.Name
			}
			if call.Args != "" {
				pending.args = call.Args
			}
			pending.partial = call.Partial
			r.pending[id] = pending
		}
		if content.ToolResult != nil {
			r.flushProgress()
			result := content.ToolResult
			if r.debug {
				r.writeToolResult(result)
			}
		}
	}
}

func (r *scriptReporter) reset() {
	r.collector.Reset()
	r.pending = map[string]scriptToolCall{}
	r.order = nil
	r.reasoning.Reset()
}

func (r *scriptReporter) commit() {
	r.collector.Commit()
	r.flushProgress()
}

func (r *scriptReporter) flushProgress() {
	if summary := strings.TrimSpace(r.reasoning.String()); summary != "" {
		if r.debug {
			_, err := fmt.Fprintln(r.errOut, summary)
			r.setErr(err)
		}
	}
	r.reasoning.Reset()
	for _, id := range r.order {
		call, present := r.pending[id]
		if !present || call.partial || r.started[id] {
			continue
		}
		r.started[id] = true
		if r.debug {
			_, err := fmt.Fprintf(r.errOut, "→ %s\n", scriptToolLabel(call.name, call.args))
			r.setErr(err)
			r.writeToolArgs(call.args)
			_, err = fmt.Fprintln(r.errOut)
			r.setErr(err)
		}
	}
	r.pending = map[string]scriptToolCall{}
	r.order = nil
}

func (r *scriptReporter) writeToolArgs(args string) {
	r.writeIndented(args)
}

func (r *scriptReporter) writeToolResult(result *agent.ToolResult) {
	_, err := fmt.Fprintf(r.errOut, "✓ %s\n", scriptToolLabel(result.Name, result.Args))
	r.setErr(err)
	r.writeIndented(result.Content)
	_, err = fmt.Fprintln(r.errOut)
	r.setErr(err)
}

func (r *scriptReporter) writeIndented(text string) {
	text = strings.TrimRight(text, "\r\n")
	if strings.TrimSpace(text) == "" {
		return
	}
	for line := range strings.SplitSeq(text, "\n") {
		_, err := fmt.Fprintf(r.errOut, "  %s\n", strings.TrimSuffix(line, "\r"))
		r.setErr(err)
	}
}

func scriptToolLabel(name, args string) string {
	hint := tool.ExtractHint(args, name)
	if hint == "" {
		return name
	}
	return name + " " + hint
}

func (r *scriptReporter) finish() error {
	r.commit()
	return r.err
}

func (r *scriptReporter) fail(err error) error {
	return errors.Join(err, r.err)
}

func (r *scriptReporter) setErr(err error) {
	if r.err == nil && err != nil {
		r.err = err
	}
}

func (r *scriptReporter) itemID(prefix string) string {
	r.nextID++
	return fmt.Sprintf("%s_%d", prefix, r.nextID)
}

type scriptTextCollector struct {
	accepted strings.Builder
	current  strings.Builder
}

func (c *scriptTextCollector) Add(message agent.Message) {
	if message.Role != agent.RoleAssistant {
		return
	}
	for _, content := range message.Content {
		if !content.Hidden {
			c.current.WriteString(content.Text)
		}
	}
}

func (c *scriptTextCollector) Reset() {
	c.current.Reset()
}

func (c *scriptTextCollector) Commit() {
	c.accepted.WriteString(c.current.String())
	c.current.Reset()
}

func (c *scriptTextCollector) Text() string {
	return c.accepted.String() + c.current.String()
}

func finalAssistantText(messages []agent.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if message.Role == agent.RoleUser && !message.Hidden {
			return ""
		}
		if message.Role != agent.RoleAssistant || message.Hidden {
			continue
		}
		var result strings.Builder
		for _, content := range message.Content {
			if !content.Hidden {
				result.WriteString(content.Text)
			}
		}
		if result.Len() > 0 {
			return result.String()
		}
	}
	return ""
}

func writeScriptText(w io.Writer, result string) error {
	if result == "" {
		return nil
	}
	if _, err := io.WriteString(w, result); err != nil {
		return err
	}
	if !strings.HasSuffix(result, "\n") {
		_, err := io.WriteString(w, "\n")
		return err
	}
	return nil
}
