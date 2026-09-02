package telemetry

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

var (
	keyMCPResourceURI    = attribute.Key("mcp.resource.uri")
	keyMCPSessionID      = attribute.Key("mcp.session.id")
	keyGenAIPromptName   = attribute.Key("gen_ai.prompt.name")
	keyToolCallArguments = attribute.Key("gen_ai.tool.call.arguments")
	keyToolCallResult    = attribute.Key("gen_ai.tool.call.result")
)

// MCPTransport describes the transport used for an MCP client operation or
// session. NetworkTransport is normally "pipe" for stdio or "tcp" for HTTP.
type MCPTransport struct {
	NetworkTransport       string
	NetworkProtocolName    string
	NetworkProtocolVersion string
	ServerAddress          string
	ServerPort             int
}

// MCPClientRequest contains attributes known when an outbound MCP request or
// notification starts. Method is required. ResourceURI and content fields are
// excluded from metrics to avoid high-cardinality metric dimensions.
type MCPClientRequest struct {
	MCPTransport

	Method          string
	ProtocolVersion string
	SessionID       string
	ToolName        string
	PromptName      string
	PromptVariables map[string]string
	ResourceURI     string
	Arguments       any
}

// MCPClientResult contains attributes available when an outbound MCP request
// or notification ends. ToolError represents CallToolResult.isError=true.
type MCPClientResult struct {
	Outcome
	ProtocolVersion   string
	StatusCode        string
	StatusDescription string
	ToolError         bool
	Result            any
}

type mcpOperationRole uint8

type mcpAmbientSpanContextKey struct{}

const (
	mcpClientRole mcpOperationRole = iota
	mcpServerRole
)

// MCPOperation represents one MCP request or notification.
// A tools/call operation reuses an active GenAI tool span when one is present,
// as required by the MCP semantic-convention deduplication guidance.
type MCPOperation struct {
	telemetry  *Telemetry
	ctx        context.Context
	span       trace.Span
	parentTool *ToolExecution
	request    MCPClientRequest
	role       mcpOperationRole
	started    time.Time
	endOnce    sync.Once
}

// MCPClientOperation is kept as a descriptive alias for outbound operations.
type MCPClientOperation = MCPOperation

// StartMCPClient starts a CLIENT span for an outbound MCP operation, or enriches
// an active execute_tool span for tools/call. The returned context carries the
// span that should be propagated to the MCP peer.
func (t *Telemetry) StartMCPClient(ctx context.Context, req MCPClientRequest) (context.Context, *MCPOperation) {
	return t.startMCPOperation(ctx, req, mcpClientRole)
}

// StartMCPServer starts a SERVER span for an MCP operation initiated by the
// peer and processed by this endpoint.
func (t *Telemetry) StartMCPServer(ctx context.Context, req MCPClientRequest) (context.Context, *MCPOperation) {
	return t.startMCPOperation(ctx, req, mcpServerRole)
}

func (t *Telemetry) startMCPOperation(ctx context.Context, req MCPClientRequest, role mcpOperationRole) (context.Context, *MCPOperation) {
	if t == nil {
		return ctx, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	req = normalizeMCPClientRequest(req)

	var parentTool *ToolExecution
	if role == mcpClientRole && req.Method == "tools/call" {
		if execution, ok := ctx.Value(toolContextKey{}).(*ToolExecution); ok && execution != nil && execution.telemetry == t {
			parentTool = execution
			req.ToolName = execution.name
		}
	}

	spanAttrs := mcpClientSpanAttributes(req)
	spanKind := trace.SpanKindClient
	if role == mcpServerRole {
		spanAttrs = mcpServerSpanAttributes(req)
		spanKind = trace.SpanKindServer
	}
	if t.captureSpanContent {
		if arguments, ok := StructuredObjectValue(req.Arguments); ok {
			spanAttrs = append(spanAttrs, attribute.KeyValue{Key: keyToolCallArguments, Value: arguments})
		}
		keys := make([]string, 0, len(req.PromptVariables))
		for key := range req.PromptVariables {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			spanAttrs = append(spanAttrs, attribute.String("gen_ai.prompt.variable."+key, req.PromptVariables[key]))
		}
	}

	var span trace.Span
	started := time.Now()
	if parentTool != nil {
		span = parentTool.span
		if span != nil {
			span.SetAttributes(spanAttrs...)
		}
	} else if t.tracer != nil {
		startOptions := []trace.SpanStartOption{
			trace.WithSpanKind(spanKind),
			trace.WithAttributes(spanAttrs...),
			trace.WithTimestamp(started),
		}
		if role == mcpServerRole {
			if ambient, ok := ctx.Value(mcpAmbientSpanContextKey{}).(trace.SpanContext); ok && ambient.IsValid() {
				startOptions = append(startOptions, trace.WithLinks(trace.Link{SpanContext: ambient}))
			}
		}
		ctx, span = t.tracer.Start(ctx, mcpSpanName(req), startOptions...)
	}

	return ctx, &MCPOperation{
		telemetry:  t,
		ctx:        ctx,
		span:       span,
		parentTool: parentTool,
		request:    req,
		role:       role,
		started:    started,
	}
}

// End completes an MCP operation and records its role-specific duration.
func (operation *MCPOperation) End(result MCPClientResult) {
	if operation == nil {
		return
	}
	operation.endOnce.Do(func() {
		ended := time.Now()
		if protocolVersion := strings.TrimSpace(result.ProtocolVersion); protocolVersion != "" {
			operation.request.ProtocolVersion = protocolVersion
			if operation.span != nil {
				operation.span.SetAttributes(keyMCPProtocolVersion.String(protocolVersion))
			}
		}

		outcome := result.Outcome
		statusCode := strings.TrimSpace(result.StatusCode)
		if statusCode != "" {
			if operation.role == mcpServerRole && mcpServerNonErrorStatus(statusCode) {
				outcome = Outcome{}
			} else if outcome.errorType() == "" {
				outcome.ErrorType = statusCode
			}
		}
		if result.ToolError && outcome.errorType() == "" {
			outcome.ErrorType = "tool_error"
		}

		var statusAttrs []attribute.KeyValue
		if statusCode != "" {
			statusAttrs = append(statusAttrs, semconv.RPCResponseStatusCode(statusCode))
		}
		endAttrs := append([]attribute.KeyValue(nil), statusAttrs...)
		if operation.telemetry.captureSpanContent && outcome.errorType() == "" {
			if value, ok := StructuredObjectValue(result.Result); ok {
				endAttrs = append(endAttrs, attribute.KeyValue{Key: keyToolCallResult, Value: value})
			}
		}
		if operation.span != nil && len(endAttrs) > 0 {
			operation.span.SetAttributes(endAttrs...)
		}

		description := strings.TrimSpace(result.StatusDescription)
		if description == "" && outcome.Err != nil {
			description = outcome.Err.Error()
		}
		if operation.parentTool != nil {
			operation.parentTool.rememberProtocolOutcome(outcome, description)
		} else {
			recordOutcomeWithDescription(operation.span, nil, outcome, description)
		}

		metricAttrs := mcpClientMetricAttributes(operation.request)
		duration := operation.telemetry.mcpClientOperationDuration
		if operation.role == mcpServerRole {
			metricAttrs = mcpServerMetricAttributes(operation.request)
			duration = operation.telemetry.mcpServerOperationDuration
		}
		metricAttrs = append(metricAttrs, statusAttrs...)
		metricAttrs = appendOutcome(metricAttrs, outcome)
		if duration != nil {
			duration.Record(
				operation.ctx,
				ended.Sub(operation.started).Seconds(),
				metricOptions(metricAttrs),
			)
		}
		if operation.parentTool == nil && operation.span != nil {
			operation.span.End(trace.WithTimestamp(ended))
		}
	})
}

func mcpServerNonErrorStatus(status string) bool {
	switch status {
	case "-32700", "-32600", "-32601", "-32602", "-32002":
		return true
	default:
		return false
	}
}

// MCPSessionRequest contains stable attributes for an MCP client session.
type MCPSessionRequest struct {
	MCPTransport
	ProtocolVersion string
}

// MCPSession observes one connected MCP client session.
type MCPSession struct {
	telemetry *Telemetry
	ctx       context.Context
	request   MCPSessionRequest
	started   time.Time
	endOnce   sync.Once
}

// StartMCPSession begins an MCP client-session duration observation.
func (t *Telemetry) StartMCPSession(ctx context.Context, req MCPSessionRequest) *MCPSession {
	if t == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return &MCPSession{
		telemetry: t,
		ctx:       context.WithoutCancel(ctx),
		request:   req,
		started:   time.Now(),
	}
}

// End records mcp.client.session.duration. It is safe to call more than once.
func (session *MCPSession) End(outcome Outcome) {
	if session == nil {
		return
	}
	session.endOnce.Do(func() {
		attrs := mcpSessionAttributes(session.request)
		attrs = appendOutcome(attrs, outcome)
		if session.telemetry.mcpClientSessionDuration != nil {
			session.telemetry.mcpClientSessionDuration.Record(
				session.ctx,
				time.Since(session.started).Seconds(),
				metricOptions(attrs),
			)
		}
	})
}

// InjectMCPContext injects the configured propagator into an MCP params._meta
// map. The default is W3C Trace Context plus W3C Baggage.
func (t *Telemetry) InjectMCPContext(ctx context.Context, meta map[string]any) map[string]any {
	if t == nil || t.propagator == nil {
		return meta
	}
	if meta == nil {
		meta = make(map[string]any)
	}
	t.propagator.Inject(ctx, mcpMetaCarrier(meta))
	return meta
}

// ExtractMCPContext extracts configured propagation fields from MCP
// params._meta before a receiver span is started.
func (t *Telemetry) ExtractMCPContext(ctx context.Context, meta map[string]any) context.Context {
	if t == nil || t.propagator == nil {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ambient := trace.SpanContextFromContext(ctx)
	extracted := t.propagator.Extract(ctx, mcpMetaCarrier(meta))
	remote := trace.SpanContextFromContext(extracted)
	sameSpan := ambient.TraceID() == remote.TraceID() && ambient.SpanID() == remote.SpanID()
	link := trace.SpanContext{}
	if ambient.IsValid() && remote.IsValid() && !sameSpan {
		link = ambient
	}
	return context.WithValue(extracted, mcpAmbientSpanContextKey{}, link)
}

func resolvePropagator(configured propagation.TextMapPropagator) propagation.TextMapPropagator {
	if configured != nil {
		return configured
	}
	if global := otel.GetTextMapPropagator(); global != nil && len(global.Fields()) > 0 {
		return global
	}
	return propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
}

type mcpMetaCarrier map[string]any

func (carrier mcpMetaCarrier) Get(key string) string {
	value, _ := carrier[key].(string)
	return value
}

func (carrier mcpMetaCarrier) Set(key, value string) { carrier[key] = value }

func (carrier mcpMetaCarrier) Keys() []string {
	keys := make([]string, 0, len(carrier))
	for key := range carrier {
		keys = append(keys, key)
	}
	return keys
}

func normalizeMCPClientRequest(req MCPClientRequest) MCPClientRequest {
	req.Method = firstNonEmpty(strings.TrimSpace(req.Method), "_OTHER")
	req.ProtocolVersion = strings.TrimSpace(req.ProtocolVersion)
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.ToolName = strings.TrimSpace(req.ToolName)
	req.PromptName = strings.TrimSpace(req.PromptName)
	req.ResourceURI = strings.TrimSpace(req.ResourceURI)
	req.NetworkTransport = strings.TrimSpace(req.NetworkTransport)
	req.NetworkProtocolName = strings.TrimSpace(req.NetworkProtocolName)
	req.NetworkProtocolVersion = strings.TrimSpace(req.NetworkProtocolVersion)
	req.ServerAddress = strings.TrimSpace(req.ServerAddress)
	return req
}

func mcpSpanName(req MCPClientRequest) string {
	target := firstNonEmpty(req.ToolName, req.PromptName)
	if target == "" {
		return req.Method
	}
	return req.Method + " " + target
}

func mcpClientSpanAttributes(req MCPClientRequest) []attribute.KeyValue {
	attrs := mcpClientMetricAttributes(req)
	if req.SessionID != "" {
		attrs = append(attrs, keyMCPSessionID.String(req.SessionID))
	}
	if req.ResourceURI != "" {
		attrs = append(attrs, keyMCPResourceURI.String(req.ResourceURI))
	}
	return attrs
}

func mcpServerSpanAttributes(req MCPClientRequest) []attribute.KeyValue {
	attrs := mcpServerMetricAttributes(req)
	if req.SessionID != "" {
		attrs = append(attrs, keyMCPSessionID.String(req.SessionID))
	}
	if req.ResourceURI != "" {
		attrs = append(attrs, keyMCPResourceURI.String(req.ResourceURI))
	}
	return attrs
}

func mcpClientMetricAttributes(req MCPClientRequest) []attribute.KeyValue {
	attrs := []attribute.KeyValue{keyMCPMethod.String(req.Method)}
	if req.ProtocolVersion != "" {
		attrs = append(attrs, keyMCPProtocolVersion.String(req.ProtocolVersion))
	}
	if req.ToolName != "" {
		attrs = append(attrs,
			keyOperationName.String(operationExecuteTool),
			keyToolName.String(req.ToolName),
		)
	}
	if req.PromptName != "" {
		attrs = append(attrs, keyGenAIPromptName.String(req.PromptName))
	}
	return append(attrs, mcpTransportAttributes(req.MCPTransport)...)
}

func mcpServerMetricAttributes(req MCPClientRequest) []attribute.KeyValue {
	req.ServerAddress = ""
	req.ServerPort = 0
	return mcpClientMetricAttributes(req)
}

func mcpSessionAttributes(req MCPSessionRequest) []attribute.KeyValue {
	var attrs []attribute.KeyValue
	if protocolVersion := strings.TrimSpace(req.ProtocolVersion); protocolVersion != "" {
		attrs = append(attrs, keyMCPProtocolVersion.String(protocolVersion))
	}
	return append(attrs, mcpTransportAttributes(req.MCPTransport)...)
}

func mcpTransportAttributes(transport MCPTransport) []attribute.KeyValue {
	var attrs []attribute.KeyValue
	if value := strings.TrimSpace(transport.NetworkTransport); value != "" {
		attrs = append(attrs, semconv.NetworkTransportKey.String(value))
	}
	if value := strings.TrimSpace(transport.NetworkProtocolName); value != "" {
		attrs = append(attrs, semconv.NetworkProtocolName(value))
	}
	if value := strings.TrimSpace(transport.NetworkProtocolVersion); value != "" {
		attrs = append(attrs, semconv.NetworkProtocolVersion(value))
	}
	if address := strings.TrimSpace(transport.ServerAddress); address != "" {
		attrs = append(attrs, semconv.ServerAddress(address))
		if transport.ServerPort > 0 {
			attrs = append(attrs, semconv.ServerPort(transport.ServerPort))
		}
	}
	return attrs
}
