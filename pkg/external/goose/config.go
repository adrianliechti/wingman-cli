package goose

import (
	"context"

	"github.com/adrianliechti/wingman-agent/pkg/external"
	"github.com/adrianliechti/wingman-agent/pkg/model"
)

type Options = external.Options

type GooseConfig struct {
	BaseURL   string
	AuthToken string

	Model     string
	FastModel string

	ContextLimit int
}

func NewConfig(ctx context.Context, options *Options) (*GooseConfig, error) {
	options = external.WithDefaults(options)

	defaultModels, err := external.Models(ctx, options, &external.ModelOptions{
		Kind: external.ModelDefault,
	})

	if err != nil {
		return nil, err
	}

	fastModels, err := external.Models(ctx, options, &external.ModelOptions{
		Kind: external.ModelFast,
	})

	if err != nil {
		return nil, err
	}

	cfg := &GooseConfig{
		BaseURL:   options.WingmanURL,
		AuthToken: options.WingmanToken,

		ContextLimit: 200000,
	}

	if len(defaultModels) > 0 {
		cfg.Model = defaultModels[0]

		if m, ok := model.Find(cfg.Model); ok {
			cfg.ContextLimit = m.ContextTokens()
		}
	}

	if len(fastModels) > 0 {
		cfg.FastModel = fastModels[0]
	}

	return cfg, nil
}
