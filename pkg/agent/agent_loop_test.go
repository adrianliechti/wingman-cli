package agent

import (
	"context"
	"encoding/json"
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
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/adrianliechti/wingman-agent/pkg/agent/hook"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/telemetry"
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

func TestSendEmitsGenAITelemetry(t *testing.T) {
	var requests atomic.Int64
	client := streamingTestClient(func(*http.Request) string {
		if requests.Add(1) == 1 {
			return "data: {\"type\":\"response.output_item.done\",\"sequence_number\":1,\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_1\",\"name\":\"echo\",\"arguments\":\"{}\",\"status\":\"completed\"}}\n\n" +
				"data: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-test-2026\",\"usage\":{\"input_tokens\":10,\"input_tokens_details\":{\"cached_tokens\":2},\"output_tokens\":3}}}\n\n"
		}
		return "data: {\"type\":\"response.completed\",\"sequence_number\":1,\"response\":{\"id\":\"resp_2\",\"model\":\"gpt-test-2026\",\"output\":[{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"done\",\"annotations\":[]}]}],\"usage\":{\"input_tokens\":12,\"input_tokens_details\":{\"cached_tokens\":4},\"output_tokens\":2}}}\n\n"
	})

	recorder := tracetest.NewSpanRecorder()
	provider := trace.NewTracerProvider(trace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	tel, err := telemetry.New(context.Background(), telemetry.Options{
		AgentName:             "code-agent",
		ProviderName:          "openai",
		TracerProvider:        provider,
		DisableMetrics:        true,
		CaptureMessageContent: telemetry.ContentCaptureSpanOnly,
	})
	if err != nil {
		t.Fatalf("create telemetry: %v", err)
	}

	a := &Agent{Config: &Config{
		client:       &client,
		Telemetry:    tel,
		Model:        func() string { return "gpt-test" },
		Instructions: func() string { return "Be helpful" },
		Tools: func() []tool.Tool {
			return []tool.Tool{{
				Name:        "echo",
				Description: "Echo a value",
				Execute: func(context.Context, map[string]any) (tool.Result, error) {
					return tool.Text("ok"), nil
				},
			}}
		},
	}}
	ctx := hook.WithRuntime(context.Background(), hook.Runtime{SessionID: "session-42"})
	stream, err := a.Send(ctx, []Content{{Text: "start"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range stream {
		if err != nil {
			t.Fatal(err)
		}
	}

	spans := recorder.Ended()
	var agentSpan trace.ReadOnlySpan
	var inferenceCount, toolCount int
	finishReasons := map[string][]string{}
	for _, span := range spans {
		switch span.Name() {
		case "invoke_agent code-agent":
			agentSpan = span
		case "chat gpt-test":
			inferenceCount++
			if responseID, ok := spanStringAttribute(span, "gen_ai.response.id"); ok {
				if reasons, ok := spanStringSliceAttribute(span, "gen_ai.response.finish_reasons"); ok {
					finishReasons[responseID] = reasons
				}
			}
			for _, name := range []string{
				"gen_ai.input.messages",
				"gen_ai.output.messages",
				"gen_ai.system_instructions",
				"gen_ai.tool.definitions",
			} {
				if !hasSpanAttribute(span, name) {
					t.Errorf("inference span is missing captured attribute %q", name)
				}
			}
		case "execute_tool echo":
			toolCount++
		}
	}
	if agentSpan == nil || inferenceCount != 2 || toolCount != 1 {
		t.Fatalf("spans = %v, want one agent, two inference, and one tool span", spanNames(spans))
	}
	if got, ok := spanInt64Attribute(agentSpan, "gen_ai.usage.input_tokens"); !ok || got != 22 {
		t.Errorf("agent input tokens = %d (present %v), want 22", got, ok)
	}
	if got, ok := spanInt64Attribute(agentSpan, "gen_ai.usage.output_tokens"); !ok || got != 5 {
		t.Errorf("agent output tokens = %d (present %v), want 5", got, ok)
	}
	if got := finishReasons["resp_1"]; !slices.Equal(got, []string{"tool_call"}) {
		t.Errorf("first inference finish reasons = %v, want [tool_call]", got)
	}
	if got := finishReasons["resp_2"]; !slices.Equal(got, []string{"stop"}) {
		t.Errorf("second inference finish reasons = %v, want [stop]", got)
	}
	for _, span := range spans {
		if span == agentSpan {
			continue
		}
		if span.Parent().SpanID() != agentSpan.SpanContext().SpanID() {
			t.Errorf("span %q is not a direct child of the agent span", span.Name())
		}
	}
}

func spanInt64Attribute(span trace.ReadOnlySpan, name string) (int64, bool) {
	for _, attr := range span.Attributes() {
		if string(attr.Key) == name {
			return attr.Value.AsInt64(), true
		}
	}
	return 0, false
}

func spanStringAttribute(span trace.ReadOnlySpan, name string) (string, bool) {
	for _, attr := range span.Attributes() {
		if string(attr.Key) == name {
			return attr.Value.AsString(), true
		}
	}
	return "", false
}

func spanStringSliceAttribute(span trace.ReadOnlySpan, name string) ([]string, bool) {
	for _, attr := range span.Attributes() {
		if string(attr.Key) == name {
			return attr.Value.AsStringSlice(), true
		}
	}
	return nil, false
}

func hasSpanAttribute(span trace.ReadOnlySpan, name string) bool {
	for _, attr := range span.Attributes() {
		if string(attr.Key) == name {
			return true
		}
	}
	return false
}

func spanNames(spans []trace.ReadOnlySpan) []string {
	names := make([]string, len(spans))
	for i, span := range spans {
		names[i] = span.Name()
	}
	return names
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
				Execute: func(context.Context, map[string]any) (tool.Result, error) {
					executions.Add(1)
					return tool.Text("again"), nil
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

func TestCompleteStreamsPartialToolCalls(t *testing.T) {
	const args = "{\\\"items\\\":[{\\\"content\\\":\\\"Fix\\\",\\\"status\\\":\\\"pending\\\"}]}"

	client := streamingTestClient(func(*http.Request) string {
		return "data: {\"type\":\"response.output_item.added\",\"sequence_number\":1,\"output_index\":0,\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"todo\",\"arguments\":\"{\\\"items\\\":[\",\"status\":\"in_progress\"}}\n\n" +
			"data: {\"type\":\"response.function_call_arguments.done\",\"sequence_number\":3,\"output_index\":0,\"item_id\":\"fc_1\",\"name\":\"todo\",\"arguments\":\"" + args + "\"}\n\n" +
			"data: {\"type\":\"response.output_item.done\",\"sequence_number\":4,\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_1\",\"name\":\"todo\",\"arguments\":\"" + args + "\",\"status\":\"completed\"}}\n\n" +
			"data: {\"type\":\"response.completed\",\"sequence_number\":5,\"response\":{\"usage\":{\"input_tokens\":1,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":1}}}\n\n"
	})

	var partials []ToolCall
	resp, err := complete(context.Background(), &client, &request{}, func(m Message, err error) bool {
		for _, c := range m.Content {
			if c.ToolCall != nil {
				partials = append(partials, *c.ToolCall)
			}
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(partials) < 2 {
		t.Fatalf("partial tool calls = %v, want announcement and completion", partials)
	}
	first, last := partials[0], partials[len(partials)-1]
	if !first.Partial || first.ID != "call_1" || first.Name != "todo" || first.Args != `{"items":[` {
		t.Fatalf("announcement = %+v", first)
	}
	wantArgs := `{"items":[{"content":"Fix","status":"pending"}]}`
	if !last.Partial || last.ID != "call_1" || last.Args != wantArgs {
		t.Fatalf("completion snapshot = %+v", last)
	}

	calls := extractToolCalls(resp.messages)
	if len(calls) != 1 || calls[0].Partial || calls[0].ID != "call_1" || calls[0].Args != wantArgs {
		t.Fatalf("committed calls = %+v", calls)
	}
}

func TestCompleteTracksInterleavedPartialCallsByOutputIndex(t *testing.T) {
	client := streamingTestClient(func(*http.Request) string {
		return "data: {\"type\":\"response.output_item.added\",\"sequence_number\":1,\"output_index\":0,\"item\":{\"type\":\"function_call\",\"call_id\":\"call_a\",\"name\":\"shell\",\"arguments\":\"\",\"status\":\"in_progress\"}}\n\n" +
			"data: {\"type\":\"response.output_item.added\",\"sequence_number\":2,\"output_index\":1,\"item\":{\"type\":\"function_call\",\"call_id\":\"call_b\",\"name\":\"read\",\"arguments\":\"{\\\"path\\\":\\\"\",\"status\":\"in_progress\"}}\n\n" +
			"data: {\"type\":\"response.function_call_arguments.delta\",\"sequence_number\":3,\"output_index\":0,\"item_id\":\"item_a\",\"delta\":\"{\\\"command\\\":\\\"ls\\\"}\"}\n\n" +
			"data: {\"type\":\"response.function_call_arguments.done\",\"sequence_number\":4,\"output_index\":1,\"item_id\":\"item_b\",\"name\":\"read\",\"arguments\":\"{\\\"path\\\":\\\"b.go\\\"}\"}\n\n" +
			"data: {\"type\":\"response.function_call_arguments.done\",\"sequence_number\":5,\"output_index\":0,\"item_id\":\"item_a\",\"name\":\"shell\",\"arguments\":\"{\\\"command\\\":\\\"ls\\\"}\"}\n\n" +
			"data: {\"type\":\"response.output_item.done\",\"sequence_number\":6,\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"item_a\",\"call_id\":\"call_a\",\"name\":\"shell\",\"arguments\":\"{\\\"command\\\":\\\"ls\\\"}\",\"status\":\"completed\"}}\n\n" +
			"data: {\"type\":\"response.output_item.done\",\"sequence_number\":7,\"output_index\":1,\"item\":{\"type\":\"function_call\",\"id\":\"item_b\",\"call_id\":\"call_b\",\"name\":\"read\",\"arguments\":\"{\\\"path\\\":\\\"b.go\\\"}\",\"status\":\"completed\"}}\n\n" +
			"data: {\"type\":\"response.completed\",\"sequence_number\":8,\"response\":{\"usage\":{\"input_tokens\":1,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":1}}}\n\n"
	})

	latest := map[string]string{}
	resp, err := complete(context.Background(), &client, &request{}, func(m Message, _ error) bool {
		for _, c := range m.Content {
			if c.ToolCall != nil {
				latest[c.ToolCall.ID] = c.ToolCall.Args
			}
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if latest["call_a"] != `{"command":"ls"}` || latest["call_b"] != `{"path":"b.go"}` {
		t.Fatalf("partial calls were mixed: %v", latest)
	}
	calls := extractToolCalls(resp.messages)
	if len(calls) != 2 || calls[0].ID != "call_a" || calls[1].ID != "call_b" {
		t.Fatalf("committed calls = %+v", calls)
	}
}

func TestCompleteDoesNotCommitIncompleteToolCall(t *testing.T) {
	client := streamingTestClient(func(*http.Request) string {
		return "data: {\"type\":\"response.output_item.added\",\"sequence_number\":1,\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_1\",\"name\":\"write\",\"arguments\":\"\",\"status\":\"in_progress\"}}\n\n" +
			"data: {\"type\":\"response.function_call_arguments.delta\",\"sequence_number\":2,\"output_index\":0,\"item_id\":\"fc_1\",\"delta\":\"{\\\"file_path\\\":\\\"main.go\\\"\"}\n\n" +
			"data: {\"type\":\"response.incomplete\",\"sequence_number\":3,\"response\":{\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"},\"output\":[{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_1\",\"name\":\"write\",\"arguments\":\"{\\\"file_path\\\":\\\"main.go\\\"\",\"status\":\"incomplete\"}],\"usage\":{\"input_tokens\":1,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":1}}}\n\n"
	})

	partialSeen := false
	resp, err := complete(context.Background(), &client, &request{}, func(m Message, err error) bool {
		for _, c := range m.Content {
			partialSeen = partialSeen || c.ToolCall != nil && c.ToolCall.Partial
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if !partialSeen || !resp.incomplete {
		t.Fatalf("partialSeen=%v incomplete=%v", partialSeen, resp.incomplete)
	}
	if calls := extractToolCalls(resp.messages); len(calls) != 0 {
		t.Fatalf("incomplete tool call was committed: %+v", calls)
	}
}

func TestPendingToolCallSnapshotNeedsTimeAndGrowth(t *testing.T) {
	now := time.Now()
	pending := pendingToolCall{
		args:      make([]byte, partialArgsMinGrowth),
		lastYield: now,
	}

	if pending.snapshotReady(now.Add(partialArgsInterval - time.Millisecond)) {
		t.Fatal("snapshot ignored time throttle")
	}
	if !pending.snapshotReady(now.Add(partialArgsInterval)) {
		t.Fatal("first useful snapshot was not ready")
	}

	pending.markSnapshot(now.Add(partialArgsInterval))
	pending.args = append(pending.args, make([]byte, partialArgsMinGrowth-1)...)
	if pending.snapshotReady(now.Add(time.Second)) {
		t.Fatal("snapshot ignored growth throttle")
	}
	pending.args = append(pending.args, 0)
	if !pending.snapshotReady(now.Add(time.Second)) {
		t.Fatal("snapshot was not ready after enough growth")
	}

	pending.lastYieldSize = 4096
	pending.args = make([]byte, 4096+1023)
	if pending.snapshotReady(now.Add(time.Second)) {
		t.Fatal("large snapshot did not scale its growth threshold")
	}
	pending.args = append(pending.args, 0)
	if !pending.snapshotReady(now.Add(time.Second)) {
		t.Fatal("large snapshot was not ready after proportional growth")
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

func TestSendUsesStrictOutputSchemaWithoutTools(t *testing.T) {
	var requestBody map[string]any
	client := streamingTestClient(func(r *http.Request) string {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &requestBody); err != nil {
			t.Fatal(err)
		}
		return "data: {\"type\":\"response.completed\",\"sequence_number\":1,\"response\":{\"output\":[],\"usage\":{\"input_tokens\":1,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":1}}}\n\n"
	})

	a := &Agent{Config: &Config{
		client: &client,
		Tools: func() []tool.Tool {
			return []tool.Tool{{Name: "read", Parameters: map[string]any{"type": "object"}}}
		},
	}}
	ctx := WithOutputSchema(context.Background(), map[string]any{
		"type": "object",
	})
	stream, err := a.Send(ctx, []Content{{Text: "format the result"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range stream {
		if err != nil {
			t.Fatal(err)
		}
	}
	text, ok := requestBody["text"].(map[string]any)
	if !ok {
		t.Fatalf("text = %#v", requestBody["text"])
	}
	format, ok := text["format"].(map[string]any)
	if !ok || format["type"] != "json_schema" || format["name"] != "response" || format["strict"] != true {
		t.Fatalf("text.format = %#v", text["format"])
	}
	if tools, present := requestBody["tools"]; present {
		if list, ok := tools.([]any); !ok || len(list) != 0 {
			t.Fatalf("tools = %#v, want none during structured finalization", tools)
		}
	}
}

func TestSendUsesJSONObjectOutputForEmptySchema(t *testing.T) {
	var requestBody map[string]any
	client := streamingTestClient(func(r *http.Request) string {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &requestBody); err != nil {
			t.Fatal(err)
		}
		return "data: {\"type\":\"response.completed\",\"sequence_number\":1,\"response\":{\"output\":[],\"usage\":{\"input_tokens\":1,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":1}}}\n\n"
	})

	a := &Agent{Config: &Config{
		client: &client,
		Tools: func() []tool.Tool {
			return []tool.Tool{{Name: "read", Parameters: map[string]any{"type": "object"}}}
		},
	}}
	stream, err := a.Send(WithOutputSchema(context.Background(), map[string]any{}), []Content{{Text: "format the result"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range stream {
		if err != nil {
			t.Fatal(err)
		}
	}
	text, ok := requestBody["text"].(map[string]any)
	if !ok {
		t.Fatalf("text = %#v", requestBody["text"])
	}
	format, ok := text["format"].(map[string]any)
	if !ok || format["type"] != "json_object" {
		t.Fatalf("text.format = %#v", text["format"])
	}
	if tools, present := requestBody["tools"]; present {
		if list, ok := tools.([]any); !ok || len(list) != 0 {
			t.Fatalf("tools = %#v, want none during structured finalization", tools)
		}
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

	if err := a.recordEvents(RuntimeEvent{Type: EventTurnStarted, TurnID: "turn"}); err != nil {
		t.Fatal(err)
	}
	if err := a.finishTurn("turn", RuntimeInterrupted, context.Canceled, Usage{}); err != nil {
		t.Fatal(err)
	}

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

	execute := func(context.Context, map[string]any) (tool.Result, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			old := peak.Load()
			if current <= old || peak.CompareAndSwap(old, current) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		return tool.Text("ok"), nil
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
	if !strings.Contains(got.Content, "no executor") {
		t.Fatalf("result = %q", got.Content)
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
		Execute: func(ctx context.Context, _ map[string]any) (tool.Result, error) {
			return tool.Result{}, ctx.Err()
		},
	}})
	if !strings.Contains(got.Content, "10ms time limit") {
		t.Fatalf("result = %q", got.Content)
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
		Execute: func(context.Context, map[string]any) (tool.Result, error) {
			return tool.Text("original output that must be hidden"), nil
		},
	}})
	if got.Content != "review the generated files" {
		t.Fatalf("tool result = %q", got.Content)
	}
}

func TestToolResultHasHarnessLevelSizeLimit(t *testing.T) {
	input := "HEAD" + strings.Repeat("☃", maxInlineToolResultBytes) + "TAIL"
	a := &Agent{Config: &Config{}}
	got := a.runSingleToolCall(context.Background(), ToolCall{Name: "unbounded"}, []tool.Tool{{
		Name: "unbounded",
		Execute: func(context.Context, map[string]any) (tool.Result, error) {
			return tool.Text(input), nil
		},
	}})
	if !strings.Contains(got.Content, "truncated by the agent harness") {
		t.Fatalf("tool result did not report truncation")
	}
	if !strings.Contains(got.Content, "HEAD") || !strings.Contains(got.Content, "TAIL") {
		t.Fatalf("tool result did not preserve head and tail")
	}
	if len(got.Content) >= len(input) {
		t.Fatalf("bounded result length = %d, input length = %d", len(got.Content), len(input))
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
