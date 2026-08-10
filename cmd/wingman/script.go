package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/adrianliechti/wingman-agent/pkg/code/agents"
	"github.com/adrianliechti/wingman-agent/pkg/skill"
)

const maxScriptStdinBytes = 10 << 20

type scriptOptions struct {
	Agent        string
	Prompt       string
	SessionID    string
	OutputFormat string
	Mode         string
	Model        string
	Effort       string
}

type scriptResult struct {
	Type      string      `json:"type"`
	Result    string      `json:"result"`
	SessionID string      `json:"session_id"`
	Agent     string      `json:"agent"`
	Usage     agent.Usage `json:"usage"`
}

func hasPrintFlag(args []string) bool {
	return slices.ContainsFunc(args, func(arg string) bool {
		name, _, _ := strings.Cut(arg, "=")
		return name == "--print" || name == "-p"
	})
}

func parseScriptArgs(args []string) (scriptOptions, error) {
	opts := scriptOptions{
		Agent:        code.BuiltinAgentName,
		OutputFormat: "text",
		Mode:         code.UnattendedModeID,
	}
	var latest bool

	fs := newFlags("wingman -p")
	fs.String(&opts.Prompt, "--print, -p PROMPT", "run a prompt non-interactively")
	fs.String(&opts.Agent, "--agent, -a NAME", "use wingman or any detected/configured agent")
	fs.Bool(&latest, "--continue, -c", "resume the agent's latest session")
	fs.String(&opts.SessionID, "--resume, -r ID", "resume the specified session")
	fs.String(&opts.OutputFormat, "--output-format FORMAT", "output text or json")
	fs.String(&opts.Mode, "--mode MODE", "use unattended, plan, or agent mode")
	fs.String(&opts.Model, "--model MODEL", "override the model")
	fs.String(&opts.Effort, "--effort LEVEL", "override the reasoning effort")

	if err := fs.Parse(args); err != nil {
		return scriptOptions{}, err
	}
	if !hasPrintFlag(args) {
		return scriptOptions{}, errors.New("script mode requires --print or -p")
	}
	if latest && opts.SessionID != "" {
		return scriptOptions{}, errors.New("--continue and --resume cannot be used together")
	}
	if latest {
		opts.SessionID = "latest"
	}

	opts.Agent = strings.TrimSpace(opts.Agent)
	opts.OutputFormat = strings.ToLower(strings.TrimSpace(opts.OutputFormat))
	opts.Mode = strings.ToLower(strings.TrimSpace(opts.Mode))

	if opts.Agent == "" {
		return scriptOptions{}, errors.New("agent name cannot be empty")
	}
	if opts.OutputFormat != "text" && opts.OutputFormat != "json" {
		return scriptOptions{}, fmt.Errorf("unknown output format %q (expected text or json)", opts.OutputFormat)
	}
	if opts.Mode == "" {
		return scriptOptions{}, errors.New("mode cannot be empty")
	}

	return opts, nil
}

func runScript(ctx context.Context, args []string) error {
	opts, err := parseScriptArgs(args)
	if err != nil {
		return err
	}

	readStdin := false
	if info, statErr := os.Stdin.Stat(); statErr == nil {
		readStdin = info.Mode()&os.ModeCharDevice == 0
	}
	prompt, err := scriptPrompt(opts.Prompt, os.Stdin, readStdin)
	if err != nil {
		return err
	}

	wd, err := os.Getwd()
	if err != nil {
		return err
	}
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

	return executeScript(ctx, a, opts, prompt, os.Stdout)
}

func scriptPrompt(prompt string, stdin io.Reader, readStdin bool) (string, error) {
	if !readStdin {
		if prompt == "" {
			return "", errors.New("prompt is empty")
		}
		return prompt, nil
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
	switch {
	case piped == "" && prompt == "":
		return "", errors.New("prompt and stdin are empty")
	case piped == "":
		return prompt, nil
	case prompt == "":
		return piped, nil
	default:
		return piped + "\n\n" + prompt, nil
	}
}

func executeScript(ctx context.Context, a code.Agent, opts scriptOptions, prompt string, out io.Writer) error {
	sessionID := opts.SessionID
	if sessionID == "latest" {
		sessions, err := a.ListSessions(ctx)
		if err != nil {
			return fmt.Errorf("list sessions: %w", err)
		}
		sessionID = latestSessionID(sessions)
		if sessionID == "" {
			return errors.New("no previous session to continue")
		}
	}

	if sessionID != "" {
		if err := a.LoadSession(ctx, sessionID); err != nil {
			return fmt.Errorf("load session %s: %w", sessionID, err)
		}
	} else {
		var err error
		sessionID, err = a.NewSession(ctx)
		if err != nil {
			return fmt.Errorf("create session: %w", err)
		}
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
	if err := setScriptMode(ctx, a, sessionID, opts.Mode); err != nil {
		return err
	}

	input := []agent.Content{{Text: prompt}}
	if ws := a.Workspace(); ws != nil {
		for _, invocation := range skill.Invocations(opts.Prompt, ws.Skills) {
			block, err := invocation.Instructions(ws.RootPath)
			if err != nil {
				return fmt.Errorf("load skill %q: %w", invocation.Skill.Name, err)
			}
			input = append(input, agent.Content{Text: block, Hidden: true})
		}
	}

	usageBefore := a.Usage(sessionID)
	collector := &scriptTextCollector{}
	streamCtx := agent.WithStreamEventHandlers(code.WithSessionID(ctx, sessionID), agent.StreamEventHandlers{
		Reset:  collector.Reset,
		Commit: collector.Commit,
	})
	stream, err := a.Send(streamCtx, sessionID, input)
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
		collector.Add(message)
	}

	result := finalAssistantText(a.Messages(sessionID))
	if result == "" {
		result = collector.Text()
	}
	response := scriptResult{
		Type:      "result",
		Result:    result,
		SessionID: sessionID,
		Agent:     a.Name(),
		Usage:     usageDelta(usageBefore, a.Usage(sessionID)),
	}
	return writeScriptResult(out, opts.OutputFormat, response)
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
			return fmt.Errorf("agent %q does not support session modes required for script mode", a.Name())
		}
		return fmt.Errorf("agent %q does not support mode %q (available: %s)", a.Name(), wanted, strings.Join(ids, ", "))
	}
	if err := a.SetMode(ctx, sessionID, wanted); err != nil {
		return fmt.Errorf("set mode %q: %w", wanted, err)
	}
	return nil
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

func usageDelta(before, after agent.Usage) agent.Usage {
	return agent.Usage{
		InputTokens:     max(0, after.InputTokens-before.InputTokens),
		CachedTokens:    max(0, after.CachedTokens-before.CachedTokens),
		OutputTokens:    max(0, after.OutputTokens-before.OutputTokens),
		LastInputTokens: after.LastInputTokens,
		ContextWindow:   after.ContextWindow,
	}
}

func writeScriptResult(w io.Writer, format string, result scriptResult) error {
	if format == "json" {
		return json.NewEncoder(w).Encode(result)
	}
	if result.Result == "" {
		return nil
	}
	if _, err := io.WriteString(w, result.Result); err != nil {
		return err
	}
	if !strings.HasSuffix(result.Result, "\n") {
		_, err := io.WriteString(w, "\n")
		return err
	}
	return nil
}
