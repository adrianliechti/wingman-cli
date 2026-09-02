package agent

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"

	"github.com/adrianliechti/wingman-agent/pkg/agent/hook"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/model"
	"github.com/adrianliechti/wingman-agent/pkg/telemetry"
)

const (
	DefaultMaxTurns         = 400
	DefaultMaxParallelTools = 8
	DefaultToolTimeout      = 10 * time.Minute

	DefaultContextWindow = 400_000

	DefaultReserveTokens = 32_000
)

func ContextWindowFor(id string) int {
	if candidate, ok := model.Find(id); ok {
		window := candidate.ContextTokens()
		if window > 0 {
			return window
		}
	}

	return DefaultContextWindow
}

// ModelOption is a resolved model role. Efforts lists the supported reasoning
// efforts in ascending order; empty means unrestricted.
type ModelOption struct {
	ID      string
	Efforts []string
}

type Config struct {
	client *openai.Client

	// Telemetry instruments agent, model, and tool operations. DefaultConfig
	// initializes it when standard OTEL exporter variables are present. Library
	// callers may inject a separately configured pipeline instead.
	Telemetry *telemetry.Telemetry

	Model        func() string
	Effort       func() string
	Tools        func() []tool.Tool
	Instructions func() string

	// RoleModel resolves "main", "plan", and "utility" role models. An empty
	// role names the currently inherited model and is used for effort clamping.
	// ok=false or an empty ID keeps the inherited model. Nil disables role
	// overrides.
	RoleModel func(role string) (ModelOption, bool)

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

	ReserveTokens int
}

func (c *Config) Derive() *Config {
	return &Config{
		client:       c.client,
		Telemetry:    c.Telemetry,
		Model:        c.Model,
		Effort:       c.Effort,
		Tools:        c.Tools,
		Instructions: c.Instructions,
		RoleModel:    c.RoleModel,

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
		ReserveTokens: c.ReserveTokens,
	}
}

func (c *Config) utilityModelName() string {
	if c.RoleModel != nil {
		if option, ok := c.RoleModel("utility"); ok && option.ID != "" {
			return option.ID
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
	modelID := c.utilityModelName()
	captureContent := c.Telemetry.CapturesMessageContent()
	inferenceRequest := telemetry.InferenceRequest{
		Model:          modelID,
		ConversationID: conversationID(ctx, c.CacheKey),
	}
	if captureContent {
		inferenceRequest.Content = telemetry.InferenceContent{
			InputMessages:      telemetryStringInput(input),
			SystemInstructions: telemetrySystemInstructions(instructions),
		}
	}
	ctx, operation := c.Telemetry.StartInference(ctx, inferenceRequest)
	resp, err := c.client.Responses.New(ctx, responses.ResponseNewParams{
		Model:        modelID,
		Instructions: openai.String(instructions),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(input),
		},
		Store: openai.Bool(false),
	})

	if err != nil {
		operation.End(telemetry.InferenceResult{Outcome: telemetryOutcome(err)})
		return "", err
	}

	usage := responseToUsage(*resp)
	operation.End(inferenceResult(resp, usage, nil, captureContent))
	tool.ReportUsage(ctx, tool.UsageDelta{
		InputTokens:              usage.InputTokens,
		OutputTokens:             usage.OutputTokens,
		ReasoningTokens:          usage.ReasoningTokens,
		CacheReadInputTokens:     usage.CacheReadInputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
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
	tel, err := telemetry.NewFromEnvironment(context.Background(), defaultTelemetryOptions())
	if err != nil {
		return nil, fmt.Errorf("configure OpenTelemetry: %w", err)
	}

	cfg := &Config{client: &client, Telemetry: tel}

	if model := DefaultModel(); model != "" {
		cfg.Model = func() string { return model }
	}

	return cfg, nil
}

func defaultTelemetryOptions() telemetry.Options {
	baseURL, _ := clientConfig()
	provider := "wingman"
	switch {
	case envPresent("WINGMAN_URL"):
		provider = "wingman"
	case envPresent("OPENAI_API_KEY"):
		provider = "openai"
	case envPresent("OPENROUTER_API_KEY"):
		provider = "openrouter"
	case envPresent("OLLAMA_HOST") || envPresent("OLLAMA_API_KEY"):
		provider = "ollama"
	}

	opts := telemetry.Options{
		AgentName:    "wingman",
		ProviderName: provider,
	}
	if parsed, err := url.Parse(baseURL); err == nil {
		opts.ServerAddress = parsed.Hostname()
		if port := parsed.Port(); port != "" {
			opts.ServerPort, _ = strconv.Atoi(port)
		} else {
			switch strings.ToLower(parsed.Scheme) {
			case "http":
				opts.ServerPort = 80
			case "https":
				opts.ServerPort = 443
			}
		}
	}
	return opts
}

func envPresent(name string) bool {
	_, ok := os.LookupEnv(name)
	return ok
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

func SandboxDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("WINGMAN_SANDBOX"))) {
	case "off", "false", "0", "no", "disabled":
		return true
	default:
		return false
	}
}

func createClient() openai.Client {
	baseURL, token := clientConfig()
	return openai.NewClient(
		option.WithBaseURL(baseURL),
		option.WithAPIKey(token),
	)
}

func clientConfig() (baseURL, token string) {
	providers := []func() (string, string, bool){
		wingmanConfig,
		openAIConfig,
		openRouterConfig,
		ollamaConfig,
	}

	for _, config := range providers {
		if baseURL, token, ok := config(); ok {
			return baseURL, token
		}
	}

	return "http://localhost:4242/v1", "-"
}

func wingmanConfig() (baseURL, token string, ok bool) {
	url, ok := os.LookupEnv("WINGMAN_URL")
	if !ok {
		return "", "", false
	}

	token = os.Getenv("WINGMAN_TOKEN")
	if token == "" {
		token = "-"
	}

	return strings.TrimRight(url, "/") + "/v1", token, true
}

func openAIConfig() (baseURL, token string, ok bool) {
	token, ok = os.LookupEnv("OPENAI_API_KEY")
	if !ok {
		return "", "", false
	}

	baseURL = os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}

	return baseURL, token, true
}

func openRouterConfig() (baseURL, token string, ok bool) {
	token, ok = os.LookupEnv("OPENROUTER_API_KEY")
	if !ok {
		return "", "", false
	}

	return "https://openrouter.ai/api/v1", token, true
}

func ollamaConfig() (baseURL, token string, ok bool) {
	host := os.Getenv("OLLAMA_HOST")
	token = os.Getenv("OLLAMA_API_KEY")
	if host == "" && token == "" {
		return "", "", false
	}
	if host == "" {
		host = "https://ollama.com"
	} else if !strings.Contains(host, "://") {
		host = "http://" + host
	}
	if token == "" {
		token = "-"
	}

	return strings.TrimRight(host, "/") + "/v1", token, true
}
