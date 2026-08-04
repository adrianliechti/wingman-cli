package claude

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/adrianliechti/wingman-agent/pkg/external"
)

func BinPath() string {
	if path := external.LookupPath("claude", ""); path != "" {
		return path
	}

	if path, err := FindPath(); err == nil {
		return path
	}

	return "claude"
}

func BuildVars(cfg *ClaudeConfig) map[string]string {
	vars := map[string]string{

		"ANTHROPIC_BASE_URL":   cfg.BaseURL,
		"ANTHROPIC_API_KEY":    "",
		"ANTHROPIC_AUTH_TOKEN": cfg.AuthToken,

		"DISABLE_TELEMETRY":                        "1",
		"DISABLE_ERROR_REPORTING":                  "1",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
		"CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY":      "1",
		"CLAUDE_CODE_SUBPROCESS_ENV_SCRUB":         "1",
		"CLAUDE_CODE_ATTRIBUTION_HEADER":           "0",
		"CLAUDE_CODE_HIDE_CWD":                     "1",
		"CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST":     "1",

		"DISABLE_AUTOUPDATER":                "1",
		"DISABLE_FEEDBACK_COMMAND":           "1",
		"DISABLE_BUG_COMMAND":                "1",
		"DISABLE_INSTALLATION_CHECKS":        "1",
		"DISABLE_EXTRA_USAGE_COMMAND":        "1",
		"DISABLE_UPGRADE_COMMAND":            "1",
		"DISABLE_DOCTOR_COMMAND":             "1",
		"DISABLE_INSTALL_GITHUB_APP_COMMAND": "1",
		"DISABLE_LOGIN_COMMAND":              "1",
		"DISABLE_LOGOUT_COMMAND":             "1",

		"CLAUDE_CODE_DISABLE_FAST_MODE":          "1",
		"CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS": "1",

		"DISABLE_COST_WARNINGS": "1",

		"CLAUDE_CODE_DISABLE_ARTIFACT": "1",

		"IS_DEMO": "1",

		"CLAUDE_CODE_IDE_SKIP_AUTO_INSTALL": "1",

		"ENABLE_CLAUDEAI_MCP_SERVERS": "false",

		"CLAUDE_CODE_DISABLE_OFFICIAL_MARKETPLACE_AUTOINSTALL": "1",
	}

	if cfg.HaikuModel != "" {
		vars["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = cfg.HaikuModel
	}

	if cfg.SonnetModel != "" {
		vars["ANTHROPIC_DEFAULT_SONNET_MODEL"] = cfg.SonnetModel
	}

	if cfg.OpusModel != "" {
		vars["ANTHROPIC_DEFAULT_OPUS_MODEL"] = cfg.OpusModel
	}

	if cfg.FableModel != "" {
		vars["ANTHROPIC_DEFAULT_FABLE_MODEL"] = cfg.FableModel
	}

	if cfg.SonnetModel != "" {
		vars["CLAUDE_CODE_SUBAGENT_MODEL"] = cfg.SonnetModel
	}

	return vars
}

func BuildArgs() []string {
	return []string{"--settings", `{"disableRemoteControl":true}`}
}

func BuildEnv(parent []string, cfg *ClaudeConfig) []string {
	if parent == nil {
		parent = os.Environ()
	}
	env := make([]string, 0, len(parent)+len(BuildVars(cfg)))
	env = append(env, parent...)
	for k, v := range BuildVars(cfg) {
		env = append(env, k+"="+v)
	}
	return env
}

func FindPath() (string, error) {
	if path, err := exec.LookPath("claude"); err == nil {
		return path, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	name := "claude"
	if runtime.GOOS == "windows" {
		name = "claude.exe"
	}

	candidates := []string{
		filepath.Join(home, ".local", "bin", name),
		filepath.Join(home, ".claude", "local", name),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("claude is not installed or not on PATH")
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

	cmd := exec.CommandContext(ctx, options.Path, append(BuildArgs(), args...)...)
	cmd.Env = BuildEnv(options.Env, cfg)

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
