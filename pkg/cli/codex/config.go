package codex

import (
	"context"
	"os"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/adrianliechti/wingman-cli/pkg/cli"
)

type CodexConfig struct {
	BaseURL   string
	AuthToken string

	Model string
}

func NewConfig(ctx context.Context, options *cli.RunOptions) (*CodexConfig, error) {
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

	cfg := &CodexConfig{
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

	cfg.Model = pick(
		// Codex models
		"gpt-5.3-codex",
		"gpt-5.2-codex",
		"gpt-5.1-codex-max",
		"gpt-5.1-codex",
		"gpt-5-codex",

		// Codex Mini models
		"gpt-5.3-codex-spark",
		"gpt-5.1-codex-mini",

		// ChatGPT models
		"gpt-5.2",
		"gpt-5.1",
		"gpt-5",

		// ChatGPT Mini models
		"gpt-5-mini",
	)

	return cfg, nil
}
