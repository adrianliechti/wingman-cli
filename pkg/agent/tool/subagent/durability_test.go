package subagent

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/task"
)

func TestToolsRebindRestoredSubagentIdentityAndContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.json")
	registry, err := task.NewFileRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	spec := durableSpec{
		Version: durableSpecVersion, AgentID: "stable-agent-id", AgentType: "explore",
		Instructions: "restored instructions", Model: "test-model", Effort: "low",
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	message := agent.Message{Role: agent.RoleUser, Content: []agent.Content{{Text: "remember this"}}}
	state := agent.State{Events: []agent.RuntimeEvent{{
		Sequence: 1, ID: "message", Type: agent.EventMessage, At: time.Now().UTC(), Message: &message,
	}}}
	created, err := registry.AdoptAgent(spec.AgentID, "restored", spec.AgentType, "done", time.Second, func(tk *task.Task) error {
		return tk.SetDurableAgent(spec.AgentID, state, raw)
	})
	if err != nil || created == nil {
		t.Fatalf("adopt = %#v, %v", created, err)
	}
	registry.Close()

	restored, err := task.NewFileRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	cfg, err := agent.DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	_ = Tools(cfg, nil, restored)

	got := restored.Get(created.ID)
	if got == nil || got.AgentID != spec.AgentID || !got.SupportsResume() {
		t.Fatalf("restored task was not rebound: %#v", got)
	}
	messages := got.PeekMessages()
	if len(messages) != 1 || messages[0].Content[0].Text != "remember this" {
		t.Fatalf("restored context = %#v", messages)
	}
}
