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
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/hook"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/shell"
	"github.com/adrianliechti/wingman-agent/pkg/httpclient"
	"github.com/adrianliechti/wingman-agent/pkg/text"
)

const (
	defaultTimeout                 = 600
	sessionEndTimeout              = 1
	maxHookOutput                  = 16 * 1024
	defaultAdditionalContextTokens = 2_500
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
	Prompt                 string            `json:"prompt,omitempty"`
	Model                  string            `json:"model,omitempty"`
	URL                    string            `json:"url,omitempty"`
	Headers                map[string]string `json:"headers,omitempty"`
	AllowedEnvVars         []string          `json:"allowedEnvVars,omitempty"`
	Timeout                *int              `json:"timeout,omitempty"`
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
		parsed, err := Parse(path, data)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		cfg.Merge(parsed)
	}
	return cfg, errors.Join(errs...)
}

// Parse reads one named hooks.json document. The name is used only in errors,
// which also makes this suitable for inline plugin hook declarations.
func Parse(name string, data []byte) (*Config, error) {
	var parsed Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parsed); err != nil {
		return nil, fmt.Errorf("parse %s: %w", name, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("parse %s: trailing JSON value", name)
		}
		return nil, fmt.Errorf("parse %s: %w", name, err)
	}
	if err := parsed.validate(); err != nil {
		return nil, fmt.Errorf("parse %s: %w", name, err)
	}
	return &parsed, nil
}

func (c *Config) validate() error {
	for _, event := range c.allEvents() {
		for groupIndex, group := range event.groups {
			if !validMatcher(group.Matcher) {
				return fmt.Errorf("%s matcher group %d has invalid regex %q", event.name, groupIndex, group.Matcher)
			}
			for handlerIndex, handler := range group.Hooks {
				if handler.Timeout != nil && *handler.Timeout < 0 {
					return fmt.Errorf("%s handler %d:%d has a negative timeout", event.name, groupIndex, handlerIndex)
				}
				if handler.AdditionalContextLimit != nil && *handler.AdditionalContextLimit < 0 {
					return fmt.Errorf("%s handler %d:%d has a negative additionalContextLimit", event.name, groupIndex, handlerIndex)
				}
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

// Merge appends another hook source while preserving source order.
func (c *Config) Merge(other *Config) {
	if c == nil || other == nil {
		return
	}
	c.merge(*other)
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
	return c.BuildWithOptions(workDir, BuildOptions{Gate: gate})
}

// BuildOptions supplies source-specific trust and environment settings.
type BuildOptions struct {
	Gate        *Gate
	Environment map[string]string
}

// BuildWithOptions compiles hooks and injects source-specific environment
// variables into command handlers. HTTP handlers intentionally do not inherit
// these values into headers unless the process environment explicitly exposes
// them through allowedEnvVars.
func (c *Config) BuildWithOptions(workDir string, options BuildOptions) hook.Hooks {
	var built hook.Hooks
	if len(c.Hooks.PreToolUse) > 0 {
		built.PreToolUse = append(built.PreToolUse, c.preToolUse(workDir, options))
	}
	if len(c.Hooks.PermissionRequest) > 0 {
		built.PermissionRequest = append(built.PermissionRequest, c.permissionRequest(workDir, options))
	}
	if len(c.Hooks.PostToolUse) > 0 {
		built.PostToolUse = append(built.PostToolUse, c.postToolUse(workDir, options))
	}
	if len(c.Hooks.UserPromptSubmit) > 0 {
		built.UserPromptSubmit = append(built.UserPromptSubmit, c.userPromptSubmit(workDir, options))
	}
	if len(c.Hooks.SessionStart) > 0 {
		built.SessionStart = append(built.SessionStart, c.sessionStart(workDir, options))
	}
	if len(c.Hooks.SessionEnd) > 0 {
		built.SessionEnd = append(built.SessionEnd, c.sessionEnd(workDir, options))
	}
	if len(c.Hooks.SubagentStart) > 0 {
		built.SubagentStart = append(built.SubagentStart, c.subagentStart(workDir, options))
	}
	if len(c.Hooks.SubagentStop) > 0 {
		built.SubagentStop = append(built.SubagentStop, c.subagentStop(workDir, options))
	}
	if len(c.Hooks.PreCompact) > 0 {
		built.PreCompact = append(built.PreCompact, c.preCompact(workDir, options))
	}
	if len(c.Hooks.PostCompact) > 0 {
		built.PostCompact = append(built.PostCompact, c.postCompact(workDir, options))
	}
	if len(c.Hooks.Stop) > 0 {
		built.Stop = append(built.Stop, c.stop(workDir, options))
	}
	return built
}

type selectedHandler struct {
	configuredOrder int
	handler         Handler
}

type runResult struct {
	configuredOrder        int
	completionOrder        int
	exitCode               int
	stdout                 string
	stderr                 string
	err                    error
	additionalContextLimit *int
}

func runEvent(ctx context.Context, workDir string, options BuildOptions, event string, groups []MatcherGroup, matcherInputs []string, payload map[string]any) []runResult {
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
			selected = append(selected, selectedHandler{configuredOrder: order, handler: handler})
			order++
		}
	}
	if len(selected) == 0 || !options.Gate.Allowed(ctx) {
		return nil
	}
	input, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	var synchronous []selectedHandler
	for _, selected := range selected {
		if selected.handler.Async {
			// The released Codex runtime parses this field but does not execute
			// async hooks yet. Avoid partial semantics that discard completion
			// output or outlive session teardown differently from Codex.
			continue
		}
		synchronous = append(synchronous, selected)
	}
	ch := make(chan runResult, len(synchronous))
	for _, selected := range synchronous {
		go func() {
			result := selected.handler.run(ctx, workDir, event, input, options.Environment)
			result.configuredOrder = selected.configuredOrder
			ch <- result
		}()
	}
	results := make([]runResult, 0, len(synchronous))
	for completionOrder := range len(synchronous) {
		result := <-ch
		result.completionOrder = completionOrder
		results = append(results, result)
	}
	// Codex reports and aggregates in configured order even though handlers run
	// concurrently. completionOrder remains attached for rewrite arbitration.
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].configuredOrder < results[j].configuredOrder
	})
	return results
}

func (h Handler) run(ctx context.Context, workDir, event string, input []byte, environment map[string]string) runResult {
	timeoutSeconds := defaultTimeout
	if event == "SessionEnd" {
		timeoutSeconds = sessionEndTimeout
	}
	if h.Timeout != nil {
		timeoutSeconds = max(*h.Timeout, 1)
	}
	if event == "SessionEnd" && timeoutSeconds > 3 {
		timeoutSeconds = 3
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	if h.Type == "http" {
		result := h.post(ctx, input)
		result.additionalContextLimit = h.AdditionalContextLimit
		return result
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
	if len(environment) > 0 {
		cmd.Env = mergedEnvironment(os.Environ(), environment)
	}
	cmd.Stdin = bytes.NewReader(input)
	stdout := cappedBuffer{limit: maxHookOutput + 1}
	stderr := cappedBuffer{limit: maxHookOutput + 1}
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	return runResult{
		exitCode: exitCode, stdout: limit(stdout.String()), stderr: limit(stderr.String()),
		err: nonExitError(err), additionalContextLimit: h.AdditionalContextLimit,
	}
}

// cappedBuffer keeps draining a child process after the retained prefix is
// full. Returning the original input length is required by io.Writer and keeps
// a noisy hook from seeing a short-write error or blocking on a full pipe.
type cappedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	written := len(p)
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		return written, nil
	}
	if len(p) > remaining {
		p = p[:remaining]
	}
	_, _ = b.buf.Write(p)
	return written, nil
}

func (b *cappedBuffer) Len() int {
	return b.buf.Len()
}

func (b *cappedBuffer) String() string {
	return b.buf.String()
}

func mergedEnvironment(base []string, overrides map[string]string) []string {
	environment := append([]string(nil), base...)
	for name, value := range overrides {
		prefix := name + "="
		replaced := false
		for i, entry := range environment {
			entryName, _, _ := strings.Cut(entry, "=")
			if sameEnvironmentName(entryName, name) {
				environment[i] = prefix + value
				replaced = true
				break
			}
		}
		if !replaced {
			environment = append(environment, prefix+value)
		}
	}
	return environment
}

func sameEnvironmentName(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
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
	headers := make(map[string]string, len(h.Headers))
	for name, value := range h.Headers {
		headers[name] = os.Expand(value, func(key string) string {
			if allowed[key] {
				return os.Getenv(key)
			}
			return ""
		})
	}
	client, err := httpclient.WithOriginHeaders(http.DefaultClient, h.URL, headers)
	if err != nil {
		return runResult{exitCode: -1, err: err}
	}
	resp, err := client.Do(req)
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
	return text.HeadBytes(value, maxHookOutput) + "\n[hook output truncated]"
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
		for candidate := range strings.SplitSeq(matcher, "|") {
			if slices.Contains(inputs, candidate) {
				return true
			}
		}
		return false
	}
	re, err := regexp.Compile(matcher)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(inputs, re.MatchString)
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
	Continue           *bool               `json:"continue,omitempty"`
	StopReason         *string             `json:"stopReason,omitempty"`
	SuppressOutput     bool                `json:"suppressOutput,omitempty"`
	SystemMessage      *string             `json:"systemMessage,omitempty"`
	Decision           *string             `json:"decision,omitempty"`
	Reason             *string             `json:"reason,omitempty"`
	HookSpecificOutput *hookSpecificOutput `json:"hookSpecificOutput,omitempty"`
}

type hookSpecificOutput struct {
	HookEventName            string              `json:"hookEventName"`
	PermissionDecision       *string             `json:"permissionDecision,omitempty"`
	PermissionDecisionReason *string             `json:"permissionDecisionReason,omitempty"`
	UpdatedInput             json.RawMessage     `json:"updatedInput,omitempty"`
	AdditionalContext        *string             `json:"additionalContext,omitempty"`
	Decision                 *permissionDecision `json:"decision,omitempty"`
}

type permissionDecision struct {
	Behavior string  `json:"behavior"`
	Message  *string `json:"message,omitempty"`
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
				return parsedResult{Outcome: hook.Outcome{AdditionalContext: []string{
					boundAdditionalContext(result.stdout, result.additionalContextLimit),
				}}}
			}
		}
		return parsedResult{}
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return parsedResult{}
	}
	if !validWireOutput(event, output) {
		return parsedResult{}
	}
	specific := output.HookSpecificOutput
	parsed := parsedResult{}
	if output.Continue != nil && !*output.Continue {
		switch event {
		case "PreToolUse", "PermissionRequest", "SubagentStart":
		default:
			parsed.Stop = true
			if output.StopReason != nil {
				parsed.Reason = strings.TrimSpace(*output.StopReason)
			}
		}
	}
	if specific != nil && specific.AdditionalContext != nil {
		if context := strings.TrimSpace(*specific.AdditionalContext); context != "" {
			parsed.AdditionalContext = append(parsed.AdditionalContext,
				boundAdditionalContext(context, result.additionalContextLimit))
		}
	}
	switch event {
	case "PreToolUse":
		if specific != nil && specific.PermissionDecision != nil && *specific.PermissionDecision == "deny" {
			parsed.Block = true
			parsed.Reason = strings.TrimSpace(*specific.PermissionDecisionReason)
		} else if output.Decision != nil && *output.Decision == "block" {
			parsed.Block = true
			parsed.Reason = strings.TrimSpace(*output.Reason)
		} else if specific != nil && specific.PermissionDecision != nil && *specific.PermissionDecision == "allow" {
			parsed.updatedInput = specific.UpdatedInput
		}
	case "PermissionRequest":
		if decision := specific.Decision; decision != nil {
			switch decision.Behavior {
			case "allow":
				parsed.permission.Behavior = hook.PermissionAllow
			case "deny":
				message := "PermissionRequest hook denied approval"
				if decision.Message != nil && strings.TrimSpace(*decision.Message) != "" {
					message = strings.TrimSpace(*decision.Message)
				}
				parsed.permission = hook.PermissionRequestOutcome{Behavior: hook.PermissionDeny, Message: message}
			}
		}
	case "PostToolUse", "UserPromptSubmit", "SubagentStop", "Stop":
		if !parsed.Stop && output.Decision != nil && *output.Decision == "block" {
			parsed.Block = true
			parsed.Reason = strings.TrimSpace(*output.Reason)
		}
	}
	return parsed
}

// boundAdditionalContext applies Codex's per-handler approximate-token
// threshold. A zero value explicitly disables this limit; the process-level
// maxHookOutput ceiling remains in force for safety.
func boundAdditionalContext(value string, configured *int) string {
	tokenLimit := defaultAdditionalContextTokens
	if configured != nil {
		tokenLimit = *configured
	}
	if tokenLimit == 0 || (len(value)+3)/4 <= tokenLimit {
		return value
	}

	byteBudget := tokenLimit * 4
	marker := fmt.Sprintf("\n… hook additionalContext truncated from approximately %d tokens …\n", (len(value)+3)/4)
	previewBudget := max(byteBudget-len(marker), 0)
	headBudget := previewBudget / 2
	tailBudget := previewBudget - headBudget
	return text.HeadBytes(value, headBudget) + marker + text.TailBytes(value, tailBudget)
}

// validWireOutput applies Codex's event-specific output schemas and semantic
// checks while retaining one small shared decoder.
func validWireOutput(event string, output wireOutput) bool {
	specific := output.HookSpecificOutput
	if specific != nil && specific.HookEventName != event {
		return false
	}
	continueProcessing := output.Continue == nil || *output.Continue
	unsupportedControl := !continueProcessing || output.StopReason != nil || output.SuppressOutput
	hasPermissionFields := specific != nil && (specific.PermissionDecision != nil ||
		specific.PermissionDecisionReason != nil || len(specific.UpdatedInput) > 0)
	hasPermissionRequestDecision := specific != nil && specific.Decision != nil
	hasContext := specific != nil && specific.AdditionalContext != nil

	switch event {
	case "PreToolUse":
		if unsupportedControl || hasPermissionRequestDecision {
			return false
		}
		if hasPermissionFields {
			if specific.PermissionDecision == nil {
				return false
			}
			switch *specific.PermissionDecision {
			case "allow":
				return len(specific.UpdatedInput) > 0
			case "deny":
				return len(specific.UpdatedInput) == 0 && specific.PermissionDecisionReason != nil && strings.TrimSpace(*specific.PermissionDecisionReason) != ""
			default:
				return false
			}
		}
		if output.Decision == nil {
			return output.Reason == nil
		}
		return *output.Decision == "block" && output.Reason != nil && strings.TrimSpace(*output.Reason) != ""

	case "PermissionRequest":
		if unsupportedControl || output.Decision != nil || output.Reason != nil || hasPermissionFields || hasContext || specific == nil || specific.Decision == nil {
			return false
		}
		return specific.Decision.Behavior == "allow" || specific.Decision.Behavior == "deny"

	case "PostToolUse":
		if hasPermissionFields || hasPermissionRequestDecision {
			return false
		}
		if !continueProcessing {
			return output.Decision == nil || *output.Decision == "block"
		}
		if output.SuppressOutput {
			return false
		}
		if output.Decision == nil {
			return output.Reason == nil
		}
		return *output.Decision == "block" && output.Reason != nil && strings.TrimSpace(*output.Reason) != ""

	case "UserPromptSubmit":
		if hasPermissionFields || hasPermissionRequestDecision {
			return false
		}
		if !continueProcessing {
			return output.Decision == nil || *output.Decision == "block"
		}
		if output.Decision == nil {
			return true
		}
		return *output.Decision == "block" && output.Reason != nil && strings.TrimSpace(*output.Reason) != ""

	case "SessionStart", "SubagentStart":
		return output.Decision == nil && output.Reason == nil && !hasPermissionFields && !hasPermissionRequestDecision

	case "Stop", "SubagentStop":
		if specific != nil {
			return false
		}
		if !continueProcessing {
			return output.Decision == nil || *output.Decision == "block"
		}
		if output.Decision == nil {
			return specific == nil && output.Decision == nil
		}
		return *output.Decision == "block" && output.Reason != nil && strings.TrimSpace(*output.Reason) != ""

	case "PreCompact", "PostCompact":
		return specific == nil && output.Decision == nil && output.Reason == nil
	}
	return false
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
	case "agent":
		name = "Agent"
	case "write":
		aliases = append(aliases, "Write", "apply_patch")
	case "edit":
		aliases = append(aliases, "Edit", "apply_patch")
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

func (c *Config) preToolUse(workDir string, options BuildOptions) hook.PreToolUse {
	return func(ctx context.Context, call tool.ToolCall) (hook.PreToolUseOutcome, error) {
		name, input, aliases := toolWire(call)
		payload := commonPayload(ctx, "PreToolUse", workDir)
		addSubagent(payload, hook.RuntimeFromContext(ctx))
		payload["tool_name"], payload["tool_input"], payload["tool_use_id"] = name, input, call.ID
		parsed := aggregate("PreToolUse", runEvent(ctx, workDir, options, "PreToolUse", c.Hooks.PreToolUse, aliases, payload))
		return hook.PreToolUseOutcome{Outcome: parsed.Outcome, UpdatedInput: restoreToolInput(call, parsed.updatedInput)}, nil
	}
}

func (c *Config) permissionRequest(workDir string, options BuildOptions) hook.PermissionRequest {
	return func(ctx context.Context, call tool.ToolCall) (hook.PermissionRequestOutcome, error) {
		name, input, aliases := toolWire(call)
		payload := commonPayload(ctx, "PermissionRequest", workDir)
		addSubagent(payload, hook.RuntimeFromContext(ctx))
		payload["tool_name"], payload["tool_input"] = name, input
		return aggregate("PermissionRequest", runEvent(ctx, workDir, options, "PermissionRequest", c.Hooks.PermissionRequest, aliases, payload)).permission, nil
	}
}

func (c *Config) postToolUse(workDir string, options BuildOptions) hook.PostToolUse {
	return func(ctx context.Context, call tool.ToolCall, result string) (hook.Outcome, error) {
		name, input, aliases := toolWire(call)
		payload := commonPayload(ctx, "PostToolUse", workDir)
		addSubagent(payload, hook.RuntimeFromContext(ctx))
		payload["tool_name"], payload["tool_input"], payload["tool_use_id"], payload["tool_response"] = name, input, call.ID, result
		return aggregate("PostToolUse", runEvent(ctx, workDir, options, "PostToolUse", c.Hooks.PostToolUse, aliases, payload)).Outcome, nil
	}
}

func (c *Config) userPromptSubmit(workDir string, options BuildOptions) hook.UserPromptSubmit {
	return func(ctx context.Context, prompt string) (hook.Outcome, error) {
		payload := commonPayload(ctx, "UserPromptSubmit", workDir)
		addSubagent(payload, hook.RuntimeFromContext(ctx))
		payload["prompt"] = prompt
		return aggregate("UserPromptSubmit", runEvent(ctx, workDir, options, "UserPromptSubmit", c.Hooks.UserPromptSubmit, nil, payload)).Outcome, nil
	}
}

func (c *Config) sessionStart(workDir string, options BuildOptions) hook.SessionStart {
	return func(ctx context.Context, source string) (hook.Outcome, error) {
		payload := commonPayload(ctx, "SessionStart", workDir)
		delete(payload, "turn_id")
		payload["source"] = source
		return aggregate("SessionStart", runEvent(ctx, workDir, options, "SessionStart", c.Hooks.SessionStart, []string{source}, payload)).Outcome, nil
	}
}

func (c *Config) sessionEnd(workDir string, options BuildOptions) hook.SessionEnd {
	return func(ctx context.Context, reason string) {
		payload := commonPayload(ctx, "SessionEnd", workDir)
		delete(payload, "turn_id")
		delete(payload, "model")
		delete(payload, "permission_mode")
		payload["reason"] = reason
		runEvent(ctx, workDir, options, "SessionEnd", c.Hooks.SessionEnd, []string{reason}, payload)
	}
}

func (c *Config) subagentStart(workDir string, options BuildOptions) hook.SubagentStart {
	return func(ctx context.Context, agentID, agentType string) (hook.Outcome, error) {
		payload := commonPayload(ctx, "SubagentStart", workDir)
		payload["agent_id"], payload["agent_type"] = agentID, agentType
		return aggregate("SubagentStart", runEvent(ctx, workDir, options, "SubagentStart", c.Hooks.SubagentStart, []string{agentType}, payload)).Outcome, nil
	}
}

func (c *Config) subagentStop(workDir string, options BuildOptions) hook.SubagentStop {
	return func(ctx context.Context, agentID, agentType, result string, active bool) (hook.Outcome, error) {
		payload := commonPayload(ctx, "SubagentStop", workDir)
		payload["agent_id"], payload["agent_type"] = agentID, agentType
		payload["agent_transcript_path"], payload["stop_hook_active"], payload["last_assistant_message"] = nil, active, result
		return aggregate("SubagentStop", runEvent(ctx, workDir, options, "SubagentStop", c.Hooks.SubagentStop, []string{agentType}, payload)).Outcome, nil
	}
}

func (c *Config) preCompact(workDir string, options BuildOptions) hook.PreCompact {
	return func(ctx context.Context, trigger string) (hook.Outcome, error) {
		payload := commonPayload(ctx, "PreCompact", workDir)
		delete(payload, "permission_mode")
		addSubagent(payload, hook.RuntimeFromContext(ctx))
		payload["trigger"] = trigger
		return aggregate("PreCompact", runEvent(ctx, workDir, options, "PreCompact", c.Hooks.PreCompact, []string{trigger}, payload)).Outcome, nil
	}
}

func (c *Config) postCompact(workDir string, options BuildOptions) hook.PostCompact {
	return func(ctx context.Context, trigger string) (hook.Outcome, error) {
		payload := commonPayload(ctx, "PostCompact", workDir)
		delete(payload, "permission_mode")
		addSubagent(payload, hook.RuntimeFromContext(ctx))
		payload["trigger"] = trigger
		return aggregate("PostCompact", runEvent(ctx, workDir, options, "PostCompact", c.Hooks.PostCompact, []string{trigger}, payload)).Outcome, nil
	}
}

func (c *Config) stop(workDir string, options BuildOptions) hook.Stop {
	return func(ctx context.Context, lastAssistantMessage string, active bool) (hook.Outcome, error) {
		payload := commonPayload(ctx, "Stop", workDir)
		payload["stop_hook_active"], payload["last_assistant_message"] = active, nullable(lastAssistantMessage)
		return aggregate("Stop", runEvent(ctx, workDir, options, "Stop", c.Hooks.Stop, nil, payload)).Outcome, nil
	}
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
