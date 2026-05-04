package claude

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
)

func Run(ctx context.Context, args []string, options *Options) error {
	if options == nil {
		options = new(Options)
	}

	if options.Path == "" {
		path, err := findPath()
		if err != nil {
			return err
		}

		options.Path = path
	}

	if options.Env == nil {
		options.Env = os.Environ()
	}

	cfg, err := NewConfig(ctx, options)

	if err != nil {
		return err
	}

	vars := map[string]string{
		// Auth & API routing
		"ANTHROPIC_BASE_URL":   cfg.BaseURL,
		"ANTHROPIC_API_KEY":    "",
		"ANTHROPIC_AUTH_TOKEN": cfg.AuthToken,

		// Model configuration
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":      cfg.HaikuModel,
		"ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME": "Wingman Haiku",

		"ANTHROPIC_DEFAULT_SONNET_MODEL":      cfg.SonnetModel,
		"ANTHROPIC_DEFAULT_SONNET_MODEL_NAME": "Wingman Sonnet",

		"ANTHROPIC_DEFAULT_OPUS_MODEL":      cfg.OpusModel,
		"ANTHROPIC_DEFAULT_OPUS_MODEL_NAME": "Wingman Opus",

		// Telemetry & data exfiltration prevention
		"DISABLE_TELEMETRY":                        "1",
		"DISABLE_ERROR_REPORTING":                  "1",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
		"CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY":      "1",
		"CLAUDE_CODE_SUBPROCESS_ENV_SCRUB":         "1",
		"CLAUDE_CODE_MCP_ALLOWLIST_ENV":            "1",
		"CLAUDE_CODE_SKIP_PROMPT_HISTORY":          "1",
		"CLAUDE_CODE_ATTRIBUTION_HEADER":           "0",
		"CLAUDE_CODE_HIDE_CWD":                     "1",
		"CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST":     "1",

		// Disabled commands (not applicable in managed environment)
		"DISABLE_AUTOUPDATER":                "1",
		"DISABLE_FEEDBACK_COMMAND":           "1",
		"DISABLE_INSTALLATION_CHECKS":        "1",
		"DISABLE_EXTRA_USAGE_COMMAND":        "1",
		"DISABLE_UPGRADE_COMMAND":            "1",
		"DISABLE_DOCTOR_COMMAND":             "1",
		"DISABLE_INSTALL_GITHUB_APP_COMMAND": "1",
		"DISABLE_LOGIN_COMMAND":              "1",
		"DISABLE_LOGOUT_COMMAND":             "1",

		// Disabled features (security & cost control)
		"CLAUDE_CODE_DISABLE_FAST_MODE":             "1",
		"CLAUDE_CODE_DISABLE_BACKGROUND_TASKS":      "1",
		"CLAUDE_CODE_DISABLE_CRON":                  "1",
		"CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS":    "1",
		"CLAUDE_CODE_DISABLE_1M_CONTEXT":            "1",
		"CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK": "1",
		"CLAUDE_CODE_DISABLE_LEGACY_MODEL_REMAP":    "1",

		// UI & integration lockdown
		"CLAUDE_CODE_HIDE_ACCOUNT_INFO":     "1",
		"CLAUDE_CODE_IDE_SKIP_AUTO_INSTALL": "1",

		"ENABLE_CLAUDEAI_MCP_SERVERS": "false",

		"CLAUDE_CODE_DISABLE_OFFICIAL_MARKETPLACE_AUTOINSTALL": "1",
	}

	if cfg.SonnetModel != "" {
		vars["CLAUDE_CODE_SUBAGENT_MODEL"] = cfg.SonnetModel
	}

	if cfg.ContextWindow > 0 {
		vars["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] = strconv.Itoa(cfg.ContextWindow)
	}

	env := options.Env

	for k, v := range vars {
		env = append(env, k+"="+v)
	}

	cmd := exec.Command(options.Path, args...)
	cmd.Env = env

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

func findPath() (string, error) {
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

	path := filepath.Join(home, ".claude", "local", name)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("claude is not installed or not on PATH")
	}

	return path, nil
}
