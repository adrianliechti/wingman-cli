package shell

import (
	"context"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func TestConfirmSandboxEscalationNoGateDoesNotEscalate(t *testing.T) {
	appr := NewApprovals()

	approved, err := confirmSandboxEscalation(context.Background(), nil, appr, "touch /etc/x", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if approved {
		t.Fatal("escalation must not be silently approved without a confirmation gate")
	}
}

func TestConfirmSandboxEscalationApprovedAndRemembered(t *testing.T) {
	calls := 0
	elicit := &tool.Elicitation{
		Confirm: func(ctx context.Context, message string) (bool, error) {
			calls++
			if want := "Blocked by the workspace sandbox. Retry without it?"; !strings.Contains(message, want) {
				t.Fatalf("prompt = %q, want it to contain %q", message, want)
			}
			return true, nil
		},
	}
	appr := NewApprovals()

	for i := 0; i < 2; i++ {
		approved, err := confirmSandboxEscalation(context.Background(), elicit, appr, "touch /outside/x", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !approved {
			t.Fatal("expected escalation to be approved")
		}
	}
	if calls != 1 {
		t.Fatalf("confirm called %d times, want 1 (second call should be remembered)", calls)
	}
}

func TestConfirmSandboxEscalationDeclined(t *testing.T) {
	elicit := &tool.Elicitation{
		Confirm: func(ctx context.Context, message string) (bool, error) {
			return false, nil
		},
	}
	appr := NewApprovals()

	approved, err := confirmSandboxEscalation(context.Background(), elicit, appr, "touch /outside/x", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if approved {
		t.Fatal("expected escalation to be declined")
	}

	// A decline must not be remembered — the next attempt should ask again.
	calls := 0
	elicit.Confirm = func(ctx context.Context, message string) (bool, error) {
		calls++
		return false, nil
	}
	if _, err := confirmSandboxEscalation(context.Background(), elicit, appr, "touch /outside/x", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("confirm called %d times, want 1 (decline should not be remembered)", calls)
	}
}

func TestConfirmSandboxEscalationDistinguishesWorkdir(t *testing.T) {
	calls := 0
	elicit := &tool.Elicitation{
		Confirm: func(ctx context.Context, message string) (bool, error) {
			calls++
			return true, nil
		},
	}
	appr := NewApprovals()

	if _, err := confirmSandboxEscalation(context.Background(), elicit, appr, "touch x", "/a"); err != nil {
		t.Fatal(err)
	}
	if _, err := confirmSandboxEscalation(context.Background(), elicit, appr, "touch x", "/b"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("confirm called %d times, want 2 (different workdirs must prompt separately)", calls)
	}
}
