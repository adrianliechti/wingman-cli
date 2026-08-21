package shell

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"unicode"

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
	key := strings.Trim(text, " \t\n")

	appr.mu.Lock()
	seen := appr.seen[key]
	appr.mu.Unlock()

	if seen {
		return nil
	}

	approved, err := elicit.Confirm(ctx, "❯ "+approvalDisplayText(text))

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

// approvalDisplayText makes characters that can move, erase, or reorder a
// terminal prompt visible without changing ordinary command formatting.
func approvalDisplayText(text string) string {
	var out strings.Builder
	for _, r := range text {
		switch r {
		case '\n':
			out.WriteRune(r)
		case '\t':
			out.WriteString(`\t`)
		case '\r':
			out.WriteString(`\r`)
		default:
			if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || (unicode.IsSpace(r) && r != ' ') {
				fmt.Fprintf(&out, `\u{%04X}`, r)
				continue
			}
			out.WriteRune(r)
		}
	}
	return out.String()
}
