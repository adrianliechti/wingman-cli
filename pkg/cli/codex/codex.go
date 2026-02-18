package codex

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

func Run(ctx context.Context, args []string) error {
	cfg, err := NewConfig(ctx)

	if err != nil {
		return err
	}

	url := strings.TrimRight(cfg.BaseURL, "/") + "/v1"

	arg := []string{
		"--config", "model=\"" + cfg.Model + "\"",

		"--config", "model_provider=\"wingman\"",

		"--config", "model_providers.wingman.name=\"Wingman\"",
		"--config", "model_providers.wingman.base_url=\"" + url + "\"",
		"--config", "model_providers.wingman.env_key=\"WINGMAN_TOKEN\"",
		"--config", "model_providers.wingman.requires_openai_auth=false",

		"--config", "tui.show_tooltips=false",

		"--config", "web_search=\"disabled\"",
		"--config", "features.remote_models=false",

		"--config", "feedback.enabled=false",
		"--config", "analytics.enabled=false",

		"--config", "check_for_update_on_startup=false",
	}

	args = append(arg, args...)

	cmd := exec.Command("codex", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
