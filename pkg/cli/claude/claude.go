package claude

import (
	"os"
	"os/exec"
)

func Run(args []string) {
	env := os.Environ()

	if val := os.Getenv("WINGMAN_URL"); val != "" {
		env = append(env, "ANTHROPIC_BASE_URL="+val)
	}

	if val := os.Getenv("WINGMAN_TOKEN"); val != "" {
		env = append(env, "ANTHROPIC_API_KEY=")
		env = append(env, "ANTHROPIC_AUTH_TOKEN="+val)
	}

	env = append(env, "DISABLE_TELEMETRY=1")
	env = append(env, "DISABLE_ERROR_REPORTING=1")
	env = append(env, "DISABLE_INSTALLATION_CHECKS=1")
	env = append(env, "DISABLE_NON_ESSENTIAL_MODEL_CALLS=1")

	env = append(env, "ANTHROPIC_DEFAULT_OPUS_MODEL=claude-opus-4-5")
	env = append(env, "ANTHROPIC_DEFAULT_HAIKU_MODEL=claude-haiku-4-5")
	env = append(env, "ANTHROPIC_DEFAULT_SONNET_MODEL=claude-sonnet-4-5")

	cmd := exec.Command("claude", args...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.Run()
}
