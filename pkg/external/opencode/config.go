package opencode

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/external"
	"github.com/adrianliechti/wingman-agent/pkg/model"
)

type Options = external.Options

func NewConfig(ctx context.Context, options *Options) (string, error) {
	options = external.WithDefaults(options)

	available, err := external.AvailableModels(ctx, options)

	if err != nil {
		return "", err
	}

	var mainModel string
	var smallModel string

	models := make(map[string]any)

	for _, m := range model.Available(available) {
		models[m.ID] = map[string]any{
			"name": m.Name,

			"limit": map[string]any{
				"context": m.ContextTokens(),
				"output":  m.OutputTokens(),
			},
		}

		if m.Class == model.ClassSmall {
			if smallModel == "" {
				smallModel = m.ID
			}
		} else {
			if mainModel == "" {
				mainModel = m.ID
			}
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

		"autoupdate": false,
		"share":      "disabled",
		"snapshot":   false,

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
