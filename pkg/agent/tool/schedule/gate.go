package schedule

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/shell"
)

const (
	gateTimeout   = 2 * time.Minute
	gateMaxOutput = 16 * 1024
)

// RunGate executes a task's pre-check script. It reports whether the agent
// should be woken; on script failure it fails open so the agent can fix it.
func RunGate(ctx context.Context, dir, script string) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, gateTimeout)
	defer cancel()

	out, err := shell.Command(ctx, script, dir).CombinedOutput()

	output := strings.TrimSpace(string(out))
	if len(output) > gateMaxOutput {
		output = output[:gateMaxOutput] + "\n[output truncated]"
	}

	if err != nil {
		return true, fmt.Sprintf("pre-check script failed (%v); fix the script or remove it from the task.\n%s", err, output)
	}

	if wake, ok := parseGateOutput(output); ok {
		return wake, output
	}

	lines := strings.Split(output, "\n")
	if wake, ok := parseGateOutput(lines[len(lines)-1]); ok {
		return wake, output
	}

	return true, output
}

func parseGateOutput(s string) (bool, bool) {
	var result struct {
		Wake *bool `json:"wake"`
	}

	if err := json.Unmarshal([]byte(strings.TrimSpace(s)), &result); err != nil || result.Wake == nil {
		return false, false
	}

	return *result.Wake, true
}
