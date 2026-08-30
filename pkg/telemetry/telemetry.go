package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	instrumentationName = "github.com/adrianliechti/wingman-agent/pkg/telemetry"
	genAISchemaURL      = "https://opentelemetry.io/schemas/gen-ai-dev/1.42.0-dev"
	inferenceEventName  = "gen_ai.client.inference.operation.details"
	exceptionEventName  = "gen_ai.client.operation.exception"
	captureContentEnv   = "OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT"
	emitEventEnv        = "OTEL_INSTRUMENTATION_GENAI_EMIT_EVENT"
)

const (
	operationChat        = "chat"
	operationInvokeAgent = "invoke_agent"
	operationExecuteTool = "execute_tool"
)

var (
	keyOperationName              = attribute.Key("gen_ai.operation.name")
	keyProviderName               = attribute.Key("gen_ai.provider.name")
	keyAgentName                  = attribute.Key("gen_ai.agent.name")
	keyConversationID             = attribute.Key("gen_ai.conversation.id")
	keyRequestModel               = attribute.Key("gen_ai.request.model")
	keyRequestStream              = attribute.Key("gen_ai.request.stream")
	keyRequestReasoningLevel      = attribute.Key("gen_ai.request.reasoning.level")
	keyResponseID                 = attribute.Key("gen_ai.response.id")
	keyResponseModel              = attribute.Key("gen_ai.response.model")
	keyResponseFinishReasons      = attribute.Key("gen_ai.response.finish_reasons")
	keyResponseTimeToFirstChunk   = attribute.Key("gen_ai.response.time_to_first_chunk")
	keyUsageInputTokens           = attribute.Key("gen_ai.usage.input_tokens")
	keyUsageCacheReadInputTokens  = attribute.Key("gen_ai.usage.cache_read.input_tokens")
	keyUsageCacheWriteInputTokens = attribute.Key("gen_ai.usage.cache_write.input_tokens")
	keyUsageOutputTokens          = attribute.Key("gen_ai.usage.output_tokens")
	keyUsageReasoningOutputTokens = attribute.Key("gen_ai.usage.reasoning.output_tokens")
	keyTokenType                  = attribute.Key("gen_ai.token.type")
	keyToolName                   = attribute.Key("gen_ai.tool.name")
	keyToolType                   = attribute.Key("gen_ai.tool.type")
	keyToolCallID                 = attribute.Key("gen_ai.tool.call.id")
	keyToolDescription            = attribute.Key("gen_ai.tool.description")
	keyMCPMethod                  = attribute.Key("mcp.method.name")
	keyMCPProtocolVersion         = attribute.Key("mcp.protocol.version")
	keyOpenAIAPIType              = attribute.Key("openai.api.type")
	keyErrorType                  = attribute.Key("error.type")
	keyInputMessages              = attribute.Key("gen_ai.input.messages")
	keyOutputMessages             = attribute.Key("gen_ai.output.messages")
	keySystemInstructions         = attribute.Key("gen_ai.system_instructions")
	keyToolDefinitions            = attribute.Key("gen_ai.tool.definitions")
	keyExceptionType              = attribute.Key("exception.type")
	keyExceptionMessage           = attribute.Key("exception.message")
)

var clientDurationBounds = []float64{
	0.01, 0.02, 0.04, 0.08, 0.16, 0.32, 0.64, 1.28,
	2.56, 5.12, 10.24, 20.48, 40.96, 81.92,
}

var agentDurationBounds = []float64{
	0.1, 0.2, 0.4, 0.8, 1.6, 3.2, 6.4, 12.8,
	25.6, 51.2, 102.4, 204.8, 409.6,
}

var callCountBounds = []float64{1, 2, 4, 8, 16, 32, 64, 128}

var tokenBounds = []float64{
	1, 4, 16, 64, 256, 1024, 4096, 16384,
	65536, 262144, 1048576, 4194304, 16777216, 67108864,
}

var mcpDurationBounds = []float64{
	0.01, 0.02, 0.05, 0.1, 0.2, 0.5, 1, 2,
	5, 10, 30, 60, 120, 300,
}

// ContentCaptureMode controls where potentially sensitive GenAI message
// content is recorded. The values match the OpenTelemetry GenAI utilities
// convention and OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT.
type ContentCaptureMode string

const (
	ContentCaptureNoContent    ContentCaptureMode = "NO_CONTENT"
	ContentCaptureSpanOnly     ContentCaptureMode = "SPAN_ONLY"
	ContentCaptureEventOnly    ContentCaptureMode = "EVENT_ONLY"
	ContentCaptureSpanAndEvent ContentCaptureMode = "SPAN_AND_EVENT"
)

func (mode ContentCaptureMode) capturesSpans() bool {
	return mode == ContentCaptureSpanOnly || mode == ContentCaptureSpanAndEvent
}

func (mode ContentCaptureMode) capturesEvents() bool {
	return mode == ContentCaptureEventOnly || mode == ContentCaptureSpanAndEvent
}

// Options configures a Telemetry pipeline. New creates OTLP HTTP/protobuf
// exporters from standard OpenTelemetry environment variables when providers
// are not supplied. Injected providers are owned by the caller and are not shut
// down by Telemetry.
type Options struct {
	ServiceName string
	AgentName   string

	// ProviderName identifies the model service as observed by this client.
	// Well-known values include "openai" and "anthropic"; custom providers are
	// allowed by the GenAI semantic conventions.
	ProviderName  string
	ServerAddress string
	ServerPort    int

	TracerProvider trace.TracerProvider
	MeterProvider  metric.MeterProvider
	LoggerProvider otellog.LoggerProvider
	Propagator     propagation.TextMapPropagator

	DisableTraces  bool
	DisableMetrics bool

	// EmitEvents emits the standard gen_ai.client.inference.operation.details
	// log event and gen_ai.client.operation.exception for failed operations.
	// Events contain operation metadata and usage, but no message content in
	// ContentCaptureNoContent or ContentCaptureSpanOnly mode.
	EmitEvents bool

	// CaptureMessageContent controls whether structured input/output messages,
	// system instructions, and tool definitions are included on inference spans,
	// inference-detail events, both, or neither. The zero value reads
	// OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT and otherwise defaults
	// to ContentCaptureNoContent. Captured content can contain source code,
	// credentials, and other sensitive information.
	CaptureMessageContent ContentCaptureMode
}

// Telemetry owns the GenAI instruments and any SDK providers it created.
// It is safe for concurrent use.
type Telemetry struct {
	tracer trace.Tracer
	meter  metric.Meter
	logger otellog.Logger

	agentName           string
	providerName        string
	serverAddress       string
	serverPort          int
	captureSpanContent  bool
	captureEventContent bool
	propagator          propagation.TextMapPropagator

	clientTokenUsage           metric.Int64Histogram
	clientOperationDuration    metric.Float64Histogram
	clientTimeToFirstChunk     metric.Float64Histogram
	clientTimePerChunk         metric.Float64Histogram
	agentDuration              metric.Float64Histogram
	agentInferenceCalls        metric.Int64Histogram
	agentToolCalls             metric.Int64Histogram
	toolDuration               metric.Float64Histogram
	mcpClientOperationDuration metric.Float64Histogram
	mcpServerOperationDuration metric.Float64Histogram
	mcpClientSessionDuration   metric.Float64Histogram

	ownedTracer *sdktrace.TracerProvider
	ownedMeter  *sdkmetric.MeterProvider
	ownedLogger *sdklog.LoggerProvider

	shutdownOnce sync.Once
	shutdownErr  error
}

// New creates explicitly enabled GenAI telemetry. Unless disabled, traces and
// metrics use injected providers or OTLP HTTP/protobuf exporters. The exporter
// defaults and options are read from standard OTEL_EXPORTER_OTLP_* variables.
func New(ctx context.Context, opts Options) (*Telemetry, error) {
	if environmentBool("OTEL_SDK_DISABLED") {
		return nil, nil
	}
	captureMode, err := resolveCaptureMode(opts.CaptureMessageContent)
	if err != nil {
		return nil, err
	}
	emitEvents := resolveEventEmission(opts.EmitEvents, captureMode)
	return newTelemetry(ctx, opts, !opts.DisableTraces, !opts.DisableMetrics, emitEvents, captureMode)
}

// NewFromEnvironment creates telemetry only for signals configured through
// standard OpenTelemetry exporter variables. A nil result means no enabled
// signal has an exporter configured, or OTEL_SDK_DISABLED is true. Log events
// additionally require OTEL_INSTRUMENTATION_GENAI_EMIT_EVENT=true (or an event
// capture mode), so exporter configuration alone never enables them.
func NewFromEnvironment(ctx context.Context, opts Options) (*Telemetry, error) {
	if environmentBool("OTEL_SDK_DISABLED") {
		return nil, nil
	}
	traces, metrics := configuredSignals()
	captureMode, err := resolveCaptureMode(opts.CaptureMessageContent)
	if err != nil {
		return nil, err
	}
	emitEvents := resolveEventEmission(opts.EmitEvents, captureMode)
	logs := emitEvents && signalConfigured("LOGS")
	traces = traces || opts.TracerProvider != nil
	metrics = metrics || opts.MeterProvider != nil
	logs = logs || (emitEvents && opts.LoggerProvider != nil)
	traces = traces && !opts.DisableTraces
	metrics = metrics && !opts.DisableMetrics
	if !traces && !metrics && !logs {
		return nil, nil
	}
	return newTelemetry(ctx, opts, traces, metrics, logs, captureMode)
}

// EnvironmentConfigured reports whether a trace, metric, or log exporter is
// configured with standard OpenTelemetry environment variables.
func EnvironmentConfigured() bool {
	if environmentBool("OTEL_SDK_DISABLED") {
		return false
	}
	traces, metrics := configuredSignals()
	return traces || metrics || signalConfigured("LOGS")
}

func configuredSignals() (traces, metrics bool) {
	if environmentBool("OTEL_SDK_DISABLED") {
		return false, false
	}
	return signalConfigured("TRACES"), signalConfigured("METRICS")
}

func signalConfigured(signal string) bool {
	if value, ok := os.LookupEnv("OTEL_" + signal + "_EXPORTER"); ok && strings.TrimSpace(value) != "" {
		return !strings.EqualFold(strings.TrimSpace(value), "none")
	}

	const prefix = "OTEL_EXPORTER_OTLP_"
	signalPrefix := prefix + signal + "_"
	for _, item := range os.Environ() {
		name, value, ok := strings.Cut(item, "=")
		if !ok || value == "" || !strings.HasPrefix(name, prefix) {
			continue
		}
		if strings.HasPrefix(name, signalPrefix) {
			return true
		}
		remainder := strings.TrimPrefix(name, prefix)
		if !strings.HasPrefix(remainder, "TRACES_") &&
			!strings.HasPrefix(remainder, "METRICS_") &&
			!strings.HasPrefix(remainder, "LOGS_") &&
			!strings.HasPrefix(remainder, "PROFILES_") {
			return true
		}
	}
	return false
}

func environmentBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func resolveCaptureMode(configured ContentCaptureMode) (ContentCaptureMode, error) {
	raw := strings.TrimSpace(string(configured))
	fromEnvironment := false
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv(captureContentEnv))
		fromEnvironment = true
	}
	if raw == "" {
		return ContentCaptureNoContent, nil
	}

	mode := ContentCaptureMode(strings.ToUpper(raw))
	switch mode {
	case ContentCaptureNoContent,
		ContentCaptureSpanOnly,
		ContentCaptureEventOnly,
		ContentCaptureSpanAndEvent:
		return mode, nil
	default:
		if fromEnvironment {
			return ContentCaptureNoContent, nil
		}
		return "", fmt.Errorf("CaptureMessageContent=%q is unsupported: use NO_CONTENT, SPAN_ONLY, EVENT_ONLY, or SPAN_AND_EVENT", raw)
	}
}

func resolveEventEmission(explicit bool, captureMode ContentCaptureMode) bool {
	emit := captureMode.capturesEvents()
	if value, ok := environmentBoolOverride(emitEventEnv); ok {
		emit = value
	}
	return explicit || emit
}

func environmentBoolOverride(name string) (bool, bool) {
	raw, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(raw) == "" {
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	default:
		return false, true
	}
}

func newTelemetry(ctx context.Context, opts Options, enableTraces, enableMetrics, enableLogs bool, captureMode ContentCaptureMode) (*Telemetry, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	var err error
	if opts.TracerProvider == nil {
		enableTraces, err = directOTLPEnabled("TRACES", enableTraces)
		if err != nil {
			return nil, err
		}
	}
	if opts.MeterProvider == nil {
		enableMetrics, err = directOTLPEnabled("METRICS", enableMetrics)
		if err != nil {
			return nil, err
		}
	}
	if opts.LoggerProvider == nil {
		enableLogs, err = directOTLPEnabled("LOGS", enableLogs)
		if err != nil {
			return nil, err
		}
	}
	if !enableTraces && !enableMetrics && !enableLogs {
		return nil, nil
	}

	res := telemetryResource(ctx, opts.ServiceName)
	t := &Telemetry{
		agentName:           firstNonEmpty(strings.TrimSpace(opts.AgentName), "wingman"),
		providerName:        firstNonEmpty(strings.TrimSpace(opts.ProviderName), "openai"),
		serverAddress:       strings.TrimSpace(opts.ServerAddress),
		serverPort:          opts.ServerPort,
		captureSpanContent:  captureMode.capturesSpans(),
		captureEventContent: captureMode.capturesEvents(),
		propagator:          resolvePropagator(opts.Propagator),
	}

	if enableTraces {
		provider := opts.TracerProvider
		if provider == nil {
			exporter, err := otlptracehttp.New(ctx)
			if err != nil {
				return nil, fmt.Errorf("create OTLP HTTP trace exporter: %w", err)
			}
			t.ownedTracer = sdktrace.NewTracerProvider(
				sdktrace.WithBatcher(exporter),
				sdktrace.WithResource(res),
			)
			provider = t.ownedTracer
		}
		if provider != nil {
			t.tracer = provider.Tracer(instrumentationName, trace.WithSchemaURL(genAISchemaURL))
		}
	}

	if enableMetrics {
		provider := opts.MeterProvider
		if provider == nil {
			exporter, err := otlpmetrichttp.New(ctx)
			if err != nil {
				_ = t.Shutdown(ctx)
				return nil, fmt.Errorf("create OTLP HTTP metric exporter: %w", err)
			}
			reader := sdkmetric.NewPeriodicReader(exporter)
			t.ownedMeter = sdkmetric.NewMeterProvider(
				sdkmetric.WithReader(reader),
				sdkmetric.WithResource(res),
			)
			provider = t.ownedMeter
		}
		if provider != nil {
			t.meter = provider.Meter(instrumentationName, metric.WithSchemaURL(genAISchemaURL))
			if err := t.initMetrics(); err != nil {
				_ = t.Shutdown(ctx)
				return nil, err
			}
		}
	}

	if enableLogs {
		provider := opts.LoggerProvider
		if provider == nil {
			exporter, err := otlploghttp.New(ctx)
			if err != nil {
				_ = t.Shutdown(ctx)
				return nil, fmt.Errorf("create OTLP HTTP log exporter: %w", err)
			}
			t.ownedLogger = sdklog.NewLoggerProvider(
				sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
				sdklog.WithResource(res),
			)
			provider = t.ownedLogger
		}
		if provider != nil {
			t.logger = provider.Logger(instrumentationName, otellog.WithSchemaURL(genAISchemaURL))
		}
	}

	return t, nil
}

func directOTLPEnabled(signal string, requested bool) (bool, error) {
	if !requested {
		return false, nil
	}

	selectorName := "OTEL_" + signal + "_EXPORTER"
	selector := strings.ToLower(strings.TrimSpace(os.Getenv(selectorName)))
	switch selector {
	case "", "otlp":
	case "none":
		return false, nil
	default:
		return false, fmt.Errorf("%s=%q is unsupported: telemetry supports only otlp or none", selectorName, selector)
	}

	protocolName := "OTEL_EXPORTER_OTLP_" + signal + "_PROTOCOL"
	protocol := strings.TrimSpace(os.Getenv(protocolName))
	if protocol == "" {
		protocolName = "OTEL_EXPORTER_OTLP_PROTOCOL"
		protocol = strings.TrimSpace(os.Getenv(protocolName))
	}
	if protocol != "" && !strings.EqualFold(protocol, "http/protobuf") {
		return false, fmt.Errorf("%s=%q is unsupported: telemetry supports only OTLP over HTTP/protobuf", protocolName, protocol)
	}

	return true, nil
}

func telemetryResource(ctx context.Context, serviceName string) *resource.Resource {
	res := resource.DefaultWithContext(ctx)
	serviceName = strings.TrimSpace(serviceName)
	current, ok := res.Set().Value(semconv.ServiceNameKey)
	if serviceName == "" && ok && !strings.HasPrefix(current.AsString(), "unknown_service:") {
		return res
	}
	serviceName = firstNonEmpty(serviceName, "wingman-agent")
	withService, err := resource.Merge(res, resource.NewSchemaless(semconv.ServiceName(serviceName)))
	if err != nil {
		return res
	}
	return withService
}

func (t *Telemetry) initMetrics() error {
	var errs []error
	t.clientTokenUsage, errs = intHistogram(t.meter, errs,
		"gen_ai.client.token.usage", "{token}", "Number of input and output tokens used.", tokenBounds)
	t.clientOperationDuration, errs = histogram(t.meter, errs,
		"gen_ai.client.operation.duration", "s", "GenAI operation duration.", clientDurationBounds)
	t.clientTimeToFirstChunk, errs = histogram(t.meter, errs,
		"gen_ai.client.operation.time_to_first_chunk", "s", "Time to receive the first chunk, measured from when the client issues the generation request to when the first chunk is received in the response stream.", clientDurationBounds)
	t.clientTimePerChunk, errs = histogram(t.meter, errs,
		"gen_ai.client.operation.time_per_output_chunk", "s", "Time per output chunk, recorded for each chunk received after the first one, measured as the time elapsed from the end of the previous chunk to the end of the current chunk.", clientDurationBounds)
	t.agentDuration, errs = histogram(t.meter, errs,
		"gen_ai.invoke_agent.duration", "s", "The end-to-end duration of a single in-process agent invocation, from the moment the invocation starts until the agent emits the last chunk of its final response or terminates with an error.", agentDurationBounds)
	t.agentInferenceCalls, errs = intHistogram(t.meter, errs,
		"gen_ai.invoke_agent.inference_calls", "{inference_call}", "The number of inference (model) calls a GenAI agent makes during a single invocation.", callCountBounds)
	t.agentToolCalls, errs = intHistogram(t.meter, errs,
		"gen_ai.invoke_agent.tool_calls", "{tool_call}", "The number of tool calls a GenAI agent makes during a single invocation.", callCountBounds)
	t.toolDuration, errs = histogram(t.meter, errs,
		"gen_ai.execute_tool.duration", "s", "The duration of a single tool execution.", clientDurationBounds)
	t.mcpClientOperationDuration, errs = histogram(t.meter, errs,
		"mcp.client.operation.duration", "s", "The duration of the MCP request or notification as observed on the sender from the time it was sent until the response or ack is received.", mcpDurationBounds)
	t.mcpServerOperationDuration, errs = histogram(t.meter, errs,
		"mcp.server.operation.duration", "s", "The duration of the MCP request or notification as observed on the receiver from the time it was received until the result or ack is sent.", mcpDurationBounds)
	t.mcpClientSessionDuration, errs = histogram(t.meter, errs,
		"mcp.client.session.duration", "s", "The duration of the MCP session as observed on the MCP client.", mcpDurationBounds)
	return errors.Join(errs...)
}

func histogram(meter metric.Meter, errs []error, name, unit, description string, bounds []float64) (metric.Float64Histogram, []error) {
	options := []metric.Float64HistogramOption{
		metric.WithUnit(unit),
		metric.WithDescription(description),
	}
	if len(bounds) > 0 {
		options = append(options, metric.WithExplicitBucketBoundaries(bounds...))
	}
	instrument, err := meter.Float64Histogram(name, options...)
	if err != nil {
		errs = append(errs, err)
	}
	return instrument, errs
}

func intHistogram(meter metric.Meter, errs []error, name, unit, description string, bounds []float64) (metric.Int64Histogram, []error) {
	instrument, err := meter.Int64Histogram(name,
		metric.WithUnit(unit),
		metric.WithDescription(description),
		metric.WithExplicitBucketBoundaries(bounds...),
	)
	if err != nil {
		errs = append(errs, err)
	}
	return instrument, errs
}

// ForceFlush immediately exports all telemetry owned by t.
func (t *Telemetry) ForceFlush(ctx context.Context) error {
	if t == nil {
		return nil
	}
	var errs []error
	if t.ownedTracer != nil {
		errs = append(errs, t.ownedTracer.ForceFlush(ctx))
	}
	if t.ownedMeter != nil {
		errs = append(errs, t.ownedMeter.ForceFlush(ctx))
	}
	if t.ownedLogger != nil {
		errs = append(errs, t.ownedLogger.ForceFlush(ctx))
	}
	return errors.Join(errs...)
}

// Shutdown flushes and stops SDK providers created by t. It is idempotent and
// does not shut down providers injected through Options.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	if t == nil {
		return nil
	}
	t.shutdownOnce.Do(func() {
		var errs []error
		if t.ownedTracer != nil {
			errs = append(errs, t.ownedTracer.Shutdown(ctx))
		}
		if t.ownedMeter != nil {
			errs = append(errs, t.ownedMeter.Shutdown(ctx))
		}
		if t.ownedLogger != nil {
			errs = append(errs, t.ownedLogger.Shutdown(ctx))
		}
		t.shutdownErr = errors.Join(errs...)
	})
	return t.shutdownErr
}

// Outcome describes whether an instrumented operation succeeded.
type Outcome struct {
	Err       error
	ErrorType string
}

func (o Outcome) errorType() string {
	if value := strings.TrimSpace(o.ErrorType); value != "" {
		return value
	}
	if o.Err == nil {
		return ""
	}
	if errors.Is(o.Err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(o.Err, context.Canceled) {
		return "canceled"
	}
	var netErr net.Error
	if errors.As(o.Err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	return "_OTHER"
}

func appendOutcome(attrs []attribute.KeyValue, outcome Outcome) []attribute.KeyValue {
	errorType := outcome.errorType()
	if errorType == "" {
		return attrs
	}
	return append(attrs, keyErrorType.String(errorType))
}

func recordOutcome(span trace.Span, attrs []attribute.KeyValue, outcome Outcome) []attribute.KeyValue {
	return recordOutcomeWithDescription(span, attrs, outcome, outcome.errorType())
}

func recordOutcomeWithDescription(span trace.Span, attrs []attribute.KeyValue, outcome Outcome, description string) []attribute.KeyValue {
	errorType := outcome.errorType()
	attrs = appendOutcome(attrs, outcome)
	if errorType == "" {
		return attrs
	}
	if span != nil {
		span.SetAttributes(keyErrorType.String(errorType))
		span.SetStatus(codes.Error, firstNonEmpty(description, errorType))
		if outcome.Err != nil {
			span.RecordError(outcome.Err)
		}
	}
	return attrs
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func metricOptions(attrs []attribute.KeyValue) metric.RecordOption {
	return metric.WithAttributes(attrs...)
}

type invocationContextKey struct{}

// AgentRequest contains attributes available when an in-process agent starts.
type AgentRequest struct {
	Name           string
	ConversationID string
}

// AgentInvocation represents one in-process invocation and owns its per-turn
// inference/tool call counters.
type AgentInvocation struct {
	telemetry *Telemetry
	ctx       context.Context
	span      trace.Span
	started   time.Time
	attrs     []attribute.KeyValue

	inferenceCalls atomic.Int64
	toolCalls      atomic.Int64
	usageReports   atomic.Int64
	inputTokens    atomic.Int64
	outputTokens   atomic.Int64
	endOnce        sync.Once
}

// StartAgent starts an invoke_agent INTERNAL span and returns a context that
// must be used for all model and tool operations in the invocation.
func (t *Telemetry) StartAgent(ctx context.Context, req AgentRequest) (context.Context, *AgentInvocation) {
	if t == nil {
		return ctx, nil
	}
	name := firstNonEmpty(strings.TrimSpace(req.Name), t.agentName)
	spanAttrs := []attribute.KeyValue{keyOperationName.String(operationInvokeAgent)}
	metricAttrs := make([]attribute.KeyValue, 0, 1)
	if name != "" {
		spanAttrs = append(spanAttrs, keyAgentName.String(name))
		metricAttrs = append(metricAttrs, keyAgentName.String(name))
	}
	if req.ConversationID != "" {
		spanAttrs = append(spanAttrs, keyConversationID.String(req.ConversationID))
	}

	var span trace.Span
	started := time.Now()
	if t.tracer != nil {
		spanName := operationInvokeAgent
		if name != "" {
			spanName += " " + name
		}
		ctx, span = t.tracer.Start(ctx, spanName,
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithAttributes(spanAttrs...),
			trace.WithTimestamp(started),
		)
	}
	invocation := &AgentInvocation{
		telemetry: t,
		ctx:       ctx,
		span:      span,
		started:   started,
		attrs:     metricAttrs,
	}
	return context.WithValue(ctx, invocationContextKey{}, invocation), invocation
}

// End completes the agent span and records its duration and per-invocation
// inference/tool call distributions.
func (invocation *AgentInvocation) End(outcome Outcome) {
	if invocation == nil {
		return
	}
	invocation.endOnce.Do(func() {
		ended := time.Now()
		attrs := recordOutcome(invocation.span, append([]attribute.KeyValue(nil), invocation.attrs...), outcome)
		t := invocation.telemetry
		if t.agentDuration != nil {
			t.agentDuration.Record(invocation.ctx, ended.Sub(invocation.started).Seconds(), metricOptions(attrs))
		}
		if t.agentInferenceCalls != nil {
			t.agentInferenceCalls.Record(invocation.ctx, invocation.inferenceCalls.Load(), metricOptions(invocation.attrs))
		}
		if t.agentToolCalls != nil {
			t.agentToolCalls.Record(invocation.ctx, invocation.toolCalls.Load(), metricOptions(invocation.attrs))
		}
		if invocation.span != nil {
			if invocation.usageReports.Load() > 0 {
				invocation.span.SetAttributes(
					keyUsageInputTokens.Int64(invocation.inputTokens.Load()),
					keyUsageOutputTokens.Int64(invocation.outputTokens.Load()),
				)
			}
			invocation.span.End(trace.WithTimestamp(ended))
		}
	})
}

type inferenceContextKey struct{}

// InferenceRequest contains the stable attributes of one logical model call.
type InferenceRequest struct {
	Operation      string
	Model          string
	ConversationID string
	Streaming      bool
	ReasoningLevel string
	Content        InferenceContent
}

// InferenceContent contains structured, potentially sensitive attributes used
// on inference spans and by the standard inference-details event. Values must
// follow the GenAI semantic-convention JSON schemas.
type InferenceContent struct {
	InputMessages      attribute.Value
	SystemInstructions attribute.Value
	ToolDefinitions    attribute.Value
}

// InferenceResult contains provider metadata and billable usage available at
// the end of a logical model call.
type InferenceResult struct {
	Outcome
	ResponseID     string
	ResponseModel  string
	FinishReasons  []string
	OutputMessages attribute.Value
	Usage          *TokenUsage
}

// TokenUsage is the provider-reported token breakdown. A nil Usage on
// InferenceResult means the provider did not report usage; a non-nil all-zero
// value is still a valid report. InputTokens and OutputTokens are the aggregate
// counts and already include their respective detail fields.
type TokenUsage struct {
	InputTokens           int64
	CacheReadInputTokens  int64
	CacheWriteInputTokens int64
	OutputTokens          int64
	ReasoningOutputTokens int64
}

// Inference represents one logical GenAI client operation, including any
// transparent retries.
type Inference struct {
	telemetry  *Telemetry
	invocation *AgentInvocation
	ctx        context.Context
	span       trace.Span
	started    time.Time
	attrs      []attribute.KeyValue
	streaming  bool
	eventAttrs []attribute.KeyValue
	content    InferenceContent

	chunkMu          sync.Mutex
	firstChunkSeen   bool
	lastOutputChunk  time.Time
	timeToFirstChunk time.Duration
	outputChunkTimes []time.Duration
	endOnce          sync.Once
}

// StartInference starts a CLIENT inference span. The returned context should
// cover every transport retry belonging to the same logical operation.
func (t *Telemetry) StartInference(ctx context.Context, req InferenceRequest) (context.Context, *Inference) {
	if t == nil {
		return ctx, nil
	}
	var invocation *AgentInvocation
	if active, ok := ctx.Value(invocationContextKey{}).(*AgentInvocation); ok {
		invocation = active
		invocation.inferenceCalls.Add(1)
	}
	operation := firstNonEmpty(strings.TrimSpace(req.Operation), operationChat)
	metricAttrs := t.clientAttributes(operation, req.Model)
	spanAttrs := append([]attribute.KeyValue(nil), metricAttrs...)
	if t.providerName == "openai" {
		spanAttrs = append(spanAttrs, keyOpenAIAPIType.String("responses"))
	}
	if req.Streaming {
		spanAttrs = append(spanAttrs, keyRequestStream.Bool(true))
	}
	if reasoningLevel := strings.TrimSpace(req.ReasoningLevel); reasoningLevel != "" {
		spanAttrs = append(spanAttrs, keyRequestReasoningLevel.String(reasoningLevel))
	}
	if req.ConversationID != "" {
		spanAttrs = append(spanAttrs, keyConversationID.String(req.ConversationID))
	}

	var span trace.Span
	started := time.Now()
	if t.tracer != nil {
		spanName := operation
		if req.Model != "" {
			spanName += " " + req.Model
		}
		ctx, span = t.tracer.Start(ctx, spanName,
			trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(spanAttrs...),
			trace.WithTimestamp(started),
		)
	}
	inference := &Inference{
		telemetry:  t,
		ctx:        ctx,
		span:       span,
		started:    started,
		attrs:      metricAttrs,
		streaming:  req.Streaming,
		eventAttrs: append([]attribute.KeyValue(nil), spanAttrs...),
		invocation: invocation,
	}
	if t.captureSpanContent || t.captureEventContent {
		inference.content = req.Content
	}
	return context.WithValue(ctx, inferenceContextKey{}, inference), inference
}

// CapturesMessageContent reports whether structured message content will be
// recorded on an enabled GenAI inference span or inference-detail event.
func (t *Telemetry) CapturesMessageContent() bool {
	return t != nil &&
		(t.captureSpanContent && t.tracer != nil ||
			t.captureEventContent && t.logger != nil)
}

// SetContent replaces request content for the active logical inference. This
// is useful when an automatic retry changes the effective prompt.
func (inference *Inference) SetContent(content InferenceContent) {
	if inference == nil || !inference.telemetry.CapturesMessageContent() {
		return
	}
	inference.chunkMu.Lock()
	inference.content = content
	inference.chunkMu.Unlock()
}

func (t *Telemetry) clientAttributes(operation, model string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		keyOperationName.String(operation),
		keyProviderName.String(t.providerName),
	}
	if model != "" {
		attrs = append(attrs, keyRequestModel.String(model))
	}
	if t.serverAddress != "" {
		attrs = append(attrs, semconv.ServerAddress(t.serverAddress))
		if t.serverPort > 0 {
			attrs = append(attrs, semconv.ServerPort(t.serverPort))
		}
	}
	return attrs
}

// ObserveResponseChunk records streaming time-to-first-chunk for the active
// inference operation. Call it for every response-stream event; only the first
// event is recorded.
func ObserveResponseChunk(ctx context.Context) {
	inference, ok := ctx.Value(inferenceContextKey{}).(*Inference)
	if !ok || inference == nil || !inference.streaming {
		return
	}
	now := time.Now()
	inference.chunkMu.Lock()
	defer inference.chunkMu.Unlock()
	if inference.firstChunkSeen {
		return
	}
	inference.firstChunkSeen = true
	inference.timeToFirstChunk = now.Sub(inference.started)
}

// ObserveOutputChunk records time between model-output chunks for the active
// inference operation. The first output chunk establishes the baseline.
func ObserveOutputChunk(ctx context.Context) {
	inference, ok := ctx.Value(inferenceContextKey{}).(*Inference)
	if !ok || inference == nil || !inference.streaming {
		return
	}
	now := time.Now()
	inference.chunkMu.Lock()
	defer inference.chunkMu.Unlock()
	if inference.lastOutputChunk.IsZero() {
		inference.lastOutputChunk = now
		return
	}
	inference.outputChunkTimes = append(inference.outputChunkTimes, now.Sub(inference.lastOutputChunk))
	inference.lastOutputChunk = now
}

// End completes an inference span and records duration and token usage.
func (inference *Inference) End(result InferenceResult) {
	if inference == nil {
		return
	}
	inference.endOnce.Do(func() {
		ended := time.Now()
		attrs := append([]attribute.KeyValue(nil), inference.attrs...)
		if result.ResponseID != "" && inference.span != nil {
			inference.span.SetAttributes(keyResponseID.String(result.ResponseID))
		}
		if result.ResponseModel != "" {
			attrs = append(attrs, keyResponseModel.String(result.ResponseModel))
			if inference.span != nil {
				inference.span.SetAttributes(keyResponseModel.String(result.ResponseModel))
			}
		}
		if len(result.FinishReasons) > 0 && inference.span != nil {
			inference.span.SetAttributes(keyResponseFinishReasons.StringSlice(result.FinishReasons))
		}
		inference.chunkMu.Lock()
		timeToFirstChunk := inference.timeToFirstChunk
		outputChunkTimes := append([]time.Duration(nil), inference.outputChunkTimes...)
		content := inference.content
		inference.chunkMu.Unlock()
		if timeToFirstChunk > 0 && inference.span != nil {
			inference.span.SetAttributes(keyResponseTimeToFirstChunk.Float64(timeToFirstChunk.Seconds()))
		}
		if result.Usage != nil && inference.span != nil {
			inference.span.SetAttributes(
				keyUsageInputTokens.Int64(result.Usage.InputTokens),
				keyUsageOutputTokens.Int64(result.Usage.OutputTokens),
			)
			if result.Usage.CacheReadInputTokens > 0 {
				inference.span.SetAttributes(keyUsageCacheReadInputTokens.Int64(result.Usage.CacheReadInputTokens))
			}
			if result.Usage.CacheWriteInputTokens > 0 {
				inference.span.SetAttributes(keyUsageCacheWriteInputTokens.Int64(result.Usage.CacheWriteInputTokens))
			}
			if result.Usage.ReasoningOutputTokens > 0 {
				inference.span.SetAttributes(keyUsageReasoningOutputTokens.Int64(result.Usage.ReasoningOutputTokens))
			}
		}
		if t := inference.telemetry; t.captureSpanContent && inference.span != nil {
			contentAttrs := make([]attribute.KeyValue, 0, 4)
			contentAttrs = appendValue(contentAttrs, keyInputMessages, content.InputMessages)
			contentAttrs = appendValue(contentAttrs, keySystemInstructions, content.SystemInstructions)
			contentAttrs = appendValue(contentAttrs, keyToolDefinitions, content.ToolDefinitions)
			contentAttrs = appendValue(contentAttrs, keyOutputMessages, result.OutputMessages)
			inference.span.SetAttributes(contentAttrs...)
		}
		durationAttrs := recordOutcome(inference.span, append([]attribute.KeyValue(nil), attrs...), result.Outcome)
		t := inference.telemetry
		if t.clientOperationDuration != nil {
			t.clientOperationDuration.Record(inference.ctx, ended.Sub(inference.started).Seconds(), metricOptions(durationAttrs))
		}
		if timeToFirstChunk > 0 && t.clientTimeToFirstChunk != nil {
			t.clientTimeToFirstChunk.Record(inference.ctx, timeToFirstChunk.Seconds(), metricOptions(attrs))
		}
		if t.clientTimePerChunk != nil {
			for _, duration := range outputChunkTimes {
				t.clientTimePerChunk.Record(inference.ctx, duration.Seconds(), metricOptions(attrs))
			}
		}
		if result.Usage != nil && t.clientTokenUsage != nil {
			inputAttrs := append(append([]attribute.KeyValue(nil), attrs...), keyTokenType.String("input"))
			outputAttrs := append(append([]attribute.KeyValue(nil), attrs...), keyTokenType.String("output"))
			t.clientTokenUsage.Record(inference.ctx, result.Usage.InputTokens, metricOptions(inputAttrs))
			t.clientTokenUsage.Record(inference.ctx, result.Usage.OutputTokens, metricOptions(outputAttrs))
		}
		if result.Usage != nil && inference.invocation != nil {
			inference.invocation.usageReports.Add(1)
			inference.invocation.inputTokens.Add(result.Usage.InputTokens)
			inference.invocation.outputTokens.Add(result.Usage.OutputTokens)
		}
		inference.emitEvent(result, content, timeToFirstChunk, ended)
		inference.emitExceptionEvent(result.Outcome, ended)
		if inference.span != nil {
			inference.span.End(trace.WithTimestamp(ended))
		}
	})
}

func (inference *Inference) emitEvent(result InferenceResult, content InferenceContent, timeToFirstChunk time.Duration, timestamp time.Time) {
	if inference.telemetry.logger == nil {
		return
	}
	attrs := append([]attribute.KeyValue(nil), inference.eventAttrs...)
	if result.ResponseID != "" {
		attrs = append(attrs, keyResponseID.String(result.ResponseID))
	}
	if result.ResponseModel != "" {
		attrs = append(attrs, keyResponseModel.String(result.ResponseModel))
	}
	if len(result.FinishReasons) > 0 {
		attrs = append(attrs, keyResponseFinishReasons.StringSlice(result.FinishReasons))
	}
	if timeToFirstChunk > 0 {
		attrs = append(attrs, keyResponseTimeToFirstChunk.Float64(timeToFirstChunk.Seconds()))
	}
	if result.Usage != nil {
		attrs = append(attrs,
			keyUsageInputTokens.Int64(result.Usage.InputTokens),
			keyUsageOutputTokens.Int64(result.Usage.OutputTokens),
		)
		if result.Usage.CacheReadInputTokens > 0 {
			attrs = append(attrs, keyUsageCacheReadInputTokens.Int64(result.Usage.CacheReadInputTokens))
		}
		if result.Usage.CacheWriteInputTokens > 0 {
			attrs = append(attrs, keyUsageCacheWriteInputTokens.Int64(result.Usage.CacheWriteInputTokens))
		}
		if result.Usage.ReasoningOutputTokens > 0 {
			attrs = append(attrs, keyUsageReasoningOutputTokens.Int64(result.Usage.ReasoningOutputTokens))
		}
	}
	if errorType := result.Outcome.errorType(); errorType != "" {
		attrs = append(attrs, keyErrorType.String(errorType))
	}
	if inference.telemetry.captureEventContent {
		attrs = appendValue(attrs, keyInputMessages, content.InputMessages)
		attrs = appendValue(attrs, keySystemInstructions, content.SystemInstructions)
		attrs = appendValue(attrs, keyToolDefinitions, content.ToolDefinitions)
		attrs = appendValue(attrs, keyOutputMessages, result.OutputMessages)
	}

	var record otellog.Record
	record.SetEventName(inferenceEventName)
	record.SetTimestamp(timestamp)
	record.SetObservedTimestamp(timestamp)
	record.AddAttributes(attrs...)
	inference.telemetry.logger.Emit(inference.ctx, record)
}

func (inference *Inference) emitExceptionEvent(outcome Outcome, timestamp time.Time) {
	if inference.telemetry.logger == nil {
		return
	}
	errorType := outcome.errorType()
	if errorType == "" {
		return
	}
	attrs := []attribute.KeyValue{keyExceptionType.String(exceptionType(outcome))}
	if outcome.Err != nil {
		attrs = append(attrs, keyExceptionMessage.String(outcome.Err.Error()))
	}

	var record otellog.Record
	record.SetEventName(exceptionEventName)
	record.SetTimestamp(timestamp)
	record.SetObservedTimestamp(timestamp)
	record.SetSeverity(otellog.SeverityWarn)
	record.SetSeverityText("WARN")
	record.AddAttributes(attrs...)
	inference.telemetry.logger.Emit(inference.ctx, record)
}

func exceptionType(outcome Outcome) string {
	if outcome.Err == nil {
		return outcome.errorType()
	}
	typeOf := reflect.TypeOf(outcome.Err)
	if typeOf.PkgPath() == "" && typeOf.Name() == "" {
		return typeOf.String()
	}
	return typeOf.PkgPath() + "." + typeOf.Name()
}

func appendValue(attrs []attribute.KeyValue, key attribute.Key, value attribute.Value) []attribute.KeyValue {
	if value.Type() == attribute.EMPTY {
		return attrs
	}
	return append(attrs, attribute.KeyValue{Key: key, Value: value})
}

// ToolRequest contains metadata about a tool execution. Arguments are captured
// only when GenAI message-content capture is enabled.
type ToolRequest struct {
	Name               string
	Description        string
	CallID             string
	Type               string
	AgentName          string
	MCPMethod          string
	MCPProtocolVersion string
	Arguments          any
}

// ToolExecution represents one bounded agent-side tool execution.
type ToolExecution struct {
	telemetry *Telemetry
	ctx       context.Context
	span      trace.Span
	name      string
	started   time.Time
	attrs     []attribute.KeyValue

	outcomeMu         sync.Mutex
	protocolOutcome   Outcome
	statusDescription string
	result            any
	resultSet         bool
	endOnce           sync.Once
}

type toolContextKey struct{}

// StartTool starts an execute_tool INTERNAL span. Potentially sensitive
// arguments are recorded only when span content capture is enabled.
func (t *Telemetry) StartTool(ctx context.Context, req ToolRequest) (context.Context, *ToolExecution) {
	if t == nil {
		return ctx, nil
	}
	if invocation, ok := ctx.Value(invocationContextKey{}).(*AgentInvocation); ok {
		invocation.toolCalls.Add(1)
	}
	toolType := firstNonEmpty(strings.TrimSpace(req.Type), "function")
	agentName := firstNonEmpty(strings.TrimSpace(req.AgentName), t.agentName)
	spanAttrs := []attribute.KeyValue{
		keyOperationName.String(operationExecuteTool),
		keyToolName.String(req.Name),
		keyToolType.String(toolType),
	}
	metricAttrs := []attribute.KeyValue{
		keyToolName.String(req.Name),
		keyToolType.String(toolType),
	}
	if agentName != "" {
		spanAttrs = append(spanAttrs, keyAgentName.String(agentName))
		metricAttrs = append(metricAttrs, keyAgentName.String(agentName))
	}
	if req.CallID != "" {
		spanAttrs = append(spanAttrs, keyToolCallID.String(req.CallID))
	}
	if req.Description != "" {
		spanAttrs = append(spanAttrs, keyToolDescription.String(req.Description))
	}
	mcpMethod := strings.TrimSpace(req.MCPMethod)
	if mcpMethod != "" {
		spanAttrs = append(spanAttrs, keyMCPMethod.String(mcpMethod))
	}
	if protocolVersion := strings.TrimSpace(req.MCPProtocolVersion); protocolVersion != "" {
		spanAttrs = append(spanAttrs, keyMCPProtocolVersion.String(protocolVersion))
	}
	if t.captureSpanContent {
		if arguments, ok := StructuredObjectValue(req.Arguments); ok {
			spanAttrs = append(spanAttrs, attribute.KeyValue{Key: keyToolCallArguments, Value: arguments})
		}
	}

	var span trace.Span
	started := time.Now()
	if t.tracer != nil {
		spanName := operationExecuteTool + " " + req.Name
		ctx, span = t.tracer.Start(ctx, spanName,
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithAttributes(spanAttrs...),
			trace.WithTimestamp(started),
		)
	}
	execution := &ToolExecution{
		telemetry: t,
		ctx:       ctx,
		span:      span,
		name:      req.Name,
		started:   started,
		attrs:     metricAttrs,
	}
	return context.WithValue(ctx, toolContextKey{}, execution), execution
}

// SetResult associates a successful tool result with the active execution.
// The value is emitted only when span content capture is enabled and End is
// called without an error outcome.
func (execution *ToolExecution) SetResult(result any) {
	if execution == nil || !execution.telemetry.captureSpanContent {
		return
	}
	execution.outcomeMu.Lock()
	execution.result = result
	execution.resultSet = true
	execution.outcomeMu.Unlock()
}

func (execution *ToolExecution) rememberProtocolOutcome(outcome Outcome, description string) {
	if execution == nil || outcome.errorType() == "" {
		return
	}
	execution.outcomeMu.Lock()
	execution.protocolOutcome = outcome
	execution.statusDescription = description
	execution.outcomeMu.Unlock()
}

func (execution *ToolExecution) resolvedOutcome(outcome Outcome) (Outcome, string) {
	execution.outcomeMu.Lock()
	defer execution.outcomeMu.Unlock()
	if outcome.ErrorType == "_OTHER" && execution.protocolOutcome.errorType() != "" {
		return execution.protocolOutcome, execution.statusDescription
	}
	return outcome, ""
}

// End completes a tool span and records its duration.
func (execution *ToolExecution) End(outcome Outcome) {
	if execution == nil {
		return
	}
	execution.endOnce.Do(func() {
		ended := time.Now()
		outcome, description := execution.resolvedOutcome(outcome)
		execution.outcomeMu.Lock()
		result, resultSet := execution.result, execution.resultSet
		execution.outcomeMu.Unlock()
		if outcome.errorType() == "" && resultSet && execution.span != nil {
			if value, ok := StructuredObjectValue(result); ok {
				execution.span.SetAttributes(attribute.KeyValue{Key: keyToolCallResult, Value: value})
			}
		}
		attrs := recordOutcomeWithDescription(execution.span, append([]attribute.KeyValue(nil), execution.attrs...), outcome, firstNonEmpty(description, outcome.errorType()))
		if execution.telemetry.toolDuration != nil {
			execution.telemetry.toolDuration.Record(execution.ctx, ended.Sub(execution.started).Seconds(), metricOptions(attrs))
		}
		if execution.span != nil {
			execution.span.End(trace.WithTimestamp(ended))
		}
	})
}
