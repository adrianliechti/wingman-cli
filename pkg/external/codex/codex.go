package codex

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/external"
)

func BinPath() string {
	return external.LookupPath("codex", "codex")
}

func BuildArgs(cfg *CodexConfig) []string {
	url := strings.TrimRight(cfg.BaseURL, "/") + "/v1"

	return []string{
		"--config", "agents.enabled=false",
		"--config", "features.multi_agent_v2=false",

		"--config", "model=\"" + cfg.Model + "\"",
		"--config", "model_provider=\"wingman\"",
		"--config", "model_providers.wingman.name=\"Wingman\"",
		"--config", "model_providers.wingman.base_url=\"" + url + "\"",
		"--config", "model_providers.wingman.env_key=\"WINGMAN_TOKEN\"",
		"--config", "model_providers.wingman.wire_api=\"responses\"",
		"--config", "model_providers.wingman.requires_openai_auth=false",

		"--config", "feedback.enabled=false",
		"--config", "analytics.enabled=false",
		"--config", "history.persistence=\"none\"",
		"--config", "otel.exporter=\"none\"",
		"--config", "otel.trace_exporter=\"none\"",
		"--config", "otel.metrics_exporter=\"none\"",
		"--config", "otel.log_user_prompt=false",

		"--config", "web_search=\"disabled\"",
		"--config", "features.apps=false",
		"--config", "features.plugins=false",
		"--config", "features.fast_mode=false",

		"--config", "tui.show_tooltips=false",
		"--config", "check_for_update_on_startup=false",
	}
}

func BuildEnv(parent []string, cfg *CodexConfig) []string {
	if parent == nil {
		parent = os.Environ()
	}
	env := make([]string, 0, len(parent)+1)
	env = append(env, parent...)
	env = append(env, "WINGMAN_TOKEN="+cfg.AuthToken)
	return env
}

func Run(ctx context.Context, args []string, options *Options) error {
	if options == nil {
		options = new(Options)
	}

	if options.Path == "" {
		options.Path = BinPath()
	}

	if options.Env == nil {
		options.Env = os.Environ()
	}

	cfg, err := NewConfig(ctx, options)

	if err != nil {
		return err
	}

	args = append(BuildArgs(cfg), args...)

	cmd := exec.CommandContext(ctx, options.Path, args...)
	cmd.Env = BuildEnv(options.Env, cfg)

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
