package shell

import (
	"context"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func TestApprovalDisplayMakesObfuscationVisible(t *testing.T) {
	var shown string
	elicit := &tool.Elicitation{Confirm: func(_ context.Context, message string) (bool, error) {
		shown = message
		return true, nil
	}}

	raw := "rm\t-rf /tmp/x\u202e"
	if err := confirmIfDangerous(context.Background(), elicit, NewApprovals(), raw, true); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(shown, '\t') || strings.ContainsRune(shown, '\u202e') {
		t.Fatalf("approval contains terminal-obfuscating characters: %q", shown)
	}
	if !strings.Contains(shown, `\t`) || !strings.Contains(shown, `\u{202E}`) {
		t.Fatalf("approval did not expose escaped characters: %q", shown)
	}
}

func TestApprovalKeysDoNotTrimCarriageReturn(t *testing.T) {
	calls := 0
	elicit := &tool.Elicitation{Confirm: func(_ context.Context, _ string) (bool, error) {
		calls++
		return true, nil
	}}
	appr := NewApprovals()

	for _, command := range []string{"sudo id\r", "sudo id"} {
		if err := confirmIfDangerous(context.Background(), elicit, appr, command, true); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("confirm called %d times, want 2 distinct approvals", calls)
	}
}
