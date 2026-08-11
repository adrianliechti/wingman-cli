package agent

import "testing"

func TestStateSnapshotIsIndependent(t *testing.T) {
	a := &Agent{
		Messages: []Message{{
			Role: RoleAssistant,
			Content: []Content{{
				Text:      "answer",
				Reasoning: &Reasoning{Summary: "thought"},
			}},
		}},
		Usage:    Usage{InputTokens: 10},
		Revision: 2,
	}

	snapshot := a.StateSnapshot()
	snapshot.Messages[0].Content[0].Text = "changed"
	snapshot.Messages[0].Content[0].Reasoning.Summary = "changed"
	snapshot.Usage.InputTokens = 99

	if a.Messages[0].Content[0].Text != "answer" || a.Messages[0].Content[0].Reasoning.Summary != "thought" {
		t.Fatalf("snapshot mutated agent messages: %+v", a.Messages)
	}
	if a.Usage.InputTokens != 10 || snapshot.Revision != 2 {
		t.Fatalf("snapshot state = %+v, agent usage = %+v", snapshot, a.Usage)
	}
}

func TestStateSnapshotClonesToolResultMetadata(t *testing.T) {
	a := &Agent{Messages: []Message{{Role: RoleAssistant, Content: []Content{{
		ToolResult: &ToolResult{ID: "call-1", Metadata: map[string]any{"exit_code": 7}},
	}}}}}

	snapshot := a.StateSnapshot()
	snapshot.Messages[0].Content[0].ToolResult.Metadata["exit_code"] = 0
	if got := a.Messages[0].Content[0].ToolResult.Metadata["exit_code"]; got != 7 {
		t.Fatalf("snapshot metadata mutated retained state: %v", got)
	}
}

func TestStateVersionDoesNotCloneHistory(t *testing.T) {
	a := &Agent{Messages: []Message{{Role: RoleUser}}, Revision: 3}
	messageCount, revision := a.StateVersion()
	if messageCount != 1 || revision != 3 {
		t.Fatalf("state version = (%d, %d), want (1, 3)", messageCount, revision)
	}
}
