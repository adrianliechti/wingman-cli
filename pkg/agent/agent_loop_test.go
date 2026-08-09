package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/adrianliechti/wingman-agent/pkg/agent/hook"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func streamingTestClient(body func(*http.Request) string) openai.Client {
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(body(r))),
			Request:    r,
		}, nil
	})}

	return openai.NewClient(
		option.WithBaseURL("http://agent.test"),
		option.WithAPIKey("test"),
		option.WithHTTPClient(httpClient),
	)
}

func TestSendLimitsRunawayToolCallRounds(t *testing.T) {
	var requests atomic.Int64

	client := streamingTestClient(func(*http.Request) string {
		request := requests.Add(1)
		return fmt.Sprintf("data: {\"type\":\"response.output_item.done\",\"sequence_number\":1,\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"fc_%d\",\"call_id\":\"call_%d\",\"name\":\"loop\",\"arguments\":\"{}\",\"status\":\"completed\"}}\n\ndata: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"usage\":{\"input_tokens\":1,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":1}}}\n\ndata: [DONE]\n\n", request, request)
	})

	var executions atomic.Int64
	var toolSnapshots atomic.Int64
	a := &Agent{Config: &Config{
		client:   &client,
		MaxTurns: 3,
		Tools: func() []tool.Tool {
			toolSnapshots.Add(1)
			return []tool.Tool{{
				Name: "loop",
				Execute: func(context.Context, map[string]any) (string, error) {
					executions.Add(1)
					return "again", nil
				},
			}}
		},
	}}

	var runErr error
	stream, err := a.Send(context.Background(), []Content{{Text: "start"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range stream {
		if err != nil {
			runErr = err
		}
	}

	if !errors.Is(runErr, ErrMaxTurnsExceeded) {
		t.Fatalf("run error = %v, want %v", runErr, ErrMaxTurnsExceeded)
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("model requests = %d, want 3", got)
	}
	if got := executions.Load(); got != 3 {
		t.Fatalf("tool executions = %d, want 3", got)
	}
	if got := toolSnapshots.Load(); got != 3 {
		t.Fatalf("tool snapshots = %d, want 1 per round", got)
	}
}

func TestSendAllowsFinalResponseAtMaxTurns(t *testing.T) {
	var requests atomic.Int64
	client := streamingTestClient(func(*http.Request) string {
		requests.Add(1)
		return "data: {\"type\":\"response.completed\",\"sequence_number\":1,\"response\":{\"output\":[{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"done\",\"annotations\":[]}]}],\"usage\":{\"input_tokens\":1,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":1}}}\n\n"
	})

	a := &Agent{Config: &Config{client: &client, MaxTurns: 1}}
	var runErr error
	stream, err := a.Send(context.Background(), []Content{{Text: "start"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range stream {
		if err != nil {
			runErr = err
		}
	}

	if runErr != nil {
		t.Fatalf("run error = %v, want nil", runErr)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("model requests = %d, want 1", got)
	}
}

func TestCompleteClassifiesTransientTerminalFailureBeforeOutput(t *testing.T) {
	client := streamingTestClient(func(*http.Request) string {
		return "data: {\"type\":\"response.failed\",\"sequence_number\":1,\"response\":{\"error\":{\"code\":\"server_error\",\"message\":\"try again\"}}}\n\n"
	})

	_, err := complete(context.Background(), &client, &request{}, yieldAll)
	if err == nil {
		t.Fatal("complete error = nil, want response failure")
	}
	if !isRecoverableError(err) {
		t.Fatalf("error = %v, want recoverable", err)
	}
}

func TestCompleteRetriesTransientTerminalFailureAfterOutput(t *testing.T) {
	client := streamingTestClient(func(*http.Request) string {
		return "data: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"delta\":\"partial\"}\n\ndata: {\"type\":\"response.failed\",\"sequence_number\":2,\"response\":{\"error\":{\"code\":\"server_error\",\"message\":\"try again\"}}}\n\n"
	})

	_, err := complete(context.Background(), &client, &request{}, yieldAll)
	if err == nil {
		t.Fatal("complete error = nil, want response failure")
	}
	if !isRecoverableError(err) {
		t.Fatalf("error = %v, want recoverable transient failure", err)
	}
}

func TestSendResetsVisibleOutputBeforeRetry(t *testing.T) {
	var requests atomic.Int64
	client := streamingTestClient(func(*http.Request) string {
		if requests.Add(1) == 1 {
			return "data: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"delta\":\"failed partial\"}\n\ndata: {\"type\":\"response.failed\",\"sequence_number\":2,\"response\":{\"error\":{\"code\":\"server_error\",\"message\":\"try again\"}}}\n\n"
		}
		return "data: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"item_id\":\"msg_2\",\"output_index\":0,\"content_index\":0,\"delta\":\"final answer\"}\n\ndata: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"output\":[{\"type\":\"message\",\"id\":\"msg_2\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"final answer\",\"annotations\":[]}]}],\"usage\":{\"input_tokens\":1,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":1}}}\n\n"
	})

	a := &Agent{Config: &Config{client: &client}}
	var events []string
	ctx := WithStreamEventHandlers(context.Background(), StreamEventHandlers{
		Reset:  func() { events = append(events, "reset") },
		Commit: func() { events = append(events, "commit") },
	})
	stream, err := a.Send(ctx, []Content{{Text: "start"}})
	if err != nil {
		t.Fatal(err)
	}
	for msg, err := range stream {
		if err != nil {
			t.Fatal(err)
		}
		if len(msg.Content) > 0 {
			events = append(events, msg.Content[0].Text)
		}
	}

	want := []string{"failed partial", "reset", "final answer", "commit"}
	if !slices.Equal(events, want) {
		t.Fatalf("stream events = %q, want %q", events, want)
	}
	if messages := a.MessagesSnapshot(); len(messages) != 2 || messages[1].Content[0].Text != "final answer" {
		t.Fatalf("retained messages contain failed attempt: %+v", messages)
	}
}

func TestSendDoesNotRetryVisibleOutputWithoutResetCapability(t *testing.T) {
	var requests atomic.Int64
	client := streamingTestClient(func(*http.Request) string {
		requests.Add(1)
		return "data: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"delta\":\"partial\"}\n\ndata: {\"type\":\"response.failed\",\"sequence_number\":2,\"response\":{\"error\":{\"code\":\"server_error\",\"message\":\"try again\"}}}\n\n"
	})

	a := &Agent{Config: &Config{client: &client}}
	ctx := WithStreamEventHandlers(context.Background(), StreamEventHandlers{
		// A handler for another lifecycle event must not imply reset support.
		Commit: func() {},
	})
	stream, err := a.Send(ctx, []Content{{Text: "start"}})
	if err != nil {
		t.Fatal(err)
	}
	var text string
	var streamErr error
	for msg, err := range stream {
		if err != nil {
			streamErr = err
			continue
		}
		if len(msg.Content) > 0 {
			text += msg.Content[0].Text
		}
	}
	if streamErr == nil || text != "partial" || requests.Load() != 1 {
		t.Fatalf("text=%q requests=%d err=%v", text, requests.Load(), streamErr)
	}
}

func TestEndRunPreservesQueuedUserInput(t *testing.T) {
	a := &Agent{
		Config:       &Config{},
		running:      true,
		pendingInput: [][]Content{{{Text: "queued"}}},
	}

	a.endRun()

	if a.running || len(a.pendingInput) != 0 {
		t.Fatalf("run state was not cleared: running=%v pending=%d", a.running, len(a.pendingInput))
	}
	if len(a.Messages) != 1 || a.Messages[0].Role != RoleUser || a.Messages[0].Content[0].Text != "queued" {
		t.Fatalf("queued input was not preserved: %+v", a.Messages)
	}
}

func TestQueueInputOnlyAcceptsDuringRunAndOwnsItsSlice(t *testing.T) {
	a := &Agent{}
	if a.QueueInput([]Content{{Text: "too early"}}) {
		t.Fatal("QueueInput accepted without an active run")
	}

	a.queueMu.Lock()
	a.running = true
	a.queueMu.Unlock()
	input := []Content{{Text: "guidance", File: &File{Name: "before.txt"}}}
	if !a.QueueInput(input) {
		t.Fatal("QueueInput rejected an active run")
	}
	input[0].Text = "mutated"
	input[0].File.Name = "after.txt"

	a.queueMu.Lock()
	defer a.queueMu.Unlock()
	if len(a.pendingInput) != 1 || a.pendingInput[0][0].Text != "guidance" || a.pendingInput[0][0].File.Name != "before.txt" {
		t.Fatalf("pending input = %#v", a.pendingInput)
	}
}

func TestSendOwnsAcceptedInput(t *testing.T) {
	client := streamingTestClient(func(*http.Request) string {
		return "data: {\"type\":\"response.completed\",\"sequence_number\":1,\"response\":{\"usage\":{\"input_tokens\":1,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":0}}}\n\n"
	})
	a := &Agent{Config: &Config{client: &client}}
	input := []Content{{Text: "before", File: &File{Name: "before.txt"}}}
	stream, err := a.Send(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	input[0].Text = "after"
	input[0].File.Name = "after.txt"
	for range stream {
	}

	messages := a.MessagesSnapshot()
	if len(messages) == 0 || messages[0].Content[0].Text != "before" || messages[0].Content[0].File.Name != "before.txt" {
		t.Fatalf("accepted input was mutated: %+v", messages)
	}
}

func TestSendReportsImmediateUsageErrors(t *testing.T) {
	a := &Agent{}
	if _, err := a.Send(context.Background(), nil); !errors.Is(err, ErrEmptyInput) {
		t.Fatalf("empty input error = %v", err)
	}
	a.running = true
	if _, err := a.Send(context.Background(), []Content{{Text: "another turn"}}); !errors.Is(err, ErrTurnInProgress) {
		t.Fatalf("busy error = %v", err)
	}
	if len(a.pendingInput) != 0 {
		t.Fatalf("Send queued implicitly: %#v", a.pendingInput)
	}
}

func TestCompleteBackfillsOutputFromTerminalEvent(t *testing.T) {
	client := streamingTestClient(func(*http.Request) string {
		return "data: {\"type\":\"response.completed\",\"sequence_number\":1,\"response\":{\"output\":[{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"done\",\"annotations\":[]}]}],\"usage\":{\"input_tokens\":1,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":1}}}\n\ndata: [DONE]\n\n"
	})

	resp, err := complete(context.Background(), &client, &request{}, yieldAll)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.messages) != 1 || len(resp.messages[0].Content) != 1 || resp.messages[0].Content[0].Text != "done" {
		t.Fatalf("terminal output was not backfilled: %+v", resp.messages)
	}
}

func TestCompleteBackfillsItemsMissingFromDoneEvents(t *testing.T) {
	client := streamingTestClient(func(*http.Request) string {
		return "data: {\"type\":\"response.output_item.done\",\"sequence_number\":1,\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"first\",\"annotations\":[]}]}}\n\ndata: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"output\":[{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"first\",\"annotations\":[]}]},{\"type\":\"message\",\"id\":\"msg_2\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"second\",\"annotations\":[]}]}],\"usage\":{\"input_tokens\":1,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":2}}}\n\n"
	})

	resp, err := complete(context.Background(), &client, &request{}, yieldAll)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.messages) != 2 || resp.messages[1].Content[0].Text != "second" {
		t.Fatalf("terminal output did not fill missing item: %+v", resp.messages)
	}
}

func TestParallelToolCallsRespectConcurrencyLimit(t *testing.T) {
	var active atomic.Int64
	var peak atomic.Int64
	var mu sync.Mutex
	var completed []string

	execute := func(context.Context, map[string]any) (string, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			old := peak.Load()
			if current <= old || peak.CompareAndSwap(old, current) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		return "ok", nil
	}

	a := &Agent{Config: &Config{MaxParallelTools: 3, ToolTimeout: -1}}
	tools := []tool.Tool{{Name: "read", Execute: execute}}
	calls := make([]ToolCall, 20)
	for i := range calls {
		calls[i] = ToolCall{ID: fmt.Sprintf("call-%d", i), Name: "read"}
	}
	err := a.processToolCallsParallel(context.Background(), calls, tools, func(m Message, err error) bool {
		if err == nil && len(m.Content) > 0 && m.Content[0].ToolResult != nil {
			mu.Lock()
			completed = append(completed, m.Content[0].ToolResult.ID)
			mu.Unlock()
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := peak.Load(); got != 3 {
		t.Fatalf("peak concurrency = %d, want 3", got)
	}
	if len(completed) != len(calls) {
		t.Fatalf("completed results = %d, want %d", len(completed), len(calls))
	}
}

func TestToolWithoutExecutorReturnsError(t *testing.T) {
	a := &Agent{Config: &Config{ToolTimeout: -1}}
	got := a.runSingleToolCall(context.Background(), ToolCall{Name: "broken"}, []tool.Tool{{Name: "broken"}})
	if !strings.Contains(got, "no executor") {
		t.Fatalf("result = %q", got)
	}
}

func TestToolTimeoutIncludesPreHooks(t *testing.T) {
	a := &Agent{Config: &Config{
		ToolTimeout: 10 * time.Millisecond,
		Hooks: hook.Hooks{PreToolUse: []hook.PreToolUse{
			func(ctx context.Context, _ tool.ToolCall) (hook.PreToolUseOutcome, error) {
				<-ctx.Done()
				return hook.PreToolUseOutcome{}, nil
			},
		}},
	}}
	got := a.runSingleToolCall(context.Background(), ToolCall{Name: "slow"}, []tool.Tool{{
		Name: "slow",
		Execute: func(ctx context.Context, _ map[string]any) (string, error) {
			return "", ctx.Err()
		},
	}})
	if !strings.Contains(got, "10ms time limit") {
		t.Fatalf("result = %q", got)
	}
}

func TestCodexLifecycleRunsSessionStartBeforePromptSubmit(t *testing.T) {
	var order []string
	a := &Agent{Config: &Config{Hooks: hook.Hooks{
		SessionStart: []hook.SessionStart{func(context.Context, string) (hook.Outcome, error) {
			order = append(order, "SessionStart")
			return hook.Outcome{}, nil
		}},
		UserPromptSubmit: []hook.UserPromptSubmit{func(context.Context, string) (hook.Outcome, error) {
			order = append(order, "UserPromptSubmit")
			return hook.Outcome{}, nil
		}},
	}}}
	if _, err := a.Send(context.Background(), []Content{{Text: "hello"}}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(order, ","); got != "SessionStart,UserPromptSubmit" {
		t.Fatalf("hook order = %q", got)
	}
}

func TestCodexSessionStartContinueFalseStopsFirstTurn(t *testing.T) {
	a := &Agent{Config: &Config{Hooks: hook.Hooks{
		SessionStart: []hook.SessionStart{func(context.Context, string) (hook.Outcome, error) {
			return hook.Outcome{Stop: true, Reason: "maintenance"}, nil
		}},
	}}}
	if _, err := a.Send(context.Background(), []Content{{Text: "hello"}}); err == nil || err.Error() != "maintenance" {
		t.Fatalf("Send error = %v, want maintenance", err)
	}
	if a.Running() {
		t.Fatal("agent remained running after SessionStart stopped it")
	}
}

func TestCodexPostToolBlockReplacesOriginalResult(t *testing.T) {
	a := &Agent{Config: &Config{Hooks: hook.Hooks{
		PostToolUse: []hook.PostToolUse{func(context.Context, tool.ToolCall, string) (hook.Outcome, error) {
			return hook.Outcome{Block: true, Reason: "review the generated files"}, nil
		}},
	}}}
	got := a.runSingleToolCall(context.Background(), ToolCall{Name: "build"}, []tool.Tool{{
		Name: "build",
		Execute: func(context.Context, map[string]any) (string, error) {
			return "original output that must be hidden", nil
		},
	}})
	if got != "review the generated files" {
		t.Fatalf("tool result = %q", got)
	}
}

func TestCompleteRejectsStreamWithoutTerminalEvent(t *testing.T) {
	client := streamingTestClient(func(*http.Request) string {
		return "data: {\"type\":\"response.created\",\"sequence_number\":1,\"response\":{}}\n\ndata: [DONE]\n\n"
	})

	_, err := complete(context.Background(), &client, &request{}, yieldAll)
	if err == nil {
		t.Fatal("expected an incomplete stream error")
	}
	if !isRecoverableError(err) {
		t.Fatalf("pre-output incomplete stream should be retryable: %v", err)
	}
}

func TestCompleteRetriesStreamWithoutTerminalAfterOutput(t *testing.T) {
	client := streamingTestClient(func(*http.Request) string {
		return "data: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"delta\":\"partial\"}\n\ndata: [DONE]\n\n"
	})

	_, err := complete(context.Background(), &client, &request{}, yieldAll)
	if err == nil {
		t.Fatal("expected an incomplete stream error")
	}
	if !isRecoverableError(err) {
		t.Fatalf("post-output incomplete stream should be retryable: %v", err)
	}
}
