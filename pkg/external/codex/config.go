package codex

import (
	"context"

	"github.com/adrianliechti/wingman-agent/pkg/external"
	"github.com/adrianliechti/wingman-agent/pkg/model"
)

type Options = external.Options

type CodexConfig struct {
	BaseURL   string
	AuthToken string

	Models []string

	ModelCatalog string
}

func NewConfig(ctx context.Context, options *Options) (*CodexConfig, error) {
	options = external.WithDefaults(options)

	available, err := external.AvailableModels(ctx, options)

	if err != nil {
		return nil, err
	}

	cfg := &CodexConfig{
		BaseURL:   options.WingmanURL,
		AuthToken: options.WingmanToken,
	}
	cfg.Models = resolveModels(available)

	return cfg, nil
}

func resolveModels(available map[string]bool) []string {
	var models []string
	var fastModels []string

	for _, m := range model.Available(available) {
		if !external.IsOpenAI(m.ID) {
			continue
		}

		if m.Class == model.ClassSmall {
			fastModels = append(fastModels, m.ID)
			continue
		}

		models = append(models, m.ID)
	}

	return append(models, fastModels...)
}
