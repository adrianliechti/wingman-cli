package codex

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

func Run(ctx context.Context, args []string, options *RunOptions) error {
	if options == nil {
		options = new(RunOptions)
	}

	if options.Path == "" {
		options.Path = "codex"
	}

	if options.Env == nil {
		options.Env = os.Environ()
	}

	cfg, err := NewConfig(ctx, options)

	if err != nil {
		return err
	}

	url := strings.TrimRight(cfg.BaseURL, "/") + "/v1"

	env := options.Env
	env = append(env, "WINGMAN_TOKEN="+cfg.AuthToken)

	arg := []string{
		// Model configuration
		"--config", "model=\"" + cfg.Model + "\"",
		"--config", "model_provider=\"wingman\"",
		"--config", "model_providers.wingman.name=\"Wingman\"",
		"--config", "model_providers.wingman.base_url=\"" + url + "\"",
		"--config", "model_providers.wingman.env_key=\"WINGMAN_TOKEN\"",
		"--config", "model_providers.wingman.requires_openai_auth=false",

		// Telemetry & data exfiltration prevention
		"--config", "feedback.enabled=false",
		"--config", "analytics.enabled=false",
		"--config", "history.persistence=\"none\"",

		// Disabled features (security & cost control)
		"--config", "web_search=\"disabled\"",
		"--config", "features.remote_models=false",
		"--config", "features.apps=false",

		// UI
		"--config", "tui.show_tooltips=false",
		"--config", "check_for_update_on_startup=false",
	}

	args = append(arg, args...)

	cmd := exec.Command(options.Path, args...)
	cmd.Env = env

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
