package shell

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

type Approvals struct {
	mu   sync.Mutex
	seen map[string]bool
}

func NewApprovals() *Approvals {
	return &Approvals{seen: map[string]bool{}}
}

func confirmDangerous(ctx context.Context, elicit *tool.Elicitation, appr *Approvals, args map[string]any, workdir string) error {
	command, _ := args["command"].(string)
	// The prompt (and the remembered approval key) must state where the
	// command runs — approving `git clean -fdx` for the workspace is not
	// approving it for an arbitrary other directory.
	if workdir != "" {
		command = command + "  [in " + workdir + "]"
	}
	return confirmIfDangerous(ctx, elicit, appr, command, ClassifyEffect(args) == tool.EffectDangerous)
}

func confirmIfDangerous(ctx context.Context, elicit *tool.Elicitation, appr *Approvals, text string, dangerous bool) error {
	if !dangerous || elicit == nil || elicit.Confirm == nil {
		return nil
	}

	// Exact-match key (modulo surrounding whitespace): normalizing inner
	// whitespace would conflate distinct quoted arguments.
	key := strings.TrimSpace(text)

	appr.mu.Lock()
	seen := appr.seen[key]
	appr.mu.Unlock()

	if seen {
		return nil
	}

	approved, err := elicit.Confirm(ctx, "❯ "+text)

	if err != nil {
		return fmt.Errorf("failed to get user approval: %w", err)
	}

	if !approved {
		return fmt.Errorf("command execution denied by user")
	}

	appr.mu.Lock()
	appr.seen[key] = true
	appr.mu.Unlock()

	return nil
}

// confirmSandboxEscalation asks the user whether a command that appears to
// have been blocked by the workspace sandbox should be retried without it.
// Unlike confirmIfDangerous there is no unconfirmed default: with no
// confirmation gate available, the sandbox boundary the user opted into stays
// in force and the original denial is returned as-is — never silently lifted.
func confirmSandboxEscalation(ctx context.Context, elicit *tool.Elicitation, appr *Approvals, command, workdir string) (bool, error) {
	if elicit == nil || elicit.Confirm == nil {
		return false, nil
	}

	text := command
	if workdir != "" {
		text = text + "  [in " + workdir + "]"
	}
	key := "sandbox-escalation:" + strings.TrimSpace(text)

	appr.mu.Lock()
	seen := appr.seen[key]
	appr.mu.Unlock()

	if seen {
		return true, nil
	}

	approved, err := elicit.Confirm(ctx, "❯ "+command+"\nBlocked by the workspace sandbox. Retry without it?")

	if err != nil {
		return false, fmt.Errorf("failed to get user approval: %w", err)
	}

	if !approved {
		return false, nil
	}

	appr.mu.Lock()
	appr.seen[key] = true
	appr.mu.Unlock()

	return true, nil
}
