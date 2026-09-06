package tooling

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/internal/process"
)

// ManagedTools resolves commands from Wingman's managed installations.
// Missing tools return an empty path and never fall back to another copy.
type ManagedTools interface {
	Resolve(string) string
}

// ProbeExecutes requests only a successful --version run instead of a version
// floor. It filters launchers that exist but cannot run, such as a rustup
// proxy whose component is not installed.
const ProbeExecutes = -1

// MajorVersionAtLeast probes a command's conventional --version output.
// Callers cache the surrounding detection result when appropriate. The probe
// itself deliberately stays live: shims and launchers can start or stop working
// when their external runtime changes without the executable being modified.
func MajorVersionAtLeast(ctx context.Context, command string, minimum int, workingDir string) bool {
	if minimum == 0 {
		return true
	}
	cmd := exec.CommandContext(ctx, command, "--version")
	process.Hide(cmd)
	if workingDir != "" {
		cmd.Dir = workingDir
	}
	cmd.Env = Environment(command, os.Environ())
	cmd.WaitDelay = 100 * time.Millisecond
	output, err := cmd.CombinedOutput()
	result := false
	if err == nil && minimum < 0 {
		result = true
	} else if err == nil {
		for _, field := range strings.Fields(string(output)) {
			majorText := strings.SplitN(strings.TrimPrefix(field, "v"), ".", 2)[0]
			major, atoiErr := strconv.Atoi(majorText)
			if atoiErr == nil {
				result = major >= minimum
				break
			}
		}
	}
	return result
}
