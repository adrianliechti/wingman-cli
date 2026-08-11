package external

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/model"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

type ModelKind int

const (
	ModelDefault ModelKind = iota
	ModelFast
)

type ModelFilter func(id string) bool

func IsAnthropic(id string) bool { return strings.HasPrefix(id, "claude-") }

func IsOpenAI(id string) bool { return strings.HasPrefix(id, "gpt-") }

func IsGoogle(id string) bool { return strings.HasPrefix(id, "gemini-") }

type ModelOptions struct {
	Kind   ModelKind
	Filter ModelFilter
}

func WithDefaults(options *Options) *Options {
	if options == nil {
		options = new(Options)
	}

	if options.WingmanURL == "" {
		options.WingmanURL = os.Getenv("WINGMAN_URL")
	}

	if options.WingmanToken == "" {
		val := os.Getenv("WINGMAN_TOKEN")

		if val == "" {
			val = "-"
		}

		options.WingmanToken = val
	}

	return options
}

func AvailableModels(ctx context.Context, options *Options) (map[string]bool, error) {
	options = WithDefaults(options)
	if strings.TrimSpace(options.WingmanURL) == "" {
		return nil, fmt.Errorf("WINGMAN_URL is required")
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

	return available, nil
}

func Models(ctx context.Context, options *Options, modelOpts *ModelOptions) ([]string, error) {
	available, err := AvailableModels(ctx, options)

	if err != nil {
		return nil, err
	}

	if modelOpts == nil {
		modelOpts = new(ModelOptions)
	}

	var out []string

	for _, m := range model.Available(available) {
		switch modelOpts.Kind {
		case ModelFast:
			if m.Class != model.ClassSmall {
				continue
			}

		default:
			if m.Class == model.ClassSmall {
				continue
			}
		}

		if modelOpts.Filter != nil && !modelOpts.Filter(m.ID) {
			continue
		}

		out = append(out, m.ID)
	}

	return out, nil
}
