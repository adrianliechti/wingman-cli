package external

import (
	"context"
	"os"
	"strings"

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

	return options
}

func AvailableModels(ctx context.Context, options *Options) (map[string]bool, error) {
	options = WithDefaults(options)

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

	candidates := defaultModels

	if modelOpts.Kind == ModelFast {
		candidates = fastModels
	}

	var out []string

	for _, id := range candidates {
		if !available[id] {
			continue
		}

		if modelOpts.Filter != nil && !modelOpts.Filter(id) {
			continue
		}

		out = append(out, id)
	}

	return out, nil
}

var (
	defaultModels = []string{
		"claude-sonnet-5",
		"claude-sonnet-4-6",
		"claude-sonnet-4-5",
		"claude-opus-5",
		"claude-opus-4-8",
		"claude-opus-4-7",
		"claude-opus-4-6",
		"claude-opus-4-5",
		"claude-fable-5",
		"claude-mythos-5",

		"gpt-5.6-sol",
		"gpt-5.6-terra",
		"gpt-5.5",
		"gpt-5.4",
		"gpt-5.3-codex",
		"gpt-5.2-codex",
		"gpt-5.1-codex-max",
		"gpt-5.1-codex",
		"gpt-5-codex",
		"gpt-5.2",
		"gpt-5.1",
		"gpt-5",

		"gemini-3.1-pro-preview",
		"gemini-3-pro-preview",
		"gemini-2.5-pro",
	}

	fastModels = []string{

		"claude-haiku-4-6",
		"claude-haiku-4-5",

		"gpt-5.6-luna",
		"gpt-5.3-codex-spark",
		"gpt-5.1-codex-mini",
		"gpt-5-mini",

		"gemini-3-flash-preview",
		"gemini-2.5-flash",
	}
)
