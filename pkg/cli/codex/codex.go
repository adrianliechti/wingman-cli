package codex

import (
	"os"
	"os/exec"
	"strings"
)

func Run(args []string) {
	model := os.Getenv("WINGMAN_MODEL")

	if model != "" {
		model = "gpt-5.2-codex"
	}

	url := os.Getenv("WINGMAN_URL")

	if url == "" {
		url = "http://localhost:4242"
	}

	url = strings.TrimRight(url, "/") + "/v1"

	arg := []string{
		"--config", "model=\"" + model + "\"",

		"--config", "model_provider=\"wingman\"",

		"--config", "model_providers.wingman.name=\"Wingman\"",
		"--config", "model_providers.wingman.base_url=\"" + url + "\"",
		"--config", "model_providers.wingman.env_key=\"WINGMAN_TOKEN\"",
		"--config", "model_providers.wingman.requires_openai_auth=false",

		"--config", "feedback.enabled=false",
		"--config", "analytics.enabled=false",
	}

	args = append(arg, args...)

	cmd := exec.Command("codex", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.Run()
}
