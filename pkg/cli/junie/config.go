package junie

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/adrianliechti/wingman-agent/pkg/cli"
)

type RunOptions = cli.RunOptions

func NewConfig(ctx context.Context, options *RunOptions) (string, error) {
	if options == nil {
		options = new(RunOptions)
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
		return "", err
	}

	pick := func(candidates ...string) string {
		for _, id := range candidates {
			if available[id] {
				return id
			}
		}
		return ""
	}

	mainModel := pick(
		// Claude models
		"claude-opus-4-6",
		"claude-opus-4-5",
		"claude-sonnet-4-6",
		"claude-sonnet-4-5",

		// OpenAI models
		"gpt-5.4",
		"gpt-5.3-codex",
		"gpt-5.2-codex",
		"gpt-5.1-codex",
		"gpt-5.2",
		"gpt-5.1",
		"gpt-5",

		// Gemini models
		"gemini-3.1-pro-preview",
		"gemini-3-pro-preview",
		"gemini-2.5-pro",
	)

	smallModel := pick(
		// Claude Haiku
		"claude-haiku-4-6",
		"claude-haiku-4-5",

		// OpenAI Mini
		"gpt-5.3-codex-spark",
		"gpt-5.1-codex-mini",
		"gpt-5-mini",

		// Gemini Flash
		"gemini-3-flash-preview",
		"gemini-2.5-flash",
	)

	if mainModel == "" {
		mainModel = smallModel
	}

	if smallModel == "" {
		smallModel = mainModel
	}

	url := strings.TrimRight(options.WingmanURL, "/") + "/v1"

	cfg := map[string]any{
		"baseUrl": url,
		"apiType": "OpenAIResponses",

		"id": mainModel,

		"extraHeaders": map[string]any{
			"Authorization": "Bearer " + options.WingmanToken,
		},

		"fasterModel": map[string]any{
			"id": smallModel,
		},
	}

	data, _ := json.MarshalIndent(cfg, "", "  ")

	return string(data), nil
}
