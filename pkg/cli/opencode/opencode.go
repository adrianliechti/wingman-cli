package opencode

import (
	_ "embed"
	"os"
	"os/exec"
	"strings"
)

var (
	//go:embed config.json
	config string
)

func Run(args []string) error {
	url := os.Getenv("WINGMAN_URL")

	if url == "" {
		url = "http://localhost:4242"
	}

	url = strings.TrimRight(url, "/") + "/v1"

	token := os.Getenv("WINGMAN_TOKEN")

	if token == "" {
		token = "-"
	}

	cfg := config
	cfg = strings.ReplaceAll(cfg, "WINGMAN_URL", url)
	cfg = strings.ReplaceAll(cfg, "WINGMAN_TOKEN", token)

	env := os.Environ()
	env = append(env, "OPENCODE_CONFIG_CONTENT="+cfg)

	cmd := exec.Command("opencode", args...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
