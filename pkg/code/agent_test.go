package code

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/shell"
)

func TestPlanModeToolsFilterMutations(t *testing.T) {
	calledShell := false
	tools := planModeTools([]tool.Tool{
		{Name: "read", Effect: tool.StaticEffect(tool.EffectReadOnly)},
		{Name: "edit", Effect: tool.StaticEffect(tool.EffectMutates)},
		{Name: "write", Effect: tool.StaticEffect(tool.EffectMutates)},
		{
			Name:   "shell",
			Effect: shell.ClassifyEffect,
			Execute: func(ctx context.Context, args map[string]any) (string, error) {
				calledShell = true
				return "ok", nil
			},
		},
	})

	names := make(map[string]bool)
	for _, t := range tools {
		names[t.Name] = true
	}

	if names["edit"] || names["write"] {
		t.Fatalf("plan mode should remove edit/write tools, got names: %#v", names)
	}
	if !names["read"] || !names["shell"] {
		t.Fatalf("plan mode should keep read/shell tools, got names: %#v", names)
	}

	var shellTool tool.Tool
	for _, t := range tools {
		if t.Name == "shell" {
			shellTool = t
			break
		}
	}
	if shellTool.Execute == nil {
		t.Fatal("shell tool missing Execute")
	}

	if _, err := shellTool.Execute(context.Background(), map[string]any{"command": "git status"}); err != nil {
		t.Fatalf("safe shell command rejected: %v", err)
	}
	if !calledShell {
		t.Fatal("safe shell command did not call wrapped shell execute")
	}

	calledShell = false
	_, err := shellTool.Execute(context.Background(), map[string]any{"command": "rm -rf tmp"})
	if err == nil || !strings.Contains(err.Error(), "plan mode only allows read-only tool calls") {
		t.Fatalf("unsafe shell command was not rejected with plan-mode error: %v", err)
	}
	if calledShell {
		t.Fatal("unsafe shell command reached wrapped shell execute")
	}
}

func TestPlanModeShellRejectsMutatingSafeCommands(t *testing.T) {
	execute := planModeEffectExecute(tool.Tool{
		Name:   "shell",
		Effect: shell.ClassifyEffect,
		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			return "ok", nil
		},
	})

	tests := []string{
		"echo hi > file.txt",
		"cat <<'EOF'\nhello\nEOF",
		"sed -i 's/a/b/' file.txt",
		"sed --in-place 's/a/b/' file.txt",
	}

	for _, command := range tests {
		t.Run(command, func(t *testing.T) {
			_, err := execute(context.Background(), map[string]any{"command": command})
			if err == nil || !strings.Contains(err.Error(), "plan mode only allows read-only tool calls") {
				t.Fatalf("command was not rejected with plan-mode error: %v", err)
			}
		})
	}
}

func TestMemoryContextMessagesOnlyInjectChanges(t *testing.T) {
	dir := t.TempDir()
	a := &Agent{MemoryPath: dir}

	if got := a.memoryContextMessages(); got != nil {
		t.Fatalf("initial empty memory should not inject, got %#v", got)
	}

	if err := os.WriteFile(filepath.Join(dir, memoryFileName), []byte("remember this"), 0644); err != nil {
		t.Fatal(err)
	}

	got := a.memoryContextMessages()
	if len(got) != 1 {
		t.Fatalf("memory change should inject one message, got %#v", got)
	}
	if !got[0].Hidden || got[0].Role != agent.RoleUser {
		t.Fatalf("memory context should be hidden user context, got %#v", got[0])
	}
	if got[0].Content[0].Text != memoryContextPrefix+"remember this" {
		t.Fatalf("unexpected memory context: %q", got[0].Content[0].Text)
	}

	if got := a.memoryContextMessages(); got != nil {
		t.Fatalf("unchanged memory should not inject, got %#v", got)
	}

	if err := os.WriteFile(filepath.Join(dir, memoryFileName), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	got = a.memoryContextMessages()
	if len(got) != 1 {
		t.Fatalf("cleared memory should inject one update, got %#v", got)
	}
	if !got[0].Hidden || got[0].Content[0].Text != memoryContextEmpty {
		t.Fatalf("unexpected cleared-memory context: %#v", got[0])
	}
}

func TestMemoryContextMessagesUsesSavedHiddenSnapshotOnResume(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, memoryFileName), []byte("remember this"), 0644); err != nil {
		t.Fatal(err)
	}

	a := &Agent{
		Agent:      &agent.Agent{},
		MemoryPath: dir,
	}
	a.Messages = []agent.Message{{
		Role:   agent.RoleUser,
		Hidden: true,
		Content: []agent.Content{{
			Text: memoryContextPrefix + "remember this",
		}},
	}}

	if got := a.memoryContextMessages(); got != nil {
		t.Fatalf("unchanged saved memory snapshot should not reinject, got %#v", got)
	}

	if err := os.WriteFile(filepath.Join(dir, memoryFileName), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}

	got := a.memoryContextMessages()
	if len(got) != 1 || got[0].Content[0].Text != memoryContextPrefix+"changed" {
		t.Fatalf("changed memory should inject new snapshot, got %#v", got)
	}
}
