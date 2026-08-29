package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/adrianliechti/wingman-agent/pkg/agent/hook"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/text"
)

var errYieldStopped = errors.New("yield stopped")

// ErrTurnInProgress means Send was called while another turn was active.
var ErrTurnInProgress = errors.New("agent turn already in progress")

// ErrEmptyInput means Send was called without any content.
var ErrEmptyInput = errors.New("agent input is empty")

const maxStreamRetries = 2

type Agent struct {
	*Config

	// Events is the canonical append-only runtime ledger. Messages is its
	// materialized conversation projection and remains exported for source
	// compatibility; provider context is maintained separately.
	Events   []RuntimeEvent
	Messages []Message
	Usage    Usage
	Revision uint64

	// Recorder is instance-scoped on purpose: derived subagent configs must not
	// accidentally append child events to their parent's journal.
	Recorder EventRecorder

	stateMu         sync.RWMutex
	contextMessages []Message
	contextSet      bool
	ContextRevision uint64
	runtimeIndex    runtimeEventIndex
	runtimeIndexSet bool
	queueMu         sync.Mutex
	running         bool
	finishing       bool
	pendingInput    [][]Content
	startOnce       sync.Once
	toolRunsMu      sync.Mutex
	toolRuns        map[string]*toolRun
}

type toolRun struct {
	signature string
	done      chan struct{}
	result    tool.Result
}

// Running reports whether a turn is currently active.
func (a *Agent) Running() bool {
	a.queueMu.Lock()
	defer a.queueMu.Unlock()
	return a.running
}

// QueueInput adds guidance to the active run. The agent consumes queued input
// at the next safe model boundary. It returns false when no run is active so
// callers can preserve the input as a normal follow-up instead.
func (a *Agent) QueueInput(input []Content) bool {
	if len(input) == 0 {
		return false
	}
	input = CloneContent(input)
	a.queueMu.Lock()
	defer a.queueMu.Unlock()
	if !a.running || a.finishing {
		return false
	}
	a.pendingInput = append(a.pendingInput, input)
	return true
}

// Send starts exactly one turn. It never queues implicitly: callers that want
// to guide the active turn must use QueueInput, while FIFO follow-ups belong in
// a caller-owned session orchestrator. Setup errors are returned immediately;
// failures after the turn starts are yielded by the returned stream.
func (a *Agent) Send(ctx context.Context, input []Content) (iter.Seq2[Message, error], error) {
	if len(input) == 0 {
		return nil, ErrEmptyInput
	}
	input = CloneContent(input)

	a.queueMu.Lock()
	if a.running {
		a.queueMu.Unlock()
		return nil, ErrTurnInProgress
	}
	a.running = true
	a.finishing = false
	a.queueMu.Unlock()

	runtime := hook.RuntimeFromContext(ctx)
	runtime.TurnID = uuid.NewString()
	if runtime.Model == "" && a.Config.Model != nil {
		runtime.Model = a.Model()
	}
	ctx = hook.WithRuntime(ctx, runtime)
	turnUsageBefore := a.UsageSnapshot()
	if err := a.recordEvents(RuntimeEvent{
		Type: EventTurnStarted, TurnID: runtime.TurnID, Model: runtime.Model,
	}); err != nil {
		a.setRunning(false)
		return nil, err
	}

	failSetup := func(err error) (iter.Seq2[Message, error], error) {
		terminalErr := a.finishTurn(runtime.TurnID, RuntimeFailed, err, turnUsageBefore)
		return nil, errors.Join(err, terminalErr)
	}

	var sessionStartOutcome hook.Outcome
	var sessionStartErr error
	a.startOnce.Do(func() {
		source := runtime.StartSource
		if source == "" {
			source = "startup"
		}
		sessionStartOutcome, sessionStartErr = a.runSessionStartHooks(ctx, source)
	})
	if sessionStartErr != nil {
		return failSetup(sessionStartErr)
	}
	if sessionStartOutcome.Stop {
		if sessionStartOutcome.Reason == "" {
			sessionStartOutcome.Reason = "session stopped by hook"
		}
		return failSetup(errors.New(sessionStartOutcome.Reason))
	}

	var hookContext []string

	for _, h := range a.Hooks.UserPromptSubmit {
		out, err := h(ctx, contentText(input))
		if err != nil {
			return failSetup(err)
		}
		if out.Block || out.Stop {
			if out.Reason == "" {
				out.Reason = "prompt blocked by hook"
			}
			return failSetup(errors.New(out.Reason))
		}
		hookContext = append(hookContext, out.AdditionalContext...)
	}

	messages := []Message{userMessage(input)}
	if len(hookContext) > 0 {
		messages = append(messages, hiddenContextMessage(strings.Join(hookContext, "\n\n")))
	}
	if err := a.appendMessages(messages...); err != nil {
		return failSetup(err)
	}

	maxTurns := a.MaxTurns
	if maxTurns == 0 {
		maxTurns = DefaultMaxTurns
	}

	return func(yield func(Message, error) bool) {
		status := RuntimeCompleted
		var outcomeErr error
		consumerOpen := true
		defer func() {
			if r := recover(); r != nil {
				status = RuntimeFailed
				outcomeErr = fmt.Errorf("agent turn panicked: %v", r)
				_ = a.finishTurn(runtime.TurnID, status, outcomeErr, turnUsageBefore)
				panic(r)
			}
			if err := a.finishTurn(runtime.TurnID, status, outcomeErr, turnUsageBefore); err != nil && consumerOpen {
				yield(Message{}, err)
			}
		}()

		stop := func(err error) {
			outcomeErr = err
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errYieldStopped) {
				status = RuntimeInterrupted
			} else {
				status = RuntimeFailed
			}
			if errors.Is(err, errYieldStopped) {
				consumerOpen = false
				return
			}
			if !yield(Message{}, err) {
				consumerOpen = false
			}
		}
		interrupt := func(reason string) {
			status = RuntimeInterrupted
			outcomeErr = errors.New(reason)
		}

		turns := 0
		cutoffNotified := false
		stopHookActive := false
		for {
			if maxTurns > 0 && turns >= maxTurns {
				stop(ErrMaxTurnsExceeded)
				return
			}

			if err := a.removeOrphanedToolMessages(); err != nil {
				stop(err)
				return
			}

			modelID := ""
			if a.Config.Model != nil {
				modelID = a.Model()
			}
			if err := a.dropForeignReasoning(modelID); err != nil {
				stop(err)
				return
			}

			effort := ""
			if a.Config.Effort != nil {
				effort = a.Effort()
			}

			instructions := ""
			if a.Instructions != nil {
				instructions = a.Instructions()
			}

			// Re-read per round like model and instructions: the session mode can
			// change mid-turn, and it decides which tools are offered.
			var tools []tool.Tool
			if a.Tools != nil {
				tools = a.Tools()
			}
			var outputSchema map[string]any
			if schema, ok := OutputSchemaFromContext(ctx); ok {
				outputSchema = schema
			}
			if outputSchema != nil {
				// Structured output is a finalization pass. Keeping tools enabled
				// lets some providers apply a tool's argument schema to ordinary
				// text deltas instead of producing the requested final document.
				tools = nil
			}

			req := &request{
				model:        modelID,
				effort:       effort,
				instructions: instructions,
				cacheKey:     a.CacheKey,
				messages:     a.requestMessages(),
				tools:        tools,
				outputSchema: outputSchema,
			}

			resp, err := a.completeRun(ctx, runtime.TurnID, req, yield)

			for attempt := 1; err != nil && attempt <= maxStreamRetries; attempt++ {
				if errors.Is(err, errYieldStopped) || ctx.Err() != nil || !isRecoverableError(err) {
					break
				}
				if streamOutputStarted(err) && !EmitStreamEvent(ctx, StreamEventReset) {
					// Retrying would duplicate already-visible deltas for consumers
					// whose stream protocol cannot retract a failed attempt.
					break
				}

				if isContextOverflowError(err) {
					if outcome := a.runPreCompact(ctx, "auto"); outcome.Stop {
						interrupt("compaction stopped by hook")
						return
					}
					compacted, compactErr := a.compactMessages(ctx, true)
					if compactErr != nil {
						err = compactErr
						break
					}
					if compacted {
						if outcome := a.runPostCompact(ctx, "auto"); outcome.Stop {
							interrupt("post-compaction hook stopped the turn")
							return
						}
						outcome, hookErr := a.runSessionStartHooks(ctx, "compact")
						if hookErr != nil {
							err = hookErr
							break
						}
						if outcome.Stop {
							interrupt("session-start hook stopped the compacted turn")
							return
						}
					}
					req.messages = a.requestMessages()
				} else {
					// The SDK already retried transport errors with backoff; this
					// covers failures before streamed output begins, so back off
					// before resending.
					if !waitForRetry(ctx, time.Duration(attempt)*2*time.Second) {
						err = ctx.Err()
						break
					}
				}

				if ctx.Err() != nil {
					err = ctx.Err()
					break
				}

				resp, err = a.completeRun(ctx, runtime.TurnID, req, yield)
			}

			if err != nil {
				stop(err)
				return
			}
			turns++

			EmitStreamEvent(ctx, StreamEventCommit)

			calls := extractToolCalls(resp.messages)

			if len(calls) > 0 {
				if err := a.processToolCalls(ctx, calls, tools, yield); err != nil {
					stop(err)
					return
				}
			}

			// A cut-off response (max output tokens) drops in-flight items. When
			// tool calls survived, their results already drive the next round;
			// otherwise nudge the model once to resume. Only one consecutive
			// nudge. Content-filter stops are final; a continue nudge would just
			// re-trigger them.
			resumeAfterCutoff := resp.incomplete &&
				resp.incompleteReason != "content_filter" &&
				len(calls) == 0 &&
				!cutoffNotified

			if !resp.incomplete {
				cutoffNotified = false
			}

			a.queueMu.Lock()
			queued := a.pendingInput
			a.pendingInput = nil
			if len(queued) == 0 && len(calls) == 0 && !resumeAfterCutoff {
				a.queueMu.Unlock()
				outcome := a.runStopHooks(ctx, assistantText(resp.messages), stopHookActive)
				if outcome.Block && !outcome.Stop {
					stopHookActive = true
					reason := outcome.Reason
					if reason == "" {
						reason = "A Stop hook requested another pass."
					}
					if err := a.appendMessages(hiddenContextMessage(reason)); err != nil {
						stop(err)
						return
					}
					continue
				}
				return
			}
			a.queueMu.Unlock()

			if resumeAfterCutoff {
				cutoffNotified = true
				if err := a.appendMessages(cutoffNotice(resp.incompleteReason)); err != nil {
					stop(err)
					return
				}
			}

			queuedMessages := make([]Message, 0, len(queued))
			for _, in := range queued {
				queuedMessages = append(queuedMessages, userMessage(in))
			}
			if err := a.appendMessages(queuedMessages...); err != nil {
				stop(err)
				return
			}

			// Past the compaction threshold, trim stale tool results first; LLM
			// summarization only runs when the estimated reclaim (~4 bytes per
			// token) cannot cover the overshoot. The reserve leaves headroom to
			// re-measure real usage next turn.
			if overshoot := a.compactionOvershoot(modelID, resp.usage.InputTokens); overshoot > 0 {
				freed, trimErr := a.trimStaleToolResults()
				if trimErr != nil {
					stop(trimErr)
					return
				}
				if int64(freed/4) < overshoot {
					if outcome := a.runPreCompact(ctx, "auto"); outcome.Stop {
						interrupt("compaction stopped by hook")
						return
					}
					compacted, compactErr := a.compactMessages(ctx, false)
					if compactErr != nil {
						stop(compactErr)
						return
					}
					if compacted {
						if outcome := a.runPostCompact(ctx, "auto"); outcome.Stop {
							interrupt("post-compaction hook stopped the turn")
							return
						}
						outcome, hookErr := a.runSessionStartHooks(ctx, "compact")
						if hookErr != nil {
							stop(hookErr)
							return
						}
						if outcome.Stop {
							interrupt("session-start hook stopped the compacted turn")
							return
						}
					}
				}
			}
		}
	}, nil
}

func (a *Agent) runPreCompact(ctx context.Context, trigger string) hook.Outcome {
	var combined hook.Outcome
	for _, h := range a.Hooks.PreCompact {
		outcome, err := h(ctx, trigger)
		if err == nil && outcome.Stop && !combined.Stop {
			combined = outcome
		}
	}
	return combined
}

func (a *Agent) runPostCompact(ctx context.Context, trigger string) hook.Outcome {
	var combined hook.Outcome
	for _, h := range a.Hooks.PostCompact {
		outcome, err := h(ctx, trigger)
		if err == nil && outcome.Stop && !combined.Stop {
			combined = outcome
		}
	}
	return combined
}

func (a *Agent) runSessionStartHooks(ctx context.Context, source string) (hook.Outcome, error) {
	var combined hook.Outcome
	var parts []string
	for _, h := range a.Hooks.SessionStart {
		outcome, err := h(ctx, source)
		if err != nil {
			continue
		}
		parts = append(parts, outcome.AdditionalContext...)
		if outcome.Stop && !combined.Stop {
			combined = outcome
		}
	}
	if len(parts) > 0 {
		if err := a.appendMessages(hiddenContextMessage(strings.Join(parts, "\n\n"))); err != nil {
			return combined, err
		}
	}
	return combined, nil
}

func (a *Agent) runStopHooks(ctx context.Context, lastAssistantMessage string, active bool) hook.Outcome {
	var combined hook.Outcome
	for _, h := range a.Hooks.Stop {
		outcome, err := h(ctx, lastAssistantMessage, active)
		if err != nil {
			continue
		}
		if outcome.Stop && !combined.Stop {
			combined.Stop = true
			combined.Reason = outcome.Reason
		}
		if outcome.Block && !combined.Block {
			combined.Block = true
			combined.Reason = outcome.Reason
		}
	}
	return combined
}

func contentText(input []Content) string {
	var parts []string
	for _, c := range input {
		if c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func assistantText(messages []Message) string {
	var parts []string
	for _, message := range messages {
		if message.Role != RoleAssistant {
			continue
		}
		for _, content := range message.Content {
			if content.Text != "" {
				parts = append(parts, content.Text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func hiddenContextMessage(text string) Message {
	return Message{
		Role:    RoleUser,
		Hidden:  true,
		Content: []Content{{Text: text}},
	}
}

func waitForRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// completeRun gives every provider invocation its own durable lifecycle. A
// retry is therefore a new Run rather than an invisible overwrite of the
// failed attempt. Usage is applied only by the terminal event.
func (a *Agent) completeRun(ctx context.Context, turnID string, req *request, yield func(Message, error) bool) (*response, error) {
	runID := uuid.NewString()
	if err := a.recordEvents(RuntimeEvent{
		Type: EventRunStarted, TurnID: turnID, RunID: runID, Model: req.model,
	}); err != nil {
		return nil, err
	}

	resp, runErr := complete(ctx, a.client, req, yield)
	status := RuntimeCompleted
	if runErr != nil {
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) || errors.Is(runErr, errYieldStopped) {
			status = RuntimeInterrupted
		} else {
			status = RuntimeFailed
		}
	}
	terminal := &RuntimeTerminal{Status: status}
	if runErr != nil {
		terminal.Error = runErr.Error()
	}
	event := RuntimeEvent{
		Type: EventRunTerminal, TurnID: turnID, RunID: runID, Model: req.model, Terminal: terminal,
	}
	if resp != nil {
		usage := resp.usage
		if usage.InputTokens > 0 {
			usage.LastInputTokens = usage.InputTokens
		}
		event.Usage = &usage
	}
	events := make([]RuntimeEvent, 0, 1)
	if runErr == nil && resp != nil {
		events = append(events, messageEvents(resp.messages...)...)
	}
	events = append(events, event)
	if err := a.recordEvents(events...); err != nil {
		if runErr != nil {
			return nil, fmt.Errorf("record terminal fact after provider error %q: %w", runErr, err)
		}
		return nil, err
	}
	return resp, runErr
}

func (a *Agent) setRunning(running bool) {
	a.queueMu.Lock()
	a.running = running
	if !running {
		a.finishing = false
	}
	a.queueMu.Unlock()
}

func (a *Agent) finishTurn(turnID string, status RuntimeStatus, outcomeErr error, before Usage) error {
	a.queueMu.Lock()
	a.finishing = true
	queuedMessages := make([]Message, 0, len(a.pendingInput))
	for _, input := range a.pendingInput {
		queuedMessages = append(queuedMessages, userMessage(input))
	}
	a.pendingInput = nil
	a.queueMu.Unlock()

	appendErr := a.appendMessages(queuedMessages...)
	if appendErr != nil && outcomeErr == nil {
		status = RuntimeFailed
		outcomeErr = appendErr
	}
	after := a.UsageSnapshot()
	delta := Usage{
		InputTokens:  after.InputTokens - before.InputTokens,
		CachedTokens: after.CachedTokens - before.CachedTokens,
		OutputTokens: after.OutputTokens - before.OutputTokens,
	}
	terminal := &RuntimeTerminal{Status: status}
	if outcomeErr != nil {
		terminal.Error = outcomeErr.Error()
	}
	recordErr := a.recordEvents(RuntimeEvent{
		Type: EventTurnTerminal, TurnID: turnID, Usage: &delta, Terminal: terminal,
	})
	a.setRunning(false)
	return errors.Join(appendErr, recordErr)
}

// compactionOvershoot returns how many tokens the last request exceeded the
// compaction threshold by; zero or negative means no compaction is due.
func (a *Agent) compactionOvershoot(model string, lastInputTokens int64) int64 {
	if lastInputTokens <= 0 {
		return 0
	}

	window := a.Config.ContextWindow
	if window < 0 {
		return 0
	}
	if window == 0 {
		window = ContextWindowFor(model)
	}

	reserve := a.Config.ReserveTokens
	if reserve <= 0 {
		reserve = DefaultReserveTokens
		// A fixed default reserve is too thin a margin on large (1M) windows —
		// it would defer compaction to ~97% of the window and lean on the
		// reactive overflow path. Keep at least a 10% headroom, matching the
		// ~90% trigger other Responses-API agents use. An explicit
		// Config.ReserveTokens is honored as-is.
		if frac := window / 10; frac > reserve {
			reserve = frac
		}
	}

	return lastInputTokens - int64(window-reserve)
}

func extractToolCalls(messages []Message) []ToolCall {
	var calls []ToolCall

	for _, m := range messages {
		for _, c := range m.Content {
			if c.ToolCall != nil {
				calls = append(calls, *c.ToolCall)
			}
		}
	}

	return calls
}

func (a *Agent) processToolCalls(ctx context.Context, calls []ToolCall, tools []tool.Tool, yield func(Message, error) bool) error {
	a.beginToolRound()

	for start := 0; start < len(calls); {
		end := start + 1

		if a.isReadOnly(calls[start], tools) {
			for end < len(calls) && a.isReadOnly(calls[end], tools) {
				end++
			}
		}

		var err error

		if end-start > 1 {
			err = a.processToolCallsParallel(ctx, calls[start:end], tools, yield)
		} else {
			err = a.processToolCallsSequential(ctx, calls[start:end], tools, yield)
		}

		if err != nil {
			return err
		}

		start = end
	}

	return nil
}

func (a *Agent) processToolCallsSequential(ctx context.Context, calls []ToolCall, tools []tool.Tool, yield func(Message, error) bool) error {
	for _, tc := range calls {
		if !yield(toolCallMessage(tc), nil) {
			return errYieldStopped
		}

		outcome := a.runSingleToolCallOutcome(ctx, tc, tools)
		resultMsg := toolResultMessage(tc, outcome.result)
		if !outcome.recorded {
			if err := a.appendMessages(resultMsg); err != nil {
				return err
			}
		}

		if !yield(resultMsg, nil) {
			return errYieldStopped
		}
	}

	return nil
}

func (a *Agent) processToolCallsParallel(ctx context.Context, calls []ToolCall, tools []tool.Tool, yield func(Message, error) bool) error {
	for _, tc := range calls {
		if !yield(toolCallMessage(tc), nil) {
			return errYieldStopped
		}
	}

	type completion struct {
		index   int
		outcome toolCallOutcome
	}

	outcomes := make([]toolCallOutcome, len(calls))
	completionOrder := make([]int, 0, len(calls))
	ch := make(chan completion, len(calls))
	jobs := make(chan int, len(calls))

	parallelism := a.MaxParallelTools
	if parallelism == 0 {
		parallelism = DefaultMaxParallelTools
	}
	if parallelism < 0 || parallelism > len(calls) {
		parallelism = len(calls)
	}

	for range parallelism {
		go func() {
			for i := range jobs {
				ch <- completion{i, a.runSingleToolCallOutcome(ctx, calls[i], tools)}
			}
		}()
	}
	for i := range calls {
		jobs <- i
	}
	close(jobs)

	for range calls {
		c := <-ch
		outcomes[c.index] = c.outcome
		completionOrder = append(completionOrder, c.index)
	}

	resultMessages := make([]Message, len(calls))
	var unrecorded []Message
	for i, tc := range calls {
		resultMessages[i] = toolResultMessage(tc, outcomes[i].result)
		if !outcomes[i].recorded {
			unrecorded = append(unrecorded, resultMessages[i])
		}
	}
	if err := a.appendMessages(unrecorded...); err != nil {
		return err
	}
	for _, i := range completionOrder {
		if !yield(resultMessages[i], nil) {
			return errYieldStopped
		}
	}

	return nil
}

type toolCallOutcome struct {
	result   tool.Result
	recorded bool
}

func toolCallMessage(tc ToolCall) Message {
	presentation := NewToolPresentation(tc.Name, tc.Kind, tc.Args, tc.Locations)
	return Message{
		Role: RoleAssistant,
		Content: []Content{{ToolCall: &ToolCall{
			ID: tc.ID, Name: tc.Name, Kind: tc.Kind, Args: tc.Args,
			Locations: tc.Locations, Presentation: presentation,
		}}},
	}
}

const imageResultPlaceholder = "[image attached below]"

const (
	interruptedToolResult = "error: interrupted — the request was canceled before this tool call finished"
	interruptedWaitResult = "error: interrupted while waiting for the original tool call to finish"

	// The code harness installs a more specific PostToolUse hook that persists
	// oversized output before replacing it with a preview. This is the final
	// safety net for every other Agent embedding and for context added by hooks.
	maxInlineToolResultBytes = 100 * 1024
	toolResultHeadBytes      = 4 * 1024
	toolResultTailBytes      = 8 * 1024
)

func toolResultMessage(tc ToolCall, result tool.Result) Message {
	presentation := NewToolPresentation(tc.Name, tc.Kind, tc.Args, tc.Locations)
	if tool.IsImageResult(result.Content) {
		return Message{
			Role: RoleAssistant,
			Content: []Content{
				{ToolResult: &ToolResult{
					ID: tc.ID, Name: tc.Name, Kind: tc.Kind, Args: tc.Args,
					Locations: tc.Locations, Presentation: presentation,
					Content: imageResultPlaceholder, IsError: result.IsError, Metadata: result.Metadata,
				}},
				{File: &File{Data: result.Content}},
			},
		}
	}

	return Message{
		Role: RoleAssistant,
		Content: []Content{{ToolResult: &ToolResult{
			ID: tc.ID, Name: tc.Name, Kind: tc.Kind, Args: tc.Args,
			Locations: tc.Locations, Presentation: presentation,
			Content: result.Content, IsError: result.IsError, Metadata: result.Metadata,
		}}},
	}
}

func (a *Agent) runSingleToolCall(ctx context.Context, tc ToolCall, tools []tool.Tool) tool.Result {
	return a.runSingleToolCallOutcome(ctx, tc, tools).result
}

func (a *Agent) runSingleToolCallOutcome(ctx context.Context, tc ToolCall, tools []tool.Tool) (outcome toolCallOutcome) {
	defer func() {
		if r := recover(); r != nil {
			outcome = toolCallOutcome{result: tool.Error(fmt.Sprintf("error: tool %s panicked: %v", tc.Name, r))}
		}
	}()
	return a.toolCallOutcome(ctx, tc, tools)
}

func (a *Agent) toolCallOutcome(ctx context.Context, tc ToolCall, tools []tool.Tool) toolCallOutcome {
	if tc.ID == "" {
		return a.executeToolOperation(ctx, tc, tools)
	}
	if result, ok := a.retainedToolResult(tc); ok {
		return toolCallOutcome{result: result}
	}

	signature := tc.Name + "\x00" + tc.Args
	a.toolRunsMu.Lock()
	if a.toolRuns == nil {
		a.toolRuns = make(map[string]*toolRun)
	}
	if existing := a.toolRuns[tc.ID]; existing != nil {
		if existing.signature != signature {
			a.toolRunsMu.Unlock()
			return toolCallOutcome{result: tool.Error(fmt.Sprintf("error: tool call ID %s was reused with different arguments", tc.ID))}
		}
		done := existing.done
		a.toolRunsMu.Unlock()
		select {
		case <-done:
			return toolCallOutcome{result: cloneToolResult(existing.result)}
		case <-ctx.Done():
			return toolCallOutcome{result: tool.Error(interruptedWaitResult)}
		}
	}
	run := &toolRun{signature: signature, done: make(chan struct{})}
	a.toolRuns[tc.ID] = run
	a.toolRunsMu.Unlock()

	outcome := a.executeToolOperation(ctx, tc, tools)
	a.toolRunsMu.Lock()
	run.result = cloneToolResult(outcome.result)
	close(run.done)
	a.toolRunsMu.Unlock()
	return outcome
}

func (a *Agent) executeToolOperation(ctx context.Context, tc ToolCall, tools []tool.Tool) toolCallOutcome {
	operationID := uuid.NewString()
	effect := a.toolEffect(tc, tools)
	started := RuntimeEvent{
		Type:        EventToolStarted,
		TurnID:      hook.RuntimeFromContext(ctx).TurnID,
		OperationID: operationID,
		Tool:        &RuntimeTool{CallID: tc.ID, Name: tc.Name, Args: tc.Args, Effect: string(effect)},
	}
	if err := a.recordEvents(started); err != nil {
		return toolCallOutcome{result: tool.Error(fmt.Sprintf("error: could not durably start tool call: %v", err))}
	}

	result := a.executeSingleToolCall(ctx, tc, tools)
	status := RuntimeCompleted
	if result.IsError {
		status = RuntimeFailed
	}
	if result.Content == interruptedToolResult || result.Content == interruptedWaitResult {
		status = RuntimeInterrupted
	}
	terminal := &RuntimeTerminal{Status: status}
	if result.IsError {
		terminal.Error = result.Content
	}
	resultMessage := toolResultMessage(tc, result)
	if err := a.recordEvents(append(messageEvents(resultMessage), RuntimeEvent{
		Type:        EventToolTerminal,
		TurnID:      started.TurnID,
		OperationID: operationID,
		Tool:        &RuntimeTool{CallID: tc.ID, Name: tc.Name, Args: tc.Args, Effect: string(effect), IsError: result.IsError},
		Terminal:    terminal,
	})...); err != nil {
		return toolCallOutcome{result: tool.Error(fmt.Sprintf("error: tool finished but its result and terminal fact could not be persisted: %v", err))}
	}
	return toolCallOutcome{result: result, recorded: true}
}

func (a *Agent) beginToolRound() {
	a.toolRunsMu.Lock()
	a.toolRuns = nil
	a.toolRunsMu.Unlock()
}

func (a *Agent) retainedToolResult(tc ToolCall) (tool.Result, bool) {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	for i := len(a.Messages) - 1; i >= 0; i-- {
		for j := len(a.Messages[i].Content) - 1; j >= 0; j-- {
			result := a.Messages[i].Content[j].ToolResult
			if result == nil || result.ID != tc.ID {
				continue
			}
			if result.Name != tc.Name || result.Args != tc.Args {
				return tool.Error(fmt.Sprintf("error: tool call ID %s was reused with different arguments", tc.ID)), true
			}
			if result.Content == interruptedToolResult || result.Content == interruptedWaitResult {
				return tool.Result{}, false
			}
			content := result.Content
			if content == imageResultPlaceholder && j+1 < len(a.Messages[i].Content) {
				if file := a.Messages[i].Content[j+1].File; file != nil {
					content = file.Data
				}
			}
			return tool.Result{Content: content, IsError: result.IsError, Metadata: maps.Clone(result.Metadata)}, true
		}
	}
	return tool.Result{}, false
}

func cloneToolResult(result tool.Result) tool.Result {
	result.Metadata = maps.Clone(result.Metadata)
	return result
}

func (a *Agent) executeSingleToolCall(ctx context.Context, tc ToolCall, tools []tool.Tool) tool.Result {
	started := time.Now()
	t := findTool(tc.Name, tools)

	timeout := a.ToolTimeout
	if timeout == 0 && t != nil {
		timeout = t.Timeout
	}
	if timeout == 0 {
		timeout = DefaultToolTimeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	ctx = tool.WithProgressCall(ctx, tc.ID)
	var usageMu sync.Mutex
	var usageErr error
	ctx = tool.WithUsageSink(ctx, func(d tool.UsageDelta) {
		usage := Usage{InputTokens: d.InputTokens, CachedTokens: d.CachedTokens, OutputTokens: d.OutputTokens}
		if err := a.recordEvents(RuntimeEvent{Type: EventUsage, TurnID: hook.RuntimeFromContext(ctx).TurnID, Usage: &usage}); err != nil {
			usageMu.Lock()
			usageErr = errors.Join(usageErr, err)
			usageMu.Unlock()
		}
	})

	hc := tool.ToolCall{ID: tc.ID, Name: tc.Name, Args: tc.Args}

	var result tool.Result
	execute := true
	var hookContext []string

	for _, h := range a.Hooks.PreToolUse {
		outcome, err := h(ctx, hc)

		if err != nil {
			result = tool.Error(fmt.Sprintf("error: %v", err))
			execute = false
			break
		}
		hookContext = append(hookContext, outcome.AdditionalContext...)
		if len(outcome.UpdatedInput) > 0 && json.Valid(outcome.UpdatedInput) {
			hc.Args = string(outcome.UpdatedInput)
			tc.Args = hc.Args
		}
		if outcome.Block || outcome.Stop {
			reason := outcome.Reason
			if reason == "" {
				reason = "tool call blocked by hook"
			}
			result = tool.Error("error: " + reason)
			execute = false
			break
		}
	}

	if execute {
		result = a.executeTool(hook.WithToolCall(ctx, hc), tc, t, timeout, started)
	}

	for _, h := range a.Hooks.PostToolUse {
		outcome, err := h(ctx, hc, result.Content)

		if err != nil {
			result = tool.Error(fmt.Sprintf("error: %v", err))
			break
		}
		if outcome.UpdatedResult != nil {
			result.Content = *outcome.UpdatedResult
		}
		hookContext = append(hookContext, outcome.AdditionalContext...)
		if outcome.Block || outcome.Stop {
			reason := outcome.Reason
			if reason == "" {
				reason = "agentic loop stopped by post-tool hook"
			}
			// Codex replaces the completed tool result with hook feedback. The
			// side effect has already happened, but the original output must not
			// continue into the next model request.
			result.Content = reason
			result.IsError = true
		}
	}
	if len(hookContext) > 0 && !tool.IsImageResult(result.Content) {
		result.Content += "\n\n<hook-context>\n" + strings.Join(hookContext, "\n\n") + "\n</hook-context>"
	}
	if !tool.IsImageResult(result.Content) {
		result.Content = boundToolResult(result.Content)
	}
	usageMu.Lock()
	deferredUsageErr := usageErr
	usageMu.Unlock()
	if deferredUsageErr != nil {
		return tool.Error(fmt.Sprintf("error: tool usage could not be persisted: %v", deferredUsageErr))
	}

	return result
}

func boundToolResult(content string) string {
	if len(content) <= maxInlineToolResultBytes {
		return content
	}
	head := text.HeadBytes(content, toolResultHeadBytes)
	tail := text.TailBytes(content, toolResultTailBytes)
	return fmt.Sprintf(
		"<truncated-output>\nOutput was %d bytes — too large for inline and was truncated by the agent harness.\n\nPreview (first %d bytes):\n\n%s\n\n[...]\n\nPreview (last %d bytes):\n\n%s\n</truncated-output>",
		len(content), len(head), head, len(tail), tail,
	)
}

func (a *Agent) executeTool(ctx context.Context, tc ToolCall, t *tool.Tool, timeout time.Duration, started time.Time) tool.Result {
	if t == nil {
		return tool.Error(fmt.Sprintf("error: unknown tool %s", tc.Name))
	}
	if t.Execute == nil {
		return tool.Error(fmt.Sprintf("error: tool %s has no executor", tc.Name))
	}

	args := make(map[string]any)

	if tc.Args != "" {
		if err := json.Unmarshal([]byte(tc.Args), &args); err != nil {
			return tool.Error(fmt.Sprintf("error: failed to parse arguments: %v", err))
		}
	}

	result, err := t.Execute(ctx, args)

	if err != nil {
		// Rewrite only errors the context caused; a tool's own failure that
		// races a cancellation must stay visible as-is.
		switch {
		case errors.Is(err, context.Canceled) && errors.Is(ctx.Err(), context.Canceled):
			return tool.Error(interruptedToolResult)
		case errors.Is(err, context.DeadlineExceeded) && errors.Is(ctx.Err(), context.DeadlineExceeded):
			if timeout > 0 && time.Since(started) >= timeout {
				return tool.Error(fmt.Sprintf("error: tool call aborted after exceeding its %s time limit", timeout))
			}
			return tool.Error("error: tool call aborted — the request deadline expired before it finished")
		}
		return tool.Error(fmt.Sprintf("error: %v", err))
	}

	return result
}

func (a *Agent) isReadOnly(tc ToolCall, tools []tool.Tool) bool {
	return a.toolEffect(tc, tools) == tool.EffectReadOnly
}

func (a *Agent) toolEffect(tc ToolCall, tools []tool.Tool) tool.Effect {
	t := findTool(tc.Name, tools)
	if t == nil || t.Effect == nil {
		return tool.EffectDynamic
	}

	var args map[string]any
	if tc.Args != "" {
		if err := json.Unmarshal([]byte(tc.Args), &args); err != nil {
			return tool.EffectDynamic
		}
	}

	return t.Effect(args)
}

func findTool(name string, tools []tool.Tool) *tool.Tool {
	for i := range tools {
		if tools[i].Name == name {
			return &tools[i]
		}
	}

	return nil
}

func userMessage(input []Content) Message {
	hidden := len(input) > 0
	for _, c := range input {
		if !c.Hidden {
			hidden = false
			break
		}
	}
	return Message{
		Role:    RoleUser,
		Hidden:  hidden,
		Content: input,
	}
}

func cutoffNotice(reason string) Message {
	if reason == "" {
		reason = "max_output_tokens"
	}

	text := fmt.Sprintf("<system-reminder>Your previous response was cut off before completing (%s); anything past the cutoff, including further tool calls, was dropped. Continue from where it stopped without repeating completed work.</system-reminder>", reason)

	return Message{
		Role:    RoleUser,
		Hidden:  true,
		Content: []Content{{Text: text}},
	}
}
