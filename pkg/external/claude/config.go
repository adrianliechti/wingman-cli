package claude

import (
	"context"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/external"
)

type Options = external.Options

type ClaudeConfig struct {
	BaseURL   string
	AuthToken string

	OpusModel   string
	HaikuModel  string
	SonnetModel string
	FableModel  string
}

func NewConfig(ctx context.Context, options *Options) (*ClaudeConfig, error) {
	options = external.WithDefaults(options)

	defaults, err := external.Models(ctx, options, &external.ModelOptions{
		Kind:   external.ModelDefault,
		Filter: external.IsAnthropic,
	})

	if err != nil {
		return nil, err
	}

	fast, err := external.Models(ctx, options, &external.ModelOptions{
		Kind:   external.ModelFast,
		Filter: external.IsAnthropic,
	})

	if err != nil {
		return nil, err
	}

	first := func(ids []string, name string) string {
		for _, id := range ids {
			if strings.Contains(id, name) {
				return id
			}
		}

		return ""
	}

	return &ClaudeConfig{
		BaseURL:   options.WingmanURL,
		AuthToken: options.WingmanToken,

		HaikuModel:  first(fast, "haiku"),
		SonnetModel: first(defaults, "sonnet"),
		OpusModel:   first(defaults, "opus"),
		FableModel:  first(defaults, "fable"),
	}, nil
}
