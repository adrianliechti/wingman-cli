package claude

import (
	"context"
	"os"
	"os/exec"
)

func Run(ctx context.Context, args []string) error {
	cfg, err := NewConfig(ctx)

	if err != nil {
		return err
	}

	vars := map[string]string{
		"ANTHROPIC_BASE_URL":   cfg.BaseURL,
		"ANTHROPIC_API_KEY":    "",
		"ANTHROPIC_AUTH_TOKEN": cfg.AuthToken,

		"DISABLE_PROMPT_CACHING":      "1",
		"DISABLE_INSTALLATION_CHECKS": "1",

		"CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS":   "1",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
		"CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY":      "1",

		"ANTHROPIC_DEFAULT_OPUS_MODEL":   cfg.OpusModel,
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":  cfg.HaikuModel,
		"ANTHROPIC_DEFAULT_SONNET_MODEL": cfg.SonnetModel,
	}

	env := os.Environ()

	for k, v := range vars {
		env = append(env, k+"="+v)
	}

	cmd := exec.Command("claude", args...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
