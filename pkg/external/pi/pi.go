package pi

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/external"
	"github.com/adrianliechti/wingman-agent/pkg/layout"
)

const providerName = "wingman"

func BinPath() string {
	return external.LookupPath("pi", "pi")
}

// ConfigDir returns a stable, wingman-owned pi config directory (PI_CODING_AGENT_DIR).
// Unlike the launcher's ephemeral temp dir, this persists so pi sessions written
// under <dir>/sessions survive across runs and can be listed/loaded.
func ConfigDir() (string, error) {
	dir, err := layout.WingmanPath("pi")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func SessionsDir(configDir string) string {
	return filepath.Join(configDir, "sessions")
}

// NativeSessionsDir returns the session directory used by an ordinary pi
// installation, respecting PI_CODING_AGENT_DIR when it is configured.
func NativeSessionsDir() string {
	dir := os.Getenv("PI_CODING_AGENT_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".pi", "agent")
	}
	return SessionsDir(dir)
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

	dir, err := os.MkdirTemp("", "pi-config-*")

	if err != nil {
		return err
	}

	defer os.RemoveAll(dir)

	if err := WriteModels(dir, cfg); err != nil {
		return err
	}

	args = append(BuildArgs(cfg), args...)

	cmd := exec.CommandContext(ctx, options.Path, args...)
	cmd.Env = BuildEnv(options.Env, dir)

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

func BuildEnv(parent []string, dir string) []string {
	if parent == nil {
		parent = os.Environ()
	}

	vars := map[string]string{
		"PI_CODING_AGENT_DIR": dir,

		"PI_OFFLINE":            "1",
		"PI_SKIP_VERSION_CHECK": "1",
	}

	env := make([]string, 0, len(parent)+len(vars))
	env = append(env, parent...)

	for k, v := range vars {
		env = append(env, k+"="+v)
	}

	return env
}

func BuildArgs(cfg *PiConfig) []string {
	args := []string{"--provider", providerName}

	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}

	return args
}

func WriteModels(dir string, cfg *PiConfig) error {
	type model struct {
		ID string `json:"id"`
	}

	type provider struct {
		BaseURL string  `json:"baseUrl"`
		API     string  `json:"api"`
		APIKey  string  `json:"apiKey"`
		Models  []model `json:"models"`
	}

	models := make([]model, 0, len(cfg.Models))

	for _, id := range cfg.Models {
		models = append(models, model{ID: id})
	}

	doc := map[string]any{
		"providers": map[string]any{
			providerName: provider{
				BaseURL: strings.TrimRight(cfg.BaseURL, "/") + "/v1",
				API:     "openai-completions",
				APIKey:  cfg.AuthToken,
				Models:  models,
			},
		},
	}

	data, err := json.MarshalIndent(doc, "", "  ")

	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(dir, "models.json"), data, 0600)
}
