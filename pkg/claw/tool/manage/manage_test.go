package manage

import (
	"context"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/schedule"
	"github.com/adrianliechti/wingman-agent/pkg/claw/memory"
)

type fakeManager struct {
	created []string
	deleted []string
}

func (m *fakeManager) CreateAgent(name string) error {
	m.created = append(m.created, name)
	return nil
}

func (m *fakeManager) DeleteAgent(name string) error {
	m.deleted = append(m.deleted, name)
	return nil
}

func TestManageToolsExposeEffects(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	tools := Tools(&fakeManager{}, store)

	if tools[0].Effect == nil {
		t.Fatal("create_agent effect is nil")
	}
	if got := tools[0].Effect(nil); got != tool.EffectMutates {
		t.Fatalf("create_agent effect = %v, want mutates", got)
	}
	if tools[1].Effect == nil {
		t.Fatal("delete_agent effect is nil")
	}
	if got := tools[1].Effect(nil); got != tool.EffectDangerous {
		t.Fatalf("delete_agent effect = %v, want dangerous", got)
	}
}

func TestCreateAgentRejectsInvalidScheduledTask(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	manager := &fakeManager{}
	createAgent := Tools(manager, store)[0]

	_, err = createAgent.Execute(context.Background(), map[string]any{
		"name": "worker",
		"tasks": []any{
			map[string]any{"prompt": "run", "schedule": "every 0s"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "duration must be positive") {
		t.Fatalf("expected invalid schedule error, got: %v", err)
	}
	if len(manager.created) != 0 {
		t.Fatalf("CreateAgent called despite invalid task: %#v", manager.created)
	}
}

func TestCreateAgentSavesTrimmedTasks(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	manager := &fakeManager{}
	createAgent := Tools(manager, store)[0]

	_, err = createAgent.Execute(context.Background(), map[string]any{
		"name": "worker",
		"tasks": []any{
			map[string]any{"prompt": " run ", "schedule": " every 1h "},
		},
	})
	if err != nil {
		t.Fatalf("create_agent failed: %v", err)
	}

	tasks, err := schedule.NewFileStore(store.AgentDir("worker")).List()
	if err != nil || len(tasks) != 1 || tasks[0].Prompt != "run" || tasks[0].Schedule != "every 1h" {
		t.Fatalf("tasks = %#v, %v, want one trimmed task", tasks, err)
	}
}
