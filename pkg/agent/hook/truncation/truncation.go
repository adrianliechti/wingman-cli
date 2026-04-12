package truncation

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/hook"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

const DefaultMaxBytes = 50 * 1024 // 50KB

// New returns a PostToolUse hook that truncates results exceeding maxBytes.
// If scratchDir is non-empty, the full output is saved there.
func New(maxBytes int, scratchDir string) hook.PostToolUse {
	return func(ctx context.Context, call tool.ToolCall, result string) (string, error) {
		if len(result) <= maxBytes {
			return result, nil
		}

		totalBytes := len(result)
		truncated := result[totalBytes-maxBytes:]

		if idx := strings.Index(truncated, "\n"); idx >= 0 && idx < 512 {
			truncated = truncated[idx+1:]
		}

		shownBytes := len(truncated)

		var notice string

		if scratchDir != "" {
			name := fmt.Sprintf("result-%d.txt", time.Now().UnixNano())
			path := filepath.Join(scratchDir, name)

			if err := os.WriteFile(path, []byte(result), 0644); err == nil {
				notice = fmt.Sprintf("[Output truncated: showing last %d of %d bytes. Full output: %s]\n\n", shownBytes, totalBytes, path)
			}
		}

		if notice == "" {
			notice = fmt.Sprintf("[Output truncated: showing last %d of %d bytes]\n\n", shownBytes, totalBytes)
		}

		return notice + truncated, nil
	}
}
