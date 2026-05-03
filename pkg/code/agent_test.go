package code

import (
	"context"
	"strings"
	"testing"

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
