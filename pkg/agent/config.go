package agent

import (
	"os"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/adrianliechti/wingman-agent/pkg/agent/hook"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

var AvailableModels = []string{
	"claude-opus-4-6",
	"claude-sonnet-4-6",

	"gpt-5.4",
	"gpt-5.3-codex",
}

type Config struct {
	client *openai.Client

	Model        func() string
	Tools        func() []tool.Tool
	Instructions func() string

	Hooks hook.Hooks
}

// Derive creates a new Config sharing the same client and model.
func (c *Config) Derive() *Config {
	return &Config{
		client: c.client,
		Model:  c.Model,
	}
}

func DefaultConfig() (*Config, error) {
	client := createClient()

	return &Config{
		client: &client,
	}, nil
}

func createClient() openai.Client {
	if url, ok := os.LookupEnv("WINGMAN_URL"); ok {
		baseURL := strings.TrimRight(url, "/") + "/v1"

		token, _ := os.LookupEnv("WINGMAN_TOKEN")

		if token == "" {
			token = "-"
		}

		return openai.NewClient(
			option.WithBaseURL(baseURL),
			option.WithAPIKey(token),
		)
	}

	if token, ok := os.LookupEnv("OPENAI_API_KEY"); ok {
		baseURL := "https://api.openai.com/v1"

		if url, ok := os.LookupEnv("OPENAI_BASE_URL"); ok {
			baseURL = url
		}

		return openai.NewClient(
			option.WithBaseURL(baseURL),
			option.WithAPIKey(token),
		)
	}

	return openai.NewClient(
		option.WithBaseURL("http://localhost:8080/v1"),
		option.WithAPIKey("-"),
	)
}
