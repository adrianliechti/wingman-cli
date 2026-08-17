package agent

import (
	"context"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"

	"github.com/adrianliechti/wingman-agent/pkg/agent/hook"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/model"
)

const (
	DefaultMaxTurns         = 400
	DefaultMaxParallelTools = 8
	DefaultToolTimeout      = 10 * time.Minute

	DefaultContextWindow = 400_000

	DefaultReserveTokens = 32_000
)

func ContextWindowFor(id string, largeContext bool) int {
	if window := model.ProfileFor(id).ContextWindow(largeContext); window > 0 {
		return window
	}
	if candidate, ok := model.Find(id); ok {
		if window := candidate.ContextTokens(); window > 0 {
			return window
		}
	}

	return DefaultContextWindow
}

// ModelOption is a resolved model for a per-subagent override. Efforts lists
// the supported reasoning efforts in ascending order; empty means unrestricted.
type ModelOption struct {
	ID      string
	Efforts []string
}

type Config struct {
	client *openai.Client

	Model        func() string
	Effort       func() string
	Tools        func() []tool.Tool
	Instructions func() string

	// UtilityModel, when set and non-empty, handles internal utility calls
	// (compaction summaries, recaps) instead of the main model.
	UtilityModel func() string

	// SubagentModel resolves a model role for per-subagent overrides: "plan"
	// and "utility" name the session's role models, "" the currently
	// inherited model (consulted for effort clamping). ok=false or an empty
	// ID keep the inherited model. Nil disables overrides and clamping.
	SubagentModel func(role string) (ModelOption, bool)

	// CacheKey routes provider-side prompt caching; keep it stable per
	// conversation (e.g. the session ID) to maximize prefix-cache hits.
	CacheKey string

	Hooks hook.Hooks

	// MaxTurns caps successful model invocations in one Send run. Stream
	// retries and tool calls do not consume turns. Zero uses the default;
	// negative disables the safety bound.
	MaxTurns int

	// MaxParallelTools bounds concurrently executing read-only tool calls.
	// Zero uses the default; negative allows the whole emitted batch.
	MaxParallelTools int

	// ToolTimeout is a hard ceiling on every tool call. When zero, tools may
	// extend the default via tool.Tool.Timeout; negative disables deadlines.
	ToolTimeout time.Duration

	ContextWindow int

	// LargeContext compacts against the model's full hardware window instead
	// of stopping at the provider's long-context price threshold (e.g. 2x
	// input pricing on GPT-5.4/5.5 beyond 272k input tokens).
	LargeContext bool

	ReserveTokens int
}

func (c *Config) Derive() *Config {
	return &Config{
		client:        c.client,
		Model:         c.Model,
		Effort:        c.Effort,
		Tools:         c.Tools,
		Instructions:  c.Instructions,
		UtilityModel:  c.UtilityModel,
		SubagentModel: c.SubagentModel,

		CacheKey: c.CacheKey,

		// User-prompt and main-session lifecycle hooks stay with the top-level
		// session. Tool, permission, compaction, and subagent lifecycle hooks
		// also run inside delegated agents, matching Codex hook scoping.
		Hooks: hook.Hooks{
			PreToolUse:        slices.Clone(c.Hooks.PreToolUse),
			PermissionRequest: slices.Clone(c.Hooks.PermissionRequest),
			PostToolUse:       slices.Clone(c.Hooks.PostToolUse),
			SubagentStart:     slices.Clone(c.Hooks.SubagentStart),
			SubagentStop:      slices.Clone(c.Hooks.SubagentStop),
			PreCompact:        slices.Clone(c.Hooks.PreCompact),
			PostCompact:       slices.Clone(c.Hooks.PostCompact),
			Stop:              slices.Clone(c.Hooks.Stop),
		},

		MaxTurns:         c.MaxTurns,
		MaxParallelTools: c.MaxParallelTools,
		ToolTimeout:      c.ToolTimeout,

		ContextWindow: c.ContextWindow,
		LargeContext:  c.LargeContext,
		ReserveTokens: c.ReserveTokens,
	}
}

func (c *Config) utilityModelName() string {
	if c.UtilityModel != nil {
		if model := c.UtilityModel(); model != "" {
			return model
		}
	}
	if c.Model != nil {
		return c.Model()
	}
	return ""
}

// Utility runs a one-shot completion on the utility model (falling back to the
// main model) and credits its token usage to the session through the context's
// usage sink. It backs internal helpers such as fetch page extraction.
func (c *Config) Utility(ctx context.Context, instructions, input string) (string, error) {
	resp, err := c.client.Responses.New(ctx, responses.ResponseNewParams{
		Model:        c.utilityModelName(),
		Instructions: openai.String(instructions),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(input),
		},
		Store: openai.Bool(false),
	})

	if err != nil {
		return "", err
	}

	usage := responseToUsage(*resp)
	tool.ReportUsage(ctx, tool.UsageDelta{
		InputTokens:  usage.InputTokens,
		CachedTokens: usage.CachedTokens,
		OutputTokens: usage.OutputTokens,
	})

	return strings.TrimSpace(recoverySummaryOutput(resp)), nil
}

func (c *Config) Models(ctx context.Context) ([]ModelInfo, error) {
	resp, err := c.client.Models.List(ctx)
	if err != nil {
		return nil, err
	}

	var models []ModelInfo

	for _, m := range resp.Data {
		models = append(models, ModelInfo{ID: m.ID})
	}

	return models, nil
}

func DefaultConfig() (*Config, error) {
	client := createClient()

	cfg := &Config{
		client:       &client,
		LargeContext: envBool("WINGMAN_LARGE_CONTEXT"),
	}

	if model := DefaultModel(); model != "" {
		cfg.Model = func() string { return model }
	}

	return cfg, nil
}

// DefaultModel returns the model requested via environment; WINGMAN_MODEL
// takes priority over the OpenAI-standard OPENAI_DEFAULT_MODEL.
func DefaultModel() string {
	if v := os.Getenv("WINGMAN_MODEL"); v != "" {
		return v
	}

	return os.Getenv("OPENAI_DEFAULT_MODEL")
}

// DefaultPlanModel returns the model for plan mode; empty selects the largest
// available model automatically.
func DefaultPlanModel() string {
	return os.Getenv("WINGMAN_MODEL_PLAN")
}

// DefaultUtilityModel returns the model for internal utility calls (recaps,
// compaction summaries); empty selects the smallest available automatically.
func DefaultUtilityModel() string {
	return os.Getenv("WINGMAN_MODEL_UTILITY")
}

// DefaultEffort returns the reasoning effort requested via WINGMAN_EFFORT.
// Empty (or "auto") leaves the role-based default in place. Unrecognized
// values are ignored so a typo cannot silently pin an unexpected effort.
func DefaultEffort() string {
	return effortFromEnv("WINGMAN_EFFORT")
}

// DefaultPlanEffort returns the reasoning effort for plan mode requested via
// WINGMAN_EFFORT_PLAN; empty uses the role-based default.
func DefaultPlanEffort() string {
	return effortFromEnv("WINGMAN_EFFORT_PLAN")
}

func effortFromEnv(name string) string {
	switch v := strings.ToLower(strings.TrimSpace(os.Getenv(name))); v {
	case "none", "low", "medium", "high", "xhigh", "max":
		return v
	default:
		return ""
	}
}

func envBool(name string) bool {
	switch strings.ToLower(os.Getenv(name)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func SandboxDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("WINGMAN_SANDBOX"))) {
	case "off", "false", "0", "no", "disabled":
		return true
	default:
		return false
	}
}

func createClient() openai.Client {
	if url, ok := os.LookupEnv("WINGMAN_URL"); ok {
		baseURL := strings.TrimRight(url, "/") + "/v1"

		token, _ := os.LookupEnv("WINGMAN_TOKEN")

		if token == "" {
			token = "-"
		}

		return openai.NewClient(
			option.WithBaseURL(baseURL),
			option.WithAPIKey(token),
		)
	}

	if token, ok := os.LookupEnv("OPENAI_API_KEY"); ok {
		baseURL := "https://api.openai.com/v1"

		if url, ok := os.LookupEnv("OPENAI_BASE_URL"); ok {
			baseURL = url
		}

		return openai.NewClient(
			option.WithBaseURL(baseURL),
			option.WithAPIKey(token),
		)
	}

	return openai.NewClient(
		option.WithBaseURL("http://localhost:4242/v1"),
		option.WithAPIKey("-"),
	)
}
