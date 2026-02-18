package claude

import (
	"context"
	"os"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/adrianliechti/wingman-cli/pkg/cli"
)

type ClaudeConfig struct {
	BaseURL   string
	AuthToken string

	OpusModel   string
	HaikuModel  string
	SonnetModel string
}

func NewConfig(ctx context.Context, options *cli.RunOptions) (*ClaudeConfig, error) {
	if options == nil {
		options = new(cli.RunOptions)
	}

	if options.WingmanURL == "" {
		val := os.Getenv("WINGMAN_URL")

		if val == "" {
			val = "http://localhost:4242"
		}

		options.WingmanURL = val
	}

	if options.WingmanToken == "" {
		val := os.Getenv("WINGMAN_TOKEN")

		if val == "" {
			val = "-"
		}

		options.WingmanToken = val
	}

	client := openai.NewClient(
		option.WithBaseURL(strings.TrimRight(options.WingmanURL, "/")+"/v1"),
		option.WithAPIKey(options.WingmanToken),
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
		BaseURL:   options.WingmanURL,
		AuthToken: options.WingmanToken,
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
