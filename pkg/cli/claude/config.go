package claude

import (
	"context"
	"os"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

type ClaudeConfig struct {
	BaseURL   string
	AuthToken string

	OpusModel   string
	HaikuModel  string
	SonnetModel string
}

func NewConfig(ctx context.Context) (*ClaudeConfig, error) {
	baseURL := os.Getenv("WINGMAN_URL")
	authToken := os.Getenv("WINGMAN_TOKEN")

	if baseURL == "" {
		baseURL = "http://localhost:4242"
	}

	if authToken == "" {
		authToken = "-"
	}

	client := openai.NewClient(
		option.WithBaseURL(strings.TrimRight(baseURL, "/")+"/v1"),
		option.WithAPIKey(authToken),
	)

	iter := client.Models.ListAutoPaging(ctx)

	available := make(map[string]bool)

	for iter.Next() {
		available[iter.Current().ID] = true
	}

	if err := iter.Err(); err != nil {
		return nil, err
	}

	cfg := &ClaudeConfig{
		BaseURL:   baseURL,
		AuthToken: authToken,
	}

	pick := func(candidates ...string) string {
		for _, id := range candidates {
			if available[id] {
				return id
			}
		}
		return ""
	}

	cfg.OpusModel = pick("claude-opus-4-6", "claude-opus-4-5")
	cfg.HaikuModel = pick("claude-haiku-4-6", "claude-haiku-4-5")
	cfg.SonnetModel = pick("claude-sonnet-4-6", "claude-sonnet-4-5")

	return cfg, nil
}
