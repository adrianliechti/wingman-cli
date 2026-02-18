package opencode

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/adrianliechti/wingman-cli/pkg/cli"
)

func NewConfig(ctx context.Context, options *cli.RunOptions) (string, error) {
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
		return "", err
	}

	var mainModel string
	var smallModel string

	models := make(map[string]any)

	isSmall := func(name string) bool {
		lower := strings.ToLower(name)

		for _, kw := range []string{"mini", "flash", "small", "haiku", "spark"} {
			if strings.Contains(lower, kw) {
				return true
			}
		}

		return false
	}

	for _, g := range candidates {
		for _, m := range g.models {
			if !available[m.id] {
				continue
			}

			models[m.id] = map[string]any{
				"name": g.name,

				"limit": map[string]any{
					"context": m.inputTokens,
					"output":  m.outputTokens,
				},
			}

			if isSmall(g.name) {
				if smallModel == "" {
					smallModel = m.id
				}
			} else {
				if mainModel == "" {
					mainModel = m.id
				}
			}

			break
		}
	}

	if mainModel == "" {
		mainModel = smallModel
	}

	if smallModel == "" {
		smallModel = mainModel
	}

	url := strings.TrimRight(options.WingmanURL, "/") + "/v1"

	cfg := map[string]any{
		"$schema": "https://opencode.ai/config.json",

		"model":       "wingman/" + mainModel,
		"small_model": "wingman/" + smallModel,

		"enabled_providers": []string{"wingman"},

		"provider": map[string]any{
			"wingman": map[string]any{
				"npm": "@ai-sdk/openai-compatible",

				"name": "Wingman",

				"options": map[string]any{
					"baseURL": url,
					"apiKey":  options.WingmanToken,
				},

				"models": models,
			},
		},
	}

	data, _ := json.Marshal(cfg)

	return string(data), nil
}
