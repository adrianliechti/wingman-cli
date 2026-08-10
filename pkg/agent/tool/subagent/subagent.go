package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/hook"
	"github.com/adrianliechti/wingman-agent/pkg/agent/task"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

type subagentType struct {
	Instructions        string
	AllowTool           func(tool.Tool) bool
	WrapDynamicReadOnly bool
	ReadOnly            bool
	Model               string
}

const generalPurposeInstructions = `You are an agent performing a specific delegated task. Complete only the assigned scope. Unless the task explicitly asks you to edit files, stay read-only. Search broadly when you do not know where something lives, then narrow down. Prefer editing existing files over creating new files, and never create documentation files unless explicitly requested. Lead with findings or changes made, grouped by file when relevant, using file:line references. State assumptions and verification gaps at the end, not at the start. Reply in <=200 words unless the task explicitly asks for more. Do not explain your process.`

const exploreInstructions = `You are a read-only codebase research specialist covering broad searches, feature tracing, and analysis of unfamiliar or legacy code. You may search, read, inspect git state, fetch URLs, and use read-only LSP or shell commands. You must not create, modify, delete, move, or copy files, install dependencies, or run mutating git commands. Use grep/glob/LSP before broad reads, read only the line windows that matter, and use parallel tool calls when searches or reads are independent. When tracing how a feature works, find the entry points, follow the call chain, and map the data flow and key components. When analyzing legacy or under-documented code, find the data structures first, then trace the procedures that read and write them, and call out magic values and risky coupling. Cite every concrete claim with file:line references and distinguish confirmed facts from inference. Lead with the answer; end with assumptions or gaps only if they matter. Reply in <=200 words unless the task explicitly asks for more.`

const verificationInstructions = `You are a verification specialist. Your job is to test whether the implementation actually works, not to confirm by reading code. You must not modify project files, install dependencies, or run git write operations. You may run read-only inspection commands and normal build, test, lint, type-check, or local execution commands. For UI, API, CLI, migration, and integration changes, exercise the behavior directly when possible. Probe for failure, not confirmation: try at least one realistic way to break the change (edge input, error path) before declaring PASS. Include the exact commands you ran, the relevant output, and a verdict. End with exactly one line: VERDICT: PASS, VERDICT: FAIL, or VERDICT: PARTIAL.`

const securityInstructions = `You are a security auditor reviewing code for genuinely exploitable vulnerabilities. You are read-only: you may search, read, inspect git state, and run read-only commands, but you must not create, modify, or delete files, build, run, or install anything, or make network requests against the target. Reason from the source. Default to skepticism: re-read the cited code yourself instead of trusting any summary, trace whether attacker-controlled input can actually reach the sink, and hunt for existing protections (input validation, parameterized queries, framework auto-escaping, type/length bounds, auth gates, dead/test code) before concluding a finding is real. Prefer a few high-confidence, exploitable findings over a long list of theoretical ones. For each finding give file:line, the data flow from entry point to sink, a concrete exploit scenario, and a specific fix; call out false positives as such. Reply concisely with file:line references.`

const codeArchitectInstructions = `You are a senior software architect producing implementation blueprints from the existing codebase. You are read-only. First extract local patterns, module boundaries, conventions, and similar implementations with file:line references. Then make one decisive architecture recommendation that fits those patterns. Include the files to create or modify, component responsibilities, interfaces, data flow, sequencing, tests, error handling, performance, and security considerations. Do not present a menu of options unless the caller explicitly asks for tradeoffs.`

const codeReviewerInstructions = `You are a high-precision code reviewer. Review the requested diff or files for real bugs, security issues, important quality problems, and violations of explicit project guidelines. Read the relevant AGENTS.md/CLAUDE.md files when present. Flag only issues the author would genuinely fix: skip style-only nitpicks, pre-existing issues outside the changed lines, intentional changes, and findings that demand rigor the rest of the codebase does not practice. A claim that a change breaks something elsewhere must name the provably affected code, not speculate. Score each candidate 0-100 and report only issues with confidence >=80 — but do not stop at the first qualifying finding; continue until every one is listed. Each finding must include file:line, severity, confidence, why it is real, and a concrete fix; state severity honestly and never inflate it. If nothing clears the bar, say no high-confidence issues were found and mention any verification gap.`

const codeSimplifierInstructions = `You are a code simplification specialist. Preserve behavior exactly while improving clarity, consistency, reuse, and maintainability in recently changed code. Prefer explicit, readable code over clever compression. Look for duplicated helpers, needless state, parameter sprawl, leaky abstractions, stringly-typed logic, unnecessary comments, avoidable work, missed concurrency, and hot-path bloat. If editing is allowed by the caller, make focused changes in existing files and verify when practical; otherwise return only actionable recommendations.`

const testEngineerInstructions = `You are a test engineer focused on executable characterization, regression, contract, and equivalence tests. Use the existing test framework and project conventions. Read branches and boundaries before writing tests. Prefer concrete inputs and expected outputs over vague assertions. Cover failure paths, edge cases, and behavior recently changed. If editing is allowed, add focused tests that run today; if behavior is not implemented yet, mark pending tests using the local convention rather than deleting coverage. Report the exact test command and result.`

var subagentTypes = map[string]subagentType{
	"general-purpose": {
		Instructions: generalPurposeInstructions,
		AllowTool:    allowNonAgentTool,
	},
	"explore": {
		Instructions:        exploreInstructions,
		AllowTool:           allowReadOnlyTool,
		WrapDynamicReadOnly: true,
		ReadOnly:            true,
	},
	"verification": {
		Instructions: verificationInstructions,
		AllowTool:    allowVerificationTool,
	},
	"security": {
		Instructions:        securityInstructions,
		AllowTool:           allowReadOnlyTool,
		WrapDynamicReadOnly: true,
		ReadOnly:            true,
	},
	"code-architect": {
		Instructions:        codeArchitectInstructions,
		AllowTool:           allowReadOnlyTool,
		WrapDynamicReadOnly: true,
		ReadOnly:            true,
	},
	"code-reviewer": {
		Instructions:        codeReviewerInstructions,
		AllowTool:           allowReadOnlyTool,
		WrapDynamicReadOnly: true,
		ReadOnly:            true,
	},
	"code-simplifier": {
		Instructions: codeSimplifierInstructions,
		AllowTool:    allowNonAgentTool,
	},
	"test-engineer": {
		Instructions: testEngineerInstructions,
		AllowTool:    allowNonAgentTool,
	},
}

var builtinOrder = []struct{ name, blurb string }{
	{"general-purpose", "scoped multi-step research or implementation."},
	{"explore", "read-only codebase research — broad searches, subsystem mapping, feature tracing."},
	{"verification", "runs builds/tests/executions to verify an implementation."},
	{"security", "read-only audit for exploitable vulnerabilities."},
	{"code-architect", "read-only implementation blueprint for a larger feature."},
	{"code-reviewer", "read-only high-precision review — bugs, security, guideline violations."},
	{"code-simplifier", "behavior-preserving cleanup of recently changed code."},
	{"test-engineer", "designs and adds tests using the project's conventions."},
}

func mergeTypes(custom []Definition) (types map[string]subagentType, names []string, blurbs []string) {
	types = make(map[string]subagentType, len(subagentTypes)+len(custom))

	overrides := make(map[string]Definition, len(custom))
	for _, d := range custom {
		name := strings.ToLower(d.Name)
		if _, ok := overrides[name]; !ok {
			overrides[name] = d
		}
	}

	for _, b := range builtinOrder {
		names = append(names, b.name)
		if d, ok := overrides[b.name]; ok {
			types[b.name] = d.subagentType()
			blurbs = append(blurbs, "- "+b.name+": "+d.Description)
			continue
		}
		types[b.name] = subagentTypes[b.name]
		blurbs = append(blurbs, "- "+b.name+": "+b.blurb)
	}

	for _, d := range custom {
		name := strings.ToLower(d.Name)
		if _, ok := types[name]; ok {
			continue
		}
		types[name] = d.subagentType()
		names = append(names, name)
		blurbs = append(blurbs, "- "+name+": "+d.Description)
	}

	return types, names, blurbs
}

func Tools(cfg *agent.Config, sharedContext func() string, tasks *task.Registry, custom ...Definition) []tool.Tool {
	types, names, blurbs := mergeTypes(custom)

	lines := []string{
		"Launch a subagent for a bounded task. It runs in a separate context with a filtered toolset and returns one final report; it does not see your conversation, and its intermediate output (file dumps, logs, test runs) never enters your context — only the report does. The report is delivered to you, not the user — relay what matters in your response.",
		"",
		"Agent types:",
	}
	lines = append(lines, blurbs...)
	lines = append(lines,
		"",
		"Write a self-contained prompt: goal, relevant paths/symbols, allowed edit scope, expected output shape. Pick the narrowest fitting type. Don't delegate a single known lookup (use `read`/`grep`/`glob` directly) or synthesis of results you already hold.",
		"",
		"Keep finding and verifying separated: an agent that produced findings never checks them itself. To validate claims — another agent's or your own — launch a fresh agent per claim, prompted to refute the claim rather than confirm it, and pass it only the claim, not the reasoning behind it; the claim is verified when an honest refutation attempt fails, and an uncertain verdict counts as refuted. For a claim that can fail in more than one way, prefer a few verifiers with distinct lenses (correctness, security, does-it-reproduce) over one generalist.",
		"",
		"By default the agent inherits your model and reasoning effort — almost always correct. Override `model` or `effort` only when a different tier clearly fits: `utility` (or lower effort) for mechanical, low-risk sweeps; `plan` (or higher effort) for the hardest verification, review, or architecture work.",
	)

	if tasks != nil {
		lines = append(lines,
			"",
			"Set `background: true` to launch the agent in the background and keep working: the call returns immediately with a task id, and the result arrives later as a task notification. Prefer background for research or verification you can overlap with other work, and launch several to cover independent areas in parallel. Keep the critical path yourself: if your very next step depends on the result, run the agent synchronously instead of waiting idle. While one runs, never invent or assume its result and don't start work that overlaps its scope. Use `task_output` to check on tasks, `task_stop` to cancel one, and `task_send` to ask a finished agent follow-ups with its context intact.",
			"Editing agent types may also run in the background, but give each a file scope disjoint from files you plan to touch: concurrent edits to the same file fail and force a re-read, and prefer `edit` over `write` for any file a background agent might change. Tell each editing agent it is not alone in the tree — it must leave changes outside its scope alone, even ones that look wrong.",
			"Synchronous runs are kept too: their result ends with a task id, and `task_send` continues that agent later with its context intact.",
		)
	}

	description := strings.Join(lines, "\n")

	properties := map[string]any{
		"description": map[string]any{"type": "string", "description": "Short 3-5 word label for the UI (for example, `Audit auth middleware`)."},
		"prompt":      map[string]any{"type": "string", "description": "Self-contained task briefing. Include goal, relevant paths/symbols, allowed edit scope, what is out of scope, and the expected report shape."},
		"agent_type": map[string]any{
			"type":        "string",
			"description": "Agent type to use. Must be one of the available agent types.",
			"enum":        names,
		},
		"schema": map[string]any{
			"type":        "object",
			"description": "Optional JSON Schema for the agent's final result. When set, the agent must deliver its result as validated JSON matching this schema, returned verbatim as the tool result.",
		},
		"model": map[string]any{
			"type":        "string",
			"description": "Optional model role for the new agent: `plan` runs the session's planning model (most capable available), `utility` its utility model (smallest and fastest). Omit to inherit the session model (the default and preferred).",
			"enum":        []string{"plan", "utility"},
		},
		"effort": map[string]any{
			"type":        "string",
			"description": "Optional reasoning-effort override for this agent, clamped to what the chosen model supports. Omit to inherit.",
			"enum":        []string{"none", "low", "medium", "high", "xhigh", "max"},
		},
	}

	if tasks != nil {
		properties["background"] = map[string]any{
			"type":        "boolean",
			"description": "Run in the background and return immediately with a task id; the result arrives later as a task notification. For editing types, only with a file scope disjoint from your own work.",
		}
	}

	agentTools := []tool.Tool{{
		Name:        "agent",
		Description: description,
		Effect:      effectClassifier(types),
		Timeout:     20 * time.Minute,

		Parameters: map[string]any{
			"type": "object",

			"properties": properties,

			"required":             []string{"description", "prompt", "agent_type"},
			"additionalProperties": false,
		},

		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			agentID := uuid.NewString()
			description, ok := args["description"].(string)

			if !ok || strings.TrimSpace(description) == "" {
				return "", fmt.Errorf("description is required")
			}

			prompt, ok := args["prompt"].(string)

			if !ok || strings.TrimSpace(prompt) == "" {
				return "", fmt.Errorf("prompt is required")
			}

			subagentName, ok := args["agent_type"].(string)
			if !ok || strings.TrimSpace(subagentName) == "" {
				return "", fmt.Errorf("agent_type is required")
			}
			subagentName = strings.ToLower(strings.TrimSpace(subagentName))

			typ, ok := types[subagentName]
			if !ok {
				return "", fmt.Errorf("unknown agent_type %q (available: %s)", subagentName, strings.Join(names, ", "))
			}

			var instructions strings.Builder
			instructions.WriteString(typ.Instructions)
			if sharedContext != nil {
				if c := strings.TrimSpace(sharedContext()); c != "" {
					instructions.WriteString("\n\n" + c)
				}
			}
			for _, h := range cfg.Hooks.SubagentStart {
				outcome, err := h(ctx, agentID, subagentName)
				if err == nil && len(outcome.AdditionalContext) > 0 {
					instructions.WriteString("\n\n" + strings.Join(outcome.AdditionalContext, "\n\n"))
				}
			}

			var collector *reportCollector
			if raw, present := args["schema"]; present {
				schemaMap, ok := raw.(map[string]any)
				if !ok {
					return "", fmt.Errorf("schema must be a JSON Schema object")
				}
				var err error
				if collector, err = newReportCollector(schemaMap); err != nil {
					return "", fmt.Errorf("invalid schema: %w", err)
				}
				instructions.WriteString("\n\nDeliver your final result by calling the `report` tool exactly once; its `result` argument must match the provided JSON schema. Prose output outside `report` is not the deliverable.")
			}

			subcfg := cfg.Derive()
			subcfg.Instructions = func() string { return instructions.String() }

			if err := applyModelOverrides(subcfg, args, typ.Model); err != nil {
				return "", err
			}

			subcfg.Tools = func() []tool.Tool {
				var tools []tool.Tool
				if cfg.Tools != nil {
					tools = toolsForType(cfg.Tools(), typ)
				}
				if collector != nil {
					tools = append(tools, collector.tool())
				}
				return tools
			}

			// reportCtx outlives a background run's execution context: its
			// values (usage sink) stay readable after the launching tool call
			// returns and its cancellation must not stop accounting.
			reportCtx := ctx

			sub := &agent.Agent{Config: subcfg}

			// runTurn is one turn on the shared sub-agent: the initial task and
			// each task_send follow-up. Usage is reported as this turn's delta —
			// the cumulative snapshot would double-bill resumed agents.
			runTurn := func(execCtx context.Context, tk *task.Task, input string) (string, error) {
				runtime := hook.RuntimeFromContext(execCtx)
				runtime.AgentID = agentID
				runtime.AgentType = subagentName
				execCtx = hook.WithRuntime(execCtx, runtime)
				// Sub-agent deltas are buffered internally rather than rendered on
				// the parent stream, so a failed attempt can always be discarded.
				execCtx = agent.WithStreamEventHandlers(execCtx, agent.StreamEventHandlers{
					Reset: func() {
						if tk != nil {
							tk.SetActivity("")
						}
					},
				})
				started := time.Now()
				before := sub.UsageSnapshot()
				startIdx := len(sub.MessagesSnapshot())

				runOnce := func(prompt string) error {
					stream, err := sub.Send(execCtx, []agent.Content{{Text: prompt}})
					if err != nil {
						return err
					}
					for msg, err := range stream {
						if err != nil {
							return err
						}
						if tk != nil {
							updateTaskActivity(tk, msg)
						}
					}
					return nil
				}

				runErr := runOnce(input)
				stopHookActive := false
				for runErr == nil && len(cfg.Hooks.SubagentStop) > 0 {
					runMessages := sub.MessagesSnapshot()
					if startIdx <= len(runMessages) {
						runMessages = runMessages[startIdx:]
					}
					lastMessage := strings.TrimSpace(finalText(runMessages))
					var stopOutcome hook.Outcome
					for _, h := range cfg.Hooks.SubagentStop {
						out, err := h(execCtx, agentID, subagentName, lastMessage, stopHookActive)
						if err != nil {
							continue
						}
						if out.Stop {
							stopOutcome = out
							break
						}
						if out.Block && !stopOutcome.Block {
							stopOutcome = out
						}
					}
					if stopOutcome.Stop || !stopOutcome.Block {
						break
					}
					stopHookActive = true
					reason := stopOutcome.Reason
					if reason == "" {
						reason = "A SubagentStop hook requested another pass."
					}
					runErr = runOnce(reason)
				}

				usage := sub.UsageSnapshot()
				delta := tool.UsageDelta{
					InputTokens:  usage.InputTokens - before.InputTokens,
					CachedTokens: usage.CachedTokens - before.CachedTokens,
					OutputTokens: usage.OutputTokens - before.OutputTokens,
				}
				tool.ReportUsage(reportCtx, delta)

				// Only this run's messages: a resumed agent's history still holds
				// every earlier run, and finalText over the full history would
				// re-deliver the previous run's answer.
				runMessages := sub.MessagesSnapshot()
				if startIdx <= len(runMessages) {
					runMessages = runMessages[startIdx:]
				}

				trailer := runTrailer(runMessages, agent.Usage{InputTokens: delta.InputTokens, CachedTokens: delta.CachedTokens, OutputTokens: delta.OutputTokens}, time.Since(started))

				text := strings.TrimSpace(finalText(runMessages))

				if runErr != nil {
					if text == "" {
						return "", fmt.Errorf("agent error: %w", runErr)
					}
					return fmt.Sprintf("Agent aborted before finishing (%v). Last output before the abort — treat as incomplete:\n\n%s%s", runErr, text, trailer), nil
				}

				if collector != nil {
					if payload := collector.take(); payload != "" {
						return payload + trailer, nil
					}
					if text == "" {
						return "Sub-agent completed without calling report and produced no output." + trailer, nil
					}
					return "Sub-agent completed without calling report; unstructured output follows:\n\n" + text + trailer, nil
				}

				if text == "" {
					return "Sub-agent completed but produced no output." + trailer, nil
				}
				return text + trailer, nil
			}

			if background, _ := args["background"].(bool); background {
				if tasks == nil {
					return "", fmt.Errorf("background agents are not available in this session; run the agent synchronously")
				}
				t, err := tasks.Launch(description, subagentName, func(execCtx context.Context, tk *task.Task) (string, error) {
					return runTurn(execCtx, tk, prompt)
				})
				if err != nil {
					return "", err
				}
				t.SetPeek(sub.MessagesSnapshot)
				t.SetResume(func(followUp string) error {
					return tasks.Relaunch(t, func(execCtx context.Context, tk *task.Task) (string, error) {
						return runTurn(execCtx, tk, followUp)
					})
				})
				return fmt.Sprintf("Launched background agent %s (%s: %s). Continue with other work — the result arrives as a task notification when it finishes. Never assume or invent its result, and don't start work that overlaps its scope. Check on it with task_output, cancel it with task_stop, or ask it follow-ups later with task_send.", t.ID, subagentName, description), nil
			}

			started := time.Now()
			out, err := runTurn(ctx, nil, prompt)
			if err != nil || tasks == nil {
				return out, err
			}
			if t := tasks.Adopt(description, subagentName, out, time.Since(started)); t != nil {
				t.SetPeek(sub.MessagesSnapshot)
				t.SetResume(func(followUp string) error {
					return tasks.Relaunch(t, func(execCtx context.Context, tk *task.Task) (string, error) {
						return runTurn(execCtx, tk, followUp)
					})
				})
				out += fmt.Sprintf("\n(id %s — task_send continues this agent with its context intact)", t.ID)
			}
			return out, nil
		},
	}}

	if tasks != nil {
		agentTools = append(agentTools, taskTools(tasks)...)
	}

	return agentTools
}

var effortRank = map[string]int{"none": 0, "low": 1, "medium": 2, "high": 3, "xhigh": 4, "max": 5}

func clampEffort(level string, target agent.ModelOption) string {
	if target.MaxEffort != "" && effortRank[level] > effortRank[target.MaxEffort] {
		return target.MaxEffort
	}
	if target.MinEffort != "" && effortRank[level] < effortRank[target.MinEffort] {
		return target.MinEffort
	}
	return level
}

func applyModelOverrides(cfg *agent.Config, args map[string]any, defaultRole string) error {
	resolve := cfg.SubagentModel

	var target agent.ModelOption

	role := defaultRole
	if model, _ := args["model"].(string); strings.TrimSpace(model) != "" {
		role = strings.ToLower(strings.TrimSpace(model))
		switch role {
		case "plan", "utility":
		default:
			return fmt.Errorf("unknown model role %q (use plan or utility, or omit to inherit)", role)
		}
	}

	// An unresolvable role keeps the inherited model: the role is a
	// preference, not a contract, and the session model always works.
	if role != "" && resolve != nil {
		if opt, ok := resolve(role); ok && opt.ID != "" {
			target = opt
			cfg.Model = func() string { return opt.ID }
		}
	}

	level := ""
	if effort, _ := args["effort"].(string); strings.TrimSpace(effort) != "" {
		level = strings.ToLower(strings.TrimSpace(effort))
		if _, ok := effortRank[level]; !ok {
			return fmt.Errorf("unknown effort %q (use none, low, medium, high, xhigh, or max)", level)
		}
		if target.ID == "" && resolve != nil {
			if opt, ok := resolve(""); ok {
				target = opt
			}
		}
	} else if target.ID != "" && cfg.Effort != nil {
		// A model override also clamps the inherited effort: the session may
		// run at a level the smaller model does not support.
		if inherited := cfg.Effort(); inherited != clampEffort(inherited, target) {
			level = inherited
		}
	}

	if level != "" {
		clamped := clampEffort(level, target)
		cfg.Effort = func() string { return clamped }
	}

	return nil
}

func updateTaskActivity(tk *task.Task, msg agent.Message) {
	for _, c := range msg.Content {
		if c.ToolCall == nil {
			continue
		}
		activity := c.ToolCall.Name
		if hint := tool.ExtractHint(c.ToolCall.Args, c.ToolCall.Name); hint != "" {
			activity += " " + hint
		}
		tk.SetActivity(activity)
	}
}

type reportCollector struct {
	schema   map[string]any
	resolved *jsonschema.Resolved

	mu     sync.Mutex
	result string
}

func newReportCollector(schemaMap map[string]any) (*reportCollector, error) {
	data, err := json.Marshal(schemaMap)
	if err != nil {
		return nil, err
	}

	var schema jsonschema.Schema
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, err
	}

	resolved, err := schema.Resolve(nil)
	if err != nil {
		return nil, err
	}

	return &reportCollector{schema: schemaMap, resolved: resolved}, nil
}

// take returns and clears the recorded result, so each run of a resumed agent
// delivers its own report instead of replaying the previous one.
func (c *reportCollector) take() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.result
	c.result = ""
	return out
}

func (c *reportCollector) tool() tool.Tool {
	return tool.Tool{
		Name:        "report",
		Description: "Deliver your final result. Call exactly once when your task is complete; `result` must match the caller-provided JSON schema. Your text output is not the deliverable.",
		Effect:      tool.StaticEffect(tool.EffectReadOnly),

		Parameters: map[string]any{
			"type": "object",

			"properties": map[string]any{
				"result": c.schema,
			},

			"required":             []string{"result"},
			"additionalProperties": false,
		},

		Execute: func(_ context.Context, args map[string]any) (string, error) {
			value, present := args["result"]
			if !present {
				return "", fmt.Errorf("result is required")
			}

			if err := c.resolved.Validate(value); err != nil {
				return "", fmt.Errorf("result does not match the schema: %v", err)
			}

			data, err := json.Marshal(value)
			if err != nil {
				return "", err
			}

			c.mu.Lock()
			c.result = string(data)
			c.mu.Unlock()

			return "Result recorded. End your turn.", nil
		},
	}
}

func runTrailer(messages []agent.Message, usage agent.Usage, elapsed time.Duration) string {
	calls := 0
	for _, m := range messages {
		for _, c := range m.Content {
			if c.ToolCall != nil {
				calls++
			}
		}
	}

	unit := "tool calls"
	if calls == 1 {
		unit = "tool call"
	}

	return fmt.Sprintf("\n\n(agent: %d %s · %s in / %s out tokens · %s)",
		calls, unit,
		formatTokens(usage.InputTokens), formatTokens(usage.OutputTokens),
		elapsed.Round(time.Second))
}

func formatTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func finalText(messages []agent.Message) string {
	lastTool := -1
	for i, m := range messages {
		for _, c := range m.Content {
			if c.ToolCall != nil || c.ToolResult != nil {
				lastTool = i
			}
		}
	}

	var b strings.Builder
	for _, m := range messages[lastTool+1:] {
		if m.Role != agent.RoleAssistant {
			continue
		}
		for _, c := range m.Content {
			if c.Text != "" {
				b.WriteString(c.Text)
			}
		}
	}

	return b.String()
}

func effectClassifier(types map[string]subagentType) func(map[string]any) tool.Effect {
	return func(args map[string]any) tool.Effect {
		if args == nil {
			return tool.EffectDynamic
		}

		subagentName, ok := args["agent_type"].(string)
		if !ok || strings.TrimSpace(subagentName) == "" {
			return tool.EffectDynamic
		}

		if typ, ok := types[strings.ToLower(strings.TrimSpace(subagentName))]; ok && typ.ReadOnly {
			return tool.EffectReadOnly
		}

		return tool.EffectMutates
	}
}

func toolsForType(tools []tool.Tool, typ subagentType) []tool.Tool {
	filtered := make([]tool.Tool, 0, len(tools))

	for _, t := range tools {
		if !typ.AllowTool(t) {
			continue
		}

		if typ.WrapDynamicReadOnly && t.Effect != nil && t.Effect(nil) == tool.EffectDynamic {
			t = readOnlyDynamicTool(t)
		}

		filtered = append(filtered, t)
	}

	return filtered
}

func readOnlyDynamicTool(t tool.Tool) tool.Tool {
	originalExecute := t.Execute
	originalEffect := t.Effect

	t.Execute = func(ctx context.Context, args map[string]any) (string, error) {
		if originalEffect(args) != tool.EffectReadOnly {
			return "", fmt.Errorf("read-only subagent only allows read-only %s calls", t.Name)
		}
		if originalExecute == nil {
			return "", fmt.Errorf("tool %s has no executor", t.Name)
		}
		return originalExecute(ctx, args)
	}

	return t
}

func allowNonAgentTool(t tool.Tool) bool {
	switch t.Name {
	case "agent", "task_output", "task_stop", "task_send":
		return false
	}
	return !t.Hidden
}

func allowReadOnlyTool(t tool.Tool) bool {
	if t.Name == "elicit" {
		return false
	}

	if !allowNonAgentTool(t) || t.Effect == nil {
		return false
	}

	switch t.Effect(nil) {
	case tool.EffectReadOnly:
		return true
	case tool.EffectDynamic:
		return true
	default:
		return false
	}
}

func allowVerificationTool(t tool.Tool) bool {
	if !allowNonAgentTool(t) {
		return false
	}

	switch t.Name {
	case "write", "edit", "elicit":
		return false
	case "shell":
		return true
	default:
		return t.Effect != nil && t.Effect(nil) == tool.EffectReadOnly
	}
}
