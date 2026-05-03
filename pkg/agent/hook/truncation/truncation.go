package truncation

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/hook"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/text"
)

// DefaultMaxBytes is the wire-size cap each entry point applies to tool
// results. Outputs above this cap are middle-truncated (head + "…N chars
// truncated…" + tail) so both ends survive.
const DefaultMaxBytes = 16 * 1024

// New returns a PostToolUse hook that caps tool results at maxBytes via
// middle truncation. If scratchDir is non-empty, the full untruncated
// output is saved there and the model is told where to find it, so it can
// re-read specific ranges instead of re-running the tool.
func New(maxBytes int, scratchDir string) hook.PostToolUse {
	return func(ctx context.Context, call tool.ToolCall, result string) (string, error) {
		if len(result) <= maxBytes {
			return result, nil
		}

		totalBytes := len(result)
		truncated := text.TruncateMiddle(result, maxBytes)

		var notice string

		if scratchDir != "" {
			name := fmt.Sprintf("result-%d.txt", time.Now().UnixNano())
			path := filepath.Join(scratchDir, name)

			if err := os.WriteFile(path, []byte(result), 0644); err == nil {
				notice = fmt.Sprintf("[Output truncated: %d bytes — head and tail kept, middle elided. Full output saved to %s; use `read` on that path to retrieve a specific range.]\n\n", totalBytes, path)
			}
		}

		if notice == "" {
			notice = fmt.Sprintf("[Output truncated: %d bytes — head and tail kept, middle elided. Re-run with a narrower scope if you need the omitted section.]\n\n", totalBytes)
		}

		return notice + truncated, nil
	}
}
