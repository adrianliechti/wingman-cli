package copilot

import (
	"context"

	"github.com/adrianliechti/wingman-agent/pkg/external"
	"github.com/adrianliechti/wingman-agent/pkg/model"
)

type Options = external.Options

type CopilotConfig struct {
	BaseURL   string
	AuthToken string

	Model string

	MaxPromptTokens int
	MaxOutputTokens int
}

func NewConfig(ctx context.Context, options *Options) (*CopilotConfig, error) {
	options = external.WithDefaults(options)

	models, err := external.Models(ctx, options, &external.ModelOptions{
		Kind:   external.ModelDefault,
		Filter: external.IsOpenAI,
	})

	if err != nil {
		return nil, err
	}

	cfg := &CopilotConfig{
		BaseURL:   options.WingmanURL,
		AuthToken: options.WingmanToken,

		MaxPromptTokens: 400000,
		MaxOutputTokens: 128000,
	}

	if len(models) > 0 {
		cfg.Model = models[0]

		if m, ok := model.Find(cfg.Model); ok {
			cfg.MaxPromptTokens = m.InputTokens()
			cfg.MaxOutputTokens = m.OutputTokens()
		}
	}

	return cfg, nil
}
