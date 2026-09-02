package truncation

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent/hook"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/text"
)

const MaxBytes = 100 * 1024

const grepMaxBytes = 20 * 1024

const shellMaxBytes = 48 * 1024

const headPreviewBytes = 4 * 1024

const tailPreviewBytes = 8 * 1024

func budgetFor(name string) int {
	switch name {
	case "grep":
		return grepMaxBytes
	case "shell", "exec_command", "exec_session":
		return shellMaxBytes
	default:
		return MaxBytes
	}
}

func New(scratchDir string) hook.PostToolUse {
	return func(_ context.Context, call tool.ToolCall, result string) (hook.Outcome, error) {
		if tool.IsImageResult(result) {
			return hook.Outcome{}, nil
		}
		budget := budgetFor(call.Name)
		if len(result) <= budget {
			return hook.Outcome{}, nil
		}
		path := writeScratch(scratchDir, call.Name, result)
		updated := formatPersisted(result, path)
		return hook.Outcome{UpdatedResult: &updated}, nil
	}
}

func writeScratch(scratchDir, toolName, content string) string {
	if scratchDir == "" {
		return ""
	}

	f, err := os.CreateTemp(scratchDir, "result-"+sanitizeName(toolName)+"-*.txt")
	if err != nil {
		return ""
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		os.Remove(f.Name())
		return ""
	}
	return f.Name()
}

func formatPersisted(result, scratchPath string) string {
	head := text.HeadBytes(result, headPreviewBytes)
	tail := text.TailBytes(result, tailPreviewBytes)

	var b strings.Builder
	b.WriteString("<persisted-output>\n")
	fmt.Fprintf(&b, "Output was %d bytes — too large for inline.", len(result))
	if scratchPath != "" {
		fmt.Fprintf(&b, " Full output saved to: %s", scratchPath)
	}
	fmt.Fprintf(&b, "\n\nPreview (first %d bytes):\n\n%s", len(head), head)
	fmt.Fprintf(&b, "\n\n[...]\n\nPreview (last %d bytes):\n\n%s", len(tail), tail)
	if scratchPath != "" {
		b.WriteString("\n\nUse `read` on the path above to retrieve specific ranges.")
	}
	b.WriteString("\n</persisted-output>")
	return b.String()
}

func sanitizeName(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "tool"
	}
	return string(out)
}
