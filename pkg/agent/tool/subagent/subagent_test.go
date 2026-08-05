package subagent_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	. "github.com/adrianliechti/wingman-agent/pkg/agent/tool/subagent"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/task"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func TestAgentToolSchemaIncludesTypedSubagentParameters(t *testing.T) {
	agentTool := Tools(&agent.Config{}, nil, nil)[0]

	required, ok := agentTool.Parameters["required"].([]string)
	if !ok {
		t.Fatalf("required has type %T", agentTool.Parameters["required"])
	}
	if !contains(required, "description") || !contains(required, "prompt") || !contains(required, "agent_type") {
		t.Fatalf("required = %#v, want description, prompt, and agent_type", required)
	}

	properties, ok := agentTool.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties has type %T", agentTool.Parameters["properties"])
	}

	subagentType, ok := properties["agent_type"].(map[string]any)
	if !ok {
		t.Fatalf("agent_type property has type %T", properties["agent_type"])
	}

	enum, ok := subagentType["enum"].([]string)
	if !ok {
		t.Fatalf("agent_type enum has type %T", subagentType["enum"])
	}
	for _, name := range []string{
		"general-purpose",
		"explore",
		"verification",
		"security",
		"code-architect",
		"code-reviewer",
		"code-simplifier",
		"test-engineer",
	} {
		if !contains(enum, name) {
			t.Fatalf("agent_type enum = %#v, missing %q", enum, name)
		}
	}
}

func TestAgentToolRejectsInvalidExecuteInputs(t *testing.T) {
	agentTool := Tools(&agent.Config{}, nil, nil)[0]

	cases := []struct {
		name    string
		args    map[string]any
		wantSub string
	}{
		{"missing description", map[string]any{"prompt": "p", "agent_type": "explore"}, "description is required"},
		{"blank description", map[string]any{"description": "   ", "prompt": "p", "agent_type": "explore"}, "description is required"},
		{"missing prompt", map[string]any{"description": "d", "agent_type": "explore"}, "prompt is required"},
		{"blank prompt", map[string]any{"description": "d", "prompt": "\t\n", "agent_type": "explore"}, "prompt is required"},
		{"missing agent_type", map[string]any{"description": "d", "prompt": "p"}, "agent_type is required"},
		{"unknown agent_type", map[string]any{"description": "d", "prompt": "p", "agent_type": "ninja"}, "unknown agent_type"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := agentTool.Execute(t.Context(), tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("err = %v, want error containing %q", err, tc.wantSub)
			}
		})
	}
}

func TestAgentToolClassifiesEffectByAgentType(t *testing.T) {
	agentTool := Tools(&agent.Config{}, nil, nil)[0]

	tests := []struct {
		name string
		args map[string]any
		want tool.Effect
	}{
		{"nil args", nil, tool.EffectDynamic},
		{"missing type", map[string]any{}, tool.EffectDynamic},
		{"general purpose", map[string]any{"agent_type": "general-purpose"}, tool.EffectMutates},
		{"explore", map[string]any{"agent_type": "explore"}, tool.EffectReadOnly},
		{"explore trims case", map[string]any{"agent_type": " Explore "}, tool.EffectReadOnly},
		{"verification", map[string]any{"agent_type": "verification"}, tool.EffectMutates},
		{"security", map[string]any{"agent_type": "security"}, tool.EffectReadOnly},
		{"code architect", map[string]any{"agent_type": "code-architect"}, tool.EffectReadOnly},
		{"code reviewer", map[string]any{"agent_type": "code-reviewer"}, tool.EffectReadOnly},
		{"code simplifier", map[string]any{"agent_type": "code-simplifier"}, tool.EffectMutates},
		{"test engineer", map[string]any{"agent_type": "test-engineer"}, tool.EffectMutates},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := agentTool.Effect(tt.args); got != tt.want {
				t.Fatalf("Effect() = %q, want %q", got, tt.want)
			}
		})
	}
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func TestAgentToolBackgroundValidation(t *testing.T) {
	reg := task.NewRegistry()
	defer reg.Close()

	withoutTasks := Tools(&agent.Config{}, nil, nil)[0]
	_, err := withoutTasks.Execute(t.Context(), map[string]any{
		"description": "d", "prompt": "p", "agent_type": "explore", "background": true,
	})
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("err = %v, want background-unavailable error", err)
	}

	withTasks := Tools(&agent.Config{}, nil, reg)[0]
	out, err := withTasks.Execute(t.Context(), map[string]any{
		"description": "d", "prompt": "p", "agent_type": "general-purpose", "background": true,
	})
	if err != nil {
		t.Fatalf("editing types must be backgroundable, got %v", err)
	}
	if !strings.Contains(out, "Launched background agent") {
		t.Fatalf("launch result = %q", out)
	}
	// The empty config has no model client, so the run dies — the registry
	// must contain that as a failed task instead of crashing the process.
	select {
	case done := <-reg.Events():
		if done.Status != task.StatusFailed {
			t.Fatalf("status = %s, want failed", done.Status)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no completion event for background general-purpose agent")
	}
}

func TestAgentToolBackgroundSchemaAndTaskTools(t *testing.T) {
	reg := task.NewRegistry()
	defer reg.Close()

	withoutTasks := Tools(&agent.Config{}, nil, nil)
	if len(withoutTasks) != 1 {
		t.Fatalf("tools without registry = %d, want 1", len(withoutTasks))
	}
	properties := withoutTasks[0].Parameters["properties"].(map[string]any)
	if _, ok := properties["background"]; ok {
		t.Fatal("background parameter advertised without a registry")
	}

	withTasks := Tools(&agent.Config{}, nil, reg)
	names := map[string]bool{}
	for _, tl := range withTasks {
		names[tl.Name] = true
	}
	if !names["agent"] || !names["task_output"] || !names["task_stop"] {
		t.Fatalf("tools with registry = %v, want agent, task_output, task_stop", names)
	}
	properties = withTasks[0].Parameters["properties"].(map[string]any)
	if _, ok := properties["background"]; !ok {
		t.Fatal("background parameter missing with a registry")
	}
}

func TestAgentToolBackgroundRunsDetached(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"sequence_number\":1,\"response\":{\"output\":[{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"background report\",\"annotations\":[]}]}],\"usage\":{\"input_tokens\":1,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":1}}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	t.Setenv("WINGMAN_URL", server.URL)
	cfg, err := agent.DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}

	reg := task.NewRegistry()
	defer reg.Close()

	byName := map[string]tool.Tool{}
	for _, tl := range Tools(cfg, nil, reg) {
		byName[tl.Name] = tl
	}

	out, err := byName["agent"].Execute(t.Context(), map[string]any{
		"description": "d", "prompt": "p", "agent_type": "explore", "background": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Launched background agent") {
		t.Fatalf("launch result = %q", out)
	}

	var done task.Event
	select {
	case done = <-reg.Events():
	case <-time.After(10 * time.Second):
		t.Fatal("no completion event")
	}

	if done.Status != task.StatusDone {
		t.Fatalf("status = %s, result = %q", done.Status, done.Result)
	}
	if !strings.Contains(done.Result, "background report") {
		t.Fatalf("result = %q", done.Result)
	}
	messages := done.Task.PeekMessages()
	if len(messages) == 0 {
		t.Fatal("peek returned no transcript")
	}
	found := false
	for _, m := range messages {
		for _, c := range m.Content {
			if strings.Contains(c.Text, "background report") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("peek transcript missing agent output: %+v", messages)
	}

	out, err = byName["task_send"].Execute(t.Context(), map[string]any{
		"id": done.Task.ID, "message": "and one more thing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "resumed") {
		t.Fatalf("task_send result = %q", out)
	}

	select {
	case reply := <-reg.Events():
		if reply.Task != done.Task {
			t.Fatal("follow-up completed as a different task")
		}
		if reply.Seq != 2 || reply.Status != task.StatusDone {
			t.Fatalf("seq = %d, status = %s", reply.Seq, reply.Status)
		}
		if !strings.Contains(reply.Result, "background report") {
			t.Fatalf("reply result = %q", reply.Result)
		}
		if strings.Count(reply.Result, "background report") != 1 {
			t.Fatalf("reply duplicates prior run output: %q", reply.Result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no reply event")
	}
}

func TestTaskSendValidation(t *testing.T) {
	reg := task.NewRegistry()
	defer reg.Close()

	var taskSend tool.Tool
	for _, tl := range Tools(&agent.Config{}, nil, reg) {
		if tl.Name == "task_send" {
			taskSend = tl
		}
	}
	if taskSend.Name == "" {
		t.Fatal("task_send tool missing")
	}

	if _, err := taskSend.Execute(t.Context(), map[string]any{"id": "nope", "message": "m"}); err == nil {
		t.Fatal("unknown id should error")
	}
	if _, err := taskSend.Execute(t.Context(), map[string]any{"id": "x"}); err == nil {
		t.Fatal("missing message should error")
	}
}

func TestAgentToolSyncRunAdoptedForFollowUps(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"sequence_number\":1,\"response\":{\"output\":[{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"sync report\",\"annotations\":[]}]}],\"usage\":{\"input_tokens\":1,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":1}}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	t.Setenv("WINGMAN_URL", server.URL)
	cfg, err := agent.DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}

	reg := task.NewRegistry()
	defer reg.Close()

	byName := map[string]tool.Tool{}
	for _, tl := range Tools(cfg, nil, reg) {
		byName[tl.Name] = tl
	}

	out, err := byName["agent"].Execute(t.Context(), map[string]any{
		"description": "d", "prompt": "p", "agent_type": "explore",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "sync report") {
		t.Fatalf("result = %q", out)
	}

	m := regexp.MustCompile(`\(id (\S+) — task_send`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("result missing follow-up id: %q", out)
	}
	id := m[1]

	adopted := reg.Get(id)
	if adopted == nil || adopted.Status() != task.StatusDone {
		t.Fatalf("adopted task = %+v", adopted)
	}
	if len(adopted.PeekMessages()) == 0 {
		t.Fatal("adopted task must expose its transcript")
	}

	if _, err := byName["task_send"].Execute(t.Context(), map[string]any{
		"id": id, "message": "and one more thing",
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case reply := <-reg.Events():
		if reply.ID != id || reply.Seq != 2 || reply.Status != task.StatusDone {
			t.Fatalf("reply = %+v", reply)
		}
		if strings.Count(reply.Result, "sync report") != 1 {
			t.Fatalf("reply result = %q", reply.Result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no reply event")
	}
}

func TestAgentToolSyncRunWithoutRegistryOmitsFollowUpID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"sequence_number\":1,\"response\":{\"output\":[{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"sync report\",\"annotations\":[]}]}],\"usage\":{\"input_tokens\":1,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":1}}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	t.Setenv("WINGMAN_URL", server.URL)
	cfg, err := agent.DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}

	out, err := Tools(cfg, nil, nil)[0].Execute(t.Context(), map[string]any{
		"description": "d", "prompt": "p", "agent_type": "explore",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "task_send") {
		t.Fatalf("no-registry result must not advertise task_send: %q", out)
	}
}
