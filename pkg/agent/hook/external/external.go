package external

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/hook"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/shell"
)

const (
	defaultTimeout    = 600
	sessionEndTimeout = 1
	maxHookOutput     = 16 * 1024
)

// Gate defers a workspace trust decision until the first matching hook fires.
type Gate struct {
	Confirm func(ctx context.Context, message string) (bool, error)
	Message string

	mu      sync.Mutex
	decided bool
	allowed bool
}

func (g *Gate) Allowed(ctx context.Context) bool {
	if g == nil {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.decided {
		return g.allowed
	}
	g.decided = true
	if g.Confirm == nil {
		g.allowed = true
		return true
	}
	ok, err := g.Confirm(ctx, g.Message)
	g.allowed = ok && err == nil
	return g.allowed
}

// Config is the Codex hooks.json shape. HTTP is a Wingman extension whose
// request and successful response bodies use the same wire schema as command
// hooks.
type Config struct {
	Description string `json:"description,omitempty"`
	Hooks       Events `json:"hooks"`
}

type Events struct {
	PreToolUse        []MatcherGroup `json:"PreToolUse,omitempty"`
	PermissionRequest []MatcherGroup `json:"PermissionRequest,omitempty"`
	PostToolUse       []MatcherGroup `json:"PostToolUse,omitempty"`
	PreCompact        []MatcherGroup `json:"PreCompact,omitempty"`
	PostCompact       []MatcherGroup `json:"PostCompact,omitempty"`
	SessionStart      []MatcherGroup `json:"SessionStart,omitempty"`
	SessionEnd        []MatcherGroup `json:"SessionEnd,omitempty"`
	UserPromptSubmit  []MatcherGroup `json:"UserPromptSubmit,omitempty"`
	SubagentStart     []MatcherGroup `json:"SubagentStart,omitempty"`
	SubagentStop      []MatcherGroup `json:"SubagentStop,omitempty"`
	Stop              []MatcherGroup `json:"Stop,omitempty"`
}

type MatcherGroup struct {
	Matcher string    `json:"matcher,omitempty"`
	Hooks   []Handler `json:"hooks"`
}

type Handler struct {
	Type                   string            `json:"type"`
	Command                string            `json:"command,omitempty"`
	CommandWindows         string            `json:"commandWindows,omitempty"`
	CommandWindowsSnake    string            `json:"command_windows,omitempty"`
	Server                 string            `json:"server,omitempty"`
	Tool                   string            `json:"tool,omitempty"`
	Input                  map[string]any    `json:"input,omitempty"`
	URL                    string            `json:"url,omitempty"`
	Headers                map[string]string `json:"headers,omitempty"`
	AllowedEnvVars         []string          `json:"allowedEnvVars,omitempty"`
	Timeout                int               `json:"timeout,omitempty"`
	Async                  bool              `json:"async,omitempty"`
	StatusMessage          string            `json:"statusMessage,omitempty"`
	AdditionalContextLimit *int              `json:"additionalContextLimit,omitempty"`
}

func (c *Config) RuleCount() int {
	total := 0
	for _, groups := range c.allEvents() {
		for _, group := range groups.groups {
			total += len(group.Hooks)
		}
	}
	return total
}

func Load(paths ...string) (*Config, error) {
	cfg := &Config{}
	var errs []error
	for _, path := range paths {
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, fmt.Errorf("read %s: %w", path, err))
			}
			continue
		}
		var parsed Config
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&parsed); err != nil {
			errs = append(errs, fmt.Errorf("parse %s: %w", path, err))
			continue
		}
		if err := parsed.validate(); err != nil {
			errs = append(errs, fmt.Errorf("parse %s: %w", path, err))
			continue
		}
		cfg.merge(parsed)
	}
	return cfg, errors.Join(errs...)
}

func (c *Config) validate() error {
	for _, event := range c.allEvents() {
		for groupIndex, group := range event.groups {
			if !validMatcher(group.Matcher) {
				return fmt.Errorf("%s matcher group %d has invalid regex %q", event.name, groupIndex, group.Matcher)
			}
			for handlerIndex, handler := range group.Hooks {
				switch handler.Type {
				case "command":
					if handler.Command == "" {
						return fmt.Errorf("%s handler %d:%d has no command", event.name, groupIndex, handlerIndex)
					}
				case "http":
					if handler.URL == "" {
						return fmt.Errorf("%s handler %d:%d has no url", event.name, groupIndex, handlerIndex)
					}
				case "mcp_tool":
					if handler.Server == "" || handler.Tool == "" {
						return fmt.Errorf("%s handler %d:%d has an incomplete mcp_tool target", event.name, groupIndex, handlerIndex)
					}
				case "prompt", "agent":
					// Codex accepts these reserved handler shapes but does not run
					// them yet. Keep parsing compatible and skip them below.
				default:
					return fmt.Errorf("%s handler %d:%d has unsupported type %q", event.name, groupIndex, handlerIndex, handler.Type)
				}
			}
		}
	}
	return nil
}

func (c *Config) merge(other Config) {
	if c.Description == "" {
		c.Description = other.Description
	}
	c.Hooks.PreToolUse = append(c.Hooks.PreToolUse, other.Hooks.PreToolUse...)
	c.Hooks.PermissionRequest = append(c.Hooks.PermissionRequest, other.Hooks.PermissionRequest...)
	c.Hooks.PostToolUse = append(c.Hooks.PostToolUse, other.Hooks.PostToolUse...)
	c.Hooks.PreCompact = append(c.Hooks.PreCompact, other.Hooks.PreCompact...)
	c.Hooks.PostCompact = append(c.Hooks.PostCompact, other.Hooks.PostCompact...)
	c.Hooks.SessionStart = append(c.Hooks.SessionStart, other.Hooks.SessionStart...)
	c.Hooks.SessionEnd = append(c.Hooks.SessionEnd, other.Hooks.SessionEnd...)
	c.Hooks.UserPromptSubmit = append(c.Hooks.UserPromptSubmit, other.Hooks.UserPromptSubmit...)
	c.Hooks.SubagentStart = append(c.Hooks.SubagentStart, other.Hooks.SubagentStart...)
	c.Hooks.SubagentStop = append(c.Hooks.SubagentStop, other.Hooks.SubagentStop...)
	c.Hooks.Stop = append(c.Hooks.Stop, other.Hooks.Stop...)
}

type namedGroups struct {
	name   string
	groups []MatcherGroup
}

func (c *Config) allEvents() []namedGroups {
	return []namedGroups{
		{"PreToolUse", c.Hooks.PreToolUse},
		{"PermissionRequest", c.Hooks.PermissionRequest},
		{"PostToolUse", c.Hooks.PostToolUse},
		{"PreCompact", c.Hooks.PreCompact},
		{"PostCompact", c.Hooks.PostCompact},
		{"SessionStart", c.Hooks.SessionStart},
		{"SessionEnd", c.Hooks.SessionEnd},
		{"UserPromptSubmit", c.Hooks.UserPromptSubmit},
		{"SubagentStart", c.Hooks.SubagentStart},
		{"SubagentStop", c.Hooks.SubagentStop},
		{"Stop", c.Hooks.Stop},
	}
}

// Build compiles all matching handlers for each event into one native hook so
// handlers within a Codex event can execute concurrently and aggregate their
// decisions before the agent continues.
func (c *Config) Build(workDir string, gate *Gate) hook.Hooks {
	var built hook.Hooks
	if len(c.Hooks.PreToolUse) > 0 {
		built.PreToolUse = append(built.PreToolUse, c.preToolUse(workDir, gate))
	}
	if len(c.Hooks.PermissionRequest) > 0 {
		built.PermissionRequest = append(built.PermissionRequest, c.permissionRequest(workDir, gate))
	}
	if len(c.Hooks.PostToolUse) > 0 {
		built.PostToolUse = append(built.PostToolUse, c.postToolUse(workDir, gate))
	}
	if len(c.Hooks.UserPromptSubmit) > 0 {
		built.UserPromptSubmit = append(built.UserPromptSubmit, c.userPromptSubmit(workDir, gate))
	}
	if len(c.Hooks.SessionStart) > 0 {
		built.SessionStart = append(built.SessionStart, c.sessionStart(workDir, gate))
	}
	if len(c.Hooks.SessionEnd) > 0 {
		built.SessionEnd = append(built.SessionEnd, c.sessionEnd(workDir, gate))
	}
	if len(c.Hooks.SubagentStart) > 0 {
		built.SubagentStart = append(built.SubagentStart, c.subagentStart(workDir, gate))
	}
	if len(c.Hooks.SubagentStop) > 0 {
		built.SubagentStop = append(built.SubagentStop, c.subagentStop(workDir, gate))
	}
	if len(c.Hooks.PreCompact) > 0 {
		built.PreCompact = append(built.PreCompact, c.preCompact(workDir, gate))
	}
	if len(c.Hooks.PostCompact) > 0 {
		built.PostCompact = append(built.PostCompact, c.postCompact(workDir, gate))
	}
	if len(c.Hooks.Stop) > 0 {
		built.Stop = append(built.Stop, c.stop(workDir, gate))
	}
	return built
}

type selectedHandler struct {
	configuredOrder int
	handler         Handler
}

type runResult struct {
	configuredOrder int
	completionOrder int
	exitCode        int
	stdout          string
	stderr          string
	err             error
}

func runEvent(ctx context.Context, workDir string, gate *Gate, event string, groups []MatcherGroup, matcherInputs []string, payload map[string]any) []runResult {
	var selected []selectedHandler
	order := 0
	for _, group := range groups {
		matcherIgnored := event == "UserPromptSubmit" || event == "Stop"
		if !matcherIgnored && !groupMatches(group.Matcher, matcherInputs) {
			order += len(group.Hooks)
			continue
		}
		for _, handler := range group.Hooks {
			if handler.Type != "command" && handler.Type != "http" {
				order++
				continue
			}
			if !handler.Async {
				selected = append(selected, selectedHandler{configuredOrder: order, handler: handler})
			}
			order++
		}
	}
	if len(selected) == 0 || !gate.Allowed(ctx) {
		return nil
	}
	input, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	ch := make(chan runResult, len(selected))
	for _, selected := range selected {
		go func() {
			result := selected.handler.run(ctx, workDir, event, input)
			result.configuredOrder = selected.configuredOrder
			ch <- result
		}()
	}
	results := make([]runResult, 0, len(selected))
	for completionOrder := range len(selected) {
		result := <-ch
		result.completionOrder = completionOrder
		results = append(results, result)
	}
	return results
}

func (h Handler) run(ctx context.Context, workDir, event string, input []byte) runResult {
	timeoutSeconds := h.Timeout
	if timeoutSeconds <= 0 {
		timeoutSeconds = defaultTimeout
		if event == "SessionEnd" {
			timeoutSeconds = sessionEndTimeout
		}
	}
	if event == "SessionEnd" && timeoutSeconds > 3 {
		timeoutSeconds = 3
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	if h.Type == "http" {
		return h.post(ctx, input)
	}
	command := h.Command
	if runtime.GOOS == "windows" {
		if h.CommandWindows != "" {
			command = h.CommandWindows
		} else if h.CommandWindowsSnake != "" {
			command = h.CommandWindowsSnake
		}
	}
	cmd := shell.Command(ctx, command, workDir)
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	return runResult{exitCode: exitCode, stdout: limit(stdout.String()), stderr: limit(stderr.String()), err: nonExitError(err)}
}

func (h Handler) post(ctx context.Context, input []byte) runResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(input))
	if err != nil {
		return runResult{exitCode: -1, err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	allowed := make(map[string]bool, len(h.AllowedEnvVars))
	for _, name := range h.AllowedEnvVars {
		allowed[name] = true
	}
	for name, value := range h.Headers {
		req.Header.Set(name, os.Expand(value, func(key string) string {
			if allowed[key] {
				return os.Getenv(key)
			}
			return ""
		}))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return runResult{exitCode: -1, err: err}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxHookOutput+1))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return runResult{exitCode: -1, stdout: limit(string(body)), err: fmt.Errorf("hook endpoint returned %s", resp.Status)}
	}
	return runResult{exitCode: 0, stdout: limit(string(body))}
}

func nonExitError(err error) error {
	if err == nil {
		return nil
	}
	var exitError interface{ ExitCode() int }
	if errors.As(err, &exitError) {
		return nil
	}
	return err
}

func limit(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxHookOutput {
		return value
	}
	return value[:maxHookOutput] + "\n[hook output truncated]"
}

func validMatcher(matcher string) bool {
	if matcher == "" || matcher == "*" || exactMatcher(matcher) {
		return true
	}
	_, err := regexp.Compile(matcher)
	return err == nil
}

func groupMatches(matcher string, inputs []string) bool {
	if matcher == "" || matcher == "*" {
		return true
	}
	if exactMatcher(matcher) {
		for _, candidate := range strings.Split(matcher, "|") {
			for _, input := range inputs {
				if candidate == input {
					return true
				}
			}
		}
		return false
	}
	re, err := regexp.Compile(matcher)
	if err != nil {
		return false
	}
	for _, input := range inputs {
		if re.MatchString(input) {
			return true
		}
	}
	return false
}

func exactMatcher(matcher string) bool {
	for _, char := range matcher {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '_' && char != '|' {
			return false
		}
	}
	return true
}

type wireOutput struct {
	Continue           *bool              `json:"continue,omitempty"`
	StopReason         string             `json:"stopReason,omitempty"`
	SuppressOutput     bool               `json:"suppressOutput,omitempty"`
	SystemMessage      string             `json:"systemMessage,omitempty"`
	Decision           string             `json:"decision,omitempty"`
	Reason             string             `json:"reason,omitempty"`
	HookSpecificOutput hookSpecificOutput `json:"hookSpecificOutput,omitempty"`
}

type hookSpecificOutput struct {
	HookEventName            string              `json:"hookEventName"`
	PermissionDecision       string              `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string              `json:"permissionDecisionReason,omitempty"`
	UpdatedInput             json.RawMessage     `json:"updatedInput,omitempty"`
	AdditionalContext        string              `json:"additionalContext,omitempty"`
	Decision                 *permissionDecision `json:"decision,omitempty"`
}

type permissionDecision struct {
	Behavior string `json:"behavior"`
	Message  string `json:"message,omitempty"`
}

type parsedResult struct {
	hook.Outcome
	updatedInput json.RawMessage
	permission   hook.PermissionRequestOutcome
}

func parseResult(event string, result runResult) parsedResult {
	if result.err != nil {
		return parsedResult{}
	}
	if result.exitCode == 2 {
		reason := strings.TrimSpace(result.stderr)
		if reason == "" {
			return parsedResult{}
		}
		switch event {
		case "PreToolUse", "PostToolUse", "UserPromptSubmit", "SubagentStop", "Stop":
			return parsedResult{Outcome: hook.Outcome{Block: true, Reason: reason}}
		case "PermissionRequest":
			return parsedResult{permission: hook.PermissionRequestOutcome{Behavior: hook.PermissionDeny, Message: reason}}
		}
		return parsedResult{}
	}
	if result.exitCode != 0 || result.stdout == "" {
		return parsedResult{}
	}
	var output wireOutput
	decoder := json.NewDecoder(strings.NewReader(result.stdout))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&output); err != nil {
		if event == "SessionStart" || event == "SubagentStart" || event == "UserPromptSubmit" {
			if !strings.HasPrefix(strings.TrimSpace(result.stdout), "{") && !strings.HasPrefix(strings.TrimSpace(result.stdout), "[") {
				return parsedResult{Outcome: hook.Outcome{AdditionalContext: []string{result.stdout}}}
			}
		}
		return parsedResult{}
	}
	specific := output.HookSpecificOutput
	hasSpecific := specific.HookEventName != "" ||
		specific.PermissionDecision != "" ||
		specific.PermissionDecisionReason != "" ||
		len(specific.UpdatedInput) > 0 ||
		specific.AdditionalContext != "" ||
		specific.Decision != nil
	if hasSpecific && specific.HookEventName != event {
		return parsedResult{}
	}
	parsed := parsedResult{}
	if output.Continue != nil && !*output.Continue {
		switch event {
		case "PreToolUse", "PermissionRequest", "SubagentStart":
		default:
			parsed.Stop = true
			parsed.Reason = strings.TrimSpace(output.StopReason)
		}
	}
	if context := strings.TrimSpace(output.HookSpecificOutput.AdditionalContext); context != "" {
		parsed.AdditionalContext = append(parsed.AdditionalContext, context)
	}
	switch event {
	case "PreToolUse":
		if output.HookSpecificOutput.PermissionDecision == "deny" {
			parsed.Block = true
			parsed.Reason = strings.TrimSpace(output.HookSpecificOutput.PermissionDecisionReason)
		} else if output.Decision == "block" {
			parsed.Block = true
			parsed.Reason = strings.TrimSpace(output.Reason)
		} else if output.HookSpecificOutput.PermissionDecision == "allow" && len(output.HookSpecificOutput.UpdatedInput) > 0 {
			parsed.updatedInput = output.HookSpecificOutput.UpdatedInput
		}
	case "PermissionRequest":
		if decision := output.HookSpecificOutput.Decision; decision != nil {
			switch decision.Behavior {
			case "allow":
				parsed.permission.Behavior = hook.PermissionAllow
			case "deny":
				parsed.permission = hook.PermissionRequestOutcome{Behavior: hook.PermissionDeny, Message: strings.TrimSpace(decision.Message)}
			}
		}
	case "PostToolUse", "UserPromptSubmit", "SubagentStop", "Stop":
		if output.Decision == "block" && strings.TrimSpace(output.Reason) != "" {
			parsed.Block = true
			parsed.Reason = strings.TrimSpace(output.Reason)
		}
	}
	return parsed
}

func aggregate(event string, results []runResult) parsedResult {
	var combined parsedResult
	latestRewrite := -1
	for _, result := range results {
		parsed := parseResult(event, result)
		combined.AdditionalContext = append(combined.AdditionalContext, parsed.AdditionalContext...)
		if parsed.Stop && !combined.Stop {
			combined.Stop = true
			combined.Reason = parsed.Reason
		}
		if parsed.Block && !combined.Block {
			combined.Block = true
			combined.Reason = parsed.Reason
		}
		if len(parsed.updatedInput) > 0 && result.completionOrder > latestRewrite {
			latestRewrite = result.completionOrder
			combined.updatedInput = parsed.updatedInput
		}
		if parsed.permission.Behavior == hook.PermissionDeny {
			combined.permission = parsed.permission
		} else if parsed.permission.Behavior == hook.PermissionAllow && combined.permission.Behavior == hook.PermissionUndecided {
			combined.permission = parsed.permission
		}
	}
	if combined.Block {
		combined.updatedInput = nil
	}
	return combined
}

func commonPayload(ctx context.Context, event, workDir string) map[string]any {
	runtime := hook.RuntimeFromContext(ctx)
	if runtime.CWD == "" {
		runtime.CWD = workDir
	}
	transcript := any(nil)
	if runtime.TranscriptPath != "" {
		transcript = runtime.TranscriptPath
	}
	return map[string]any{
		"session_id":      runtime.SessionID,
		"turn_id":         runtime.TurnID,
		"transcript_path": transcript,
		"cwd":             runtime.CWD,
		"hook_event_name": event,
		"model":           runtime.Model,
		"permission_mode": runtime.PermissionMode,
	}
}

func addSubagent(payload map[string]any, runtime hook.Runtime) {
	if runtime.AgentID != "" {
		payload["agent_id"] = runtime.AgentID
		payload["agent_type"] = runtime.AgentType
	}
}

func toolWire(call tool.ToolCall) (string, map[string]any, []string) {
	input := map[string]any{}
	_ = json.Unmarshal([]byte(call.Args), &input)
	name := call.Name
	aliases := []string{call.Name}
	switch call.Name {
	case "shell":
		name = "Bash"
	case "exec_command":
		name = "Bash"
		if command, ok := input["cmd"]; ok {
			input["command"] = command
			delete(input, "cmd")
		}
	case "write":
		name = "Write"
	case "edit":
		name = "Edit"
	case "agent":
		name = "Agent"
	}
	aliases = append(aliases, name)
	return name, input, aliases
}

func restoreToolInput(call tool.ToolCall, updated json.RawMessage) json.RawMessage {
	if call.Name != "exec_command" {
		return updated
	}
	var input map[string]any
	if json.Unmarshal(updated, &input) != nil {
		return updated
	}
	if command, ok := input["command"]; ok {
		input["cmd"] = command
		delete(input, "command")
	}
	restored, _ := json.Marshal(input)
	return restored
}

func (c *Config) preToolUse(workDir string, gate *Gate) hook.PreToolUse {
	return func(ctx context.Context, call tool.ToolCall) (hook.PreToolUseOutcome, error) {
		name, input, aliases := toolWire(call)
		payload := commonPayload(ctx, "PreToolUse", workDir)
		addSubagent(payload, hook.RuntimeFromContext(ctx))
		payload["tool_name"], payload["tool_input"], payload["tool_use_id"] = name, input, call.ID
		parsed := aggregate("PreToolUse", runEvent(ctx, workDir, gate, "PreToolUse", c.Hooks.PreToolUse, aliases, payload))
		return hook.PreToolUseOutcome{Outcome: parsed.Outcome, UpdatedInput: restoreToolInput(call, parsed.updatedInput)}, nil
	}
}

func (c *Config) permissionRequest(workDir string, gate *Gate) hook.PermissionRequest {
	return func(ctx context.Context, call tool.ToolCall) (hook.PermissionRequestOutcome, error) {
		name, input, aliases := toolWire(call)
		payload := commonPayload(ctx, "PermissionRequest", workDir)
		addSubagent(payload, hook.RuntimeFromContext(ctx))
		payload["tool_name"], payload["tool_input"] = name, input
		return aggregate("PermissionRequest", runEvent(ctx, workDir, gate, "PermissionRequest", c.Hooks.PermissionRequest, aliases, payload)).permission, nil
	}
}

func (c *Config) postToolUse(workDir string, gate *Gate) hook.PostToolUse {
	return func(ctx context.Context, call tool.ToolCall, result string) (hook.Outcome, error) {
		name, input, aliases := toolWire(call)
		payload := commonPayload(ctx, "PostToolUse", workDir)
		addSubagent(payload, hook.RuntimeFromContext(ctx))
		payload["tool_name"], payload["tool_input"], payload["tool_use_id"], payload["tool_response"] = name, input, call.ID, result
		return aggregate("PostToolUse", runEvent(ctx, workDir, gate, "PostToolUse", c.Hooks.PostToolUse, aliases, payload)).Outcome, nil
	}
}

func (c *Config) userPromptSubmit(workDir string, gate *Gate) hook.UserPromptSubmit {
	return func(ctx context.Context, prompt string) (hook.Outcome, error) {
		payload := commonPayload(ctx, "UserPromptSubmit", workDir)
		addSubagent(payload, hook.RuntimeFromContext(ctx))
		payload["prompt"] = prompt
		return aggregate("UserPromptSubmit", runEvent(ctx, workDir, gate, "UserPromptSubmit", c.Hooks.UserPromptSubmit, nil, payload)).Outcome, nil
	}
}

func (c *Config) sessionStart(workDir string, gate *Gate) hook.SessionStart {
	return func(ctx context.Context, source string) (hook.Outcome, error) {
		payload := commonPayload(ctx, "SessionStart", workDir)
		delete(payload, "turn_id")
		payload["source"] = source
		return aggregate("SessionStart", runEvent(ctx, workDir, gate, "SessionStart", c.Hooks.SessionStart, []string{source}, payload)).Outcome, nil
	}
}

func (c *Config) sessionEnd(workDir string, gate *Gate) hook.SessionEnd {
	return func(ctx context.Context, reason string) {
		payload := commonPayload(ctx, "SessionEnd", workDir)
		delete(payload, "turn_id")
		delete(payload, "model")
		delete(payload, "permission_mode")
		payload["reason"] = reason
		runEvent(ctx, workDir, gate, "SessionEnd", c.Hooks.SessionEnd, []string{reason}, payload)
	}
}

func (c *Config) subagentStart(workDir string, gate *Gate) hook.SubagentStart {
	return func(ctx context.Context, agentID, agentType string) (hook.Outcome, error) {
		payload := commonPayload(ctx, "SubagentStart", workDir)
		payload["agent_id"], payload["agent_type"] = agentID, agentType
		return aggregate("SubagentStart", runEvent(ctx, workDir, gate, "SubagentStart", c.Hooks.SubagentStart, []string{agentType}, payload)).Outcome, nil
	}
}

func (c *Config) subagentStop(workDir string, gate *Gate) hook.SubagentStop {
	return func(ctx context.Context, agentID, agentType, result string, active bool) (hook.Outcome, error) {
		payload := commonPayload(ctx, "SubagentStop", workDir)
		payload["agent_id"], payload["agent_type"] = agentID, agentType
		payload["agent_transcript_path"], payload["stop_hook_active"], payload["last_assistant_message"] = nil, active, result
		return aggregate("SubagentStop", runEvent(ctx, workDir, gate, "SubagentStop", c.Hooks.SubagentStop, []string{agentType}, payload)).Outcome, nil
	}
}

func (c *Config) preCompact(workDir string, gate *Gate) hook.PreCompact {
	return func(ctx context.Context, trigger string) (hook.Outcome, error) {
		payload := commonPayload(ctx, "PreCompact", workDir)
		delete(payload, "permission_mode")
		addSubagent(payload, hook.RuntimeFromContext(ctx))
		payload["trigger"] = trigger
		return aggregate("PreCompact", runEvent(ctx, workDir, gate, "PreCompact", c.Hooks.PreCompact, []string{trigger}, payload)).Outcome, nil
	}
}

func (c *Config) postCompact(workDir string, gate *Gate) hook.PostCompact {
	return func(ctx context.Context, trigger string) (hook.Outcome, error) {
		payload := commonPayload(ctx, "PostCompact", workDir)
		delete(payload, "permission_mode")
		addSubagent(payload, hook.RuntimeFromContext(ctx))
		payload["trigger"] = trigger
		return aggregate("PostCompact", runEvent(ctx, workDir, gate, "PostCompact", c.Hooks.PostCompact, []string{trigger}, payload)).Outcome, nil
	}
}

func (c *Config) stop(workDir string, gate *Gate) hook.Stop {
	return func(ctx context.Context, lastAssistantMessage string, active bool) (hook.Outcome, error) {
		payload := commonPayload(ctx, "Stop", workDir)
		payload["stop_hook_active"], payload["last_assistant_message"] = active, nullable(lastAssistantMessage)
		return aggregate("Stop", runEvent(ctx, workDir, gate, "Stop", c.Hooks.Stop, nil, payload)).Outcome, nil
	}
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
