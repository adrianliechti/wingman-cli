package gemini

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
)

func Run(ctx context.Context, args []string, options *Options) error {
	if options == nil {
		options = new(Options)
	}

	if options.Path == "" {
		options.Path = "gemini"
	}

	if options.Env == nil {
		options.Env = os.Environ()
	}

	cfg, err := NewConfig(ctx, options)

	if err != nil {
		return err
	}

	// Create managed system settings file
	dir, err := os.MkdirTemp("", "gemini-config-*")

	if err != nil {
		return err
	}

	defer os.RemoveAll(dir)

	settings := map[string]any{
		// Telemetry & data exfiltration prevention
		"telemetry": map[string]any{
			"enabled":    false,
			"logPrompts": false,
		},
		"privacy": map[string]any{
			"usageStatisticsEnabled": false,
		},

		// Updates & lifecycle
		"general": map[string]any{
			"enableAutoUpdate":             false,
			"enableAutoUpdateNotification": false,
		},
		"sessionRetention": map[string]any{
			"maxAge": "7d",
		},

		// Disabled features
		"experimental": map[string]any{
			"extensionRegistry": false,
		},
		"admin": map[string]any{
			"extensions": map[string]any{
				"enabled": false,
			},
		},
	}

	data, err := json.Marshal(settings)

	if err != nil {
		return err
	}

	settingsFile := filepath.Join(dir, "settings.json")

	if err := os.WriteFile(settingsFile, data, 0644); err != nil {
		return err
	}

	vars := map[string]string{
		// Auth & API routing
		"GOOGLE_GEMINI_BASE_URL":        cfg.BaseURL,
		"GEMINI_DEFAULT_AUTH_TYPE":      "gemini-api-key",
		"GEMINI_API_KEY":                cfg.AuthToken,
		"GEMINI_API_KEY_AUTH_MECHANISM": "bearer",

		// Model configuration
		"GEMINI_MODEL": cfg.Model,

		// Managed system settings
		"GEMINI_CLI_SYSTEM_SETTINGS_PATH": settingsFile,
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
