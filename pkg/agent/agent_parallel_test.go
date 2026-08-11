package agent

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func yieldAll(Message, error) bool { return true }

func TestProcessToolCallsRunsReadsConcurrently(t *testing.T) {
	var mu sync.Mutex
	started := 0
	release := make(chan struct{})

	readExec := func(ctx context.Context, args map[string]any) (tool.Result, error) {
		mu.Lock()
		started++
		if started == 2 {
			close(release)
		}
		mu.Unlock()

		select {
		case <-release:
			return tool.Text("ok"), nil
		case <-ctx.Done():
			return tool.Result{}, ctx.Err()
		}
	}

	tools := []tool.Tool{
		{Name: "read", Effect: tool.StaticEffect(tool.EffectReadOnly), Execute: readExec},
	}

	a := &Agent{Config: &Config{ToolTimeout: 2 * time.Second}}
	calls := []ToolCall{{ID: "1", Name: "read"}, {ID: "2", Name: "read"}}

	if err := a.processToolCalls(context.Background(), calls, tools, yieldAll); err != nil {
		t.Fatal(err)
	}

	for _, m := range a.Messages {
		for _, c := range m.Content {
			if c.ToolResult != nil && strings.HasPrefix(c.ToolResult.Content, "error") {
				t.Fatalf("reads did not overlap: %s", c.ToolResult.Content)
			}
		}
	}
}

func TestProcessToolCallsOrdersMixedSegments(t *testing.T) {
	var mu sync.Mutex
	var order []string

	rec := func(name string) func(context.Context, map[string]any) (tool.Result, error) {
		return func(context.Context, map[string]any) (tool.Result, error) {
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			return tool.Text("ok"), nil
		}
	}

	tools := []tool.Tool{
		{Name: "read", Effect: tool.StaticEffect(tool.EffectReadOnly), Execute: rec("read")},
		{Name: "write", Effect: tool.StaticEffect(tool.EffectMutates), Execute: rec("write")},
	}

	a := &Agent{Config: &Config{}}
	calls := []ToolCall{
		{ID: "1", Name: "read"},
		{ID: "2", Name: "read"},
		{ID: "3", Name: "write"},
		{ID: "4", Name: "read"},
	}

	if err := a.processToolCalls(context.Background(), calls, tools, yieldAll); err != nil {
		t.Fatal(err)
	}

	if len(order) != 4 || order[2] != "write" || order[0] != "read" || order[1] != "read" || order[3] != "read" {
		t.Fatalf("expected [read read write read] segments, got %v", order)
	}

	var ids []string
	for _, m := range a.Messages {
		for _, c := range m.Content {
			if c.ToolResult != nil {
				ids = append(ids, c.ToolResult.ID)
			}
		}
	}
	if want := []string{"1", "2", "3", "4"}; !slices.Equal(ids, want) {
		t.Fatalf("result message order %v, want %v", ids, want)
	}
}

func TestDuplicateToolCallIDExecutesOnce(t *testing.T) {
	var calls int
	release := make(chan struct{})
	started := make(chan struct{})
	tl := tool.Tool{
		Name:   "write",
		Effect: tool.StaticEffect(tool.EffectMutates),
		Execute: func(context.Context, map[string]any) (tool.Result, error) {
			calls++
			close(started)
			<-release
			return tool.Text("written"), nil
		},
	}
	a := &Agent{Config: &Config{ToolTimeout: -1}}
	tc := ToolCall{ID: "same-call", Name: "write", Args: `{"path":"a"}`}

	results := make(chan tool.Result, 2)
	go func() { results <- a.runSingleToolCall(context.Background(), tc, []tool.Tool{tl}) }()
	<-started
	go func() { results <- a.runSingleToolCall(context.Background(), tc, []tool.Tool{tl}) }()
	close(release)

	first, second := <-results, <-results
	if calls != 1 || first.Content != "written" || second.Content != "written" {
		t.Fatalf("calls=%d results=(%+v, %+v)", calls, first, second)
	}
}

func TestRetainedToolResultPreventsReplayAfterRestore(t *testing.T) {
	a := &Agent{
		Config: &Config{ToolTimeout: -1},
		Messages: []Message{{Role: RoleAssistant, Content: []Content{{ToolResult: &ToolResult{
			ID: "persisted", Name: "write", Args: `{"path":"a"}`, Content: "already written",
		}}}}},
	}
	called := false
	tl := tool.Tool{Name: "write", Execute: func(context.Context, map[string]any) (tool.Result, error) {
		called = true
		return tool.Text("replayed"), nil
	}}

	result := a.runSingleToolCall(context.Background(), ToolCall{
		ID: "persisted", Name: "write", Args: `{"path":"a"}`,
	}, []tool.Tool{tl})
	if called || result.Content != "already written" {
		t.Fatalf("called=%v result=%+v", called, result)
	}
}

func TestRetainedImageResultRestoresAttachedData(t *testing.T) {
	a := &Agent{
		Config: &Config{ToolTimeout: -1},
		Messages: []Message{{Role: RoleAssistant, Content: []Content{
			{ToolResult: &ToolResult{ID: "image", Name: "view_image", Content: "[image attached below]"}},
			{File: &File{Data: "data:image/png;base64,abc"}},
		}}},
	}

	result := a.runSingleToolCall(context.Background(), ToolCall{ID: "image", Name: "view_image"}, nil)
	if result.Content != "data:image/png;base64,abc" {
		t.Fatalf("restored image = %q", result.Content)
	}
}

func TestToolResultMessagePreservesStructuredResult(t *testing.T) {
	message := toolResultMessage(ToolCall{ID: "call", Name: "shell"}, tool.Result{
		Content: "failed", IsError: true, Metadata: map[string]any{"exit_code": 7},
	})
	result := message.Content[0].ToolResult
	if result == nil || !result.IsError || result.Content != "failed" || result.Metadata["exit_code"] != 7 {
		t.Fatalf("tool result = %+v", result)
	}
}

func TestToolCallIDReuseWithDifferentArgumentsFails(t *testing.T) {
	a := &Agent{Config: &Config{ToolTimeout: -1}}
	tl := tool.Tool{Name: "write", Execute: func(context.Context, map[string]any) (tool.Result, error) {
		return tool.Text("ok"), nil
	}}
	tools := []tool.Tool{tl}
	first := a.runSingleToolCall(context.Background(), ToolCall{ID: "reused", Name: "write", Args: `{"v":1}`}, tools)
	second := a.runSingleToolCall(context.Background(), ToolCall{ID: "reused", Name: "write", Args: `{"v":2}`}, tools)
	if first.IsError || !second.IsError || !strings.Contains(second.Content, "reused") {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}
