package mcp

import (
	"context"
	"maps"
	"reflect"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/adrianliechti/wingman-agent/pkg/telemetry"
)

func TestManagerForwardsToolListChangedWithServerName(t *testing.T) {
	manager := NewManager(&Config{Servers: map[string]ServerConfig{}})
	changed := make(chan string, 1)
	manager.SetToolListChangedHandler(func(serverName string) {
		changed <- serverName
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	clientTransport, serverTransport := sdkmcp.NewInMemoryTransports()
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
	sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "initial"},
		func(context.Context, *sdkmcp.CallToolRequest, struct{}) (*sdkmcp.CallToolResult, any, error) {
			return &sdkmcp.CallToolResult{}, nil, nil
		})
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	clientSession, err := manager.newClient("dynamic").Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()

	sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "added"},
		func(context.Context, *sdkmcp.CallToolRequest, struct{}) (*sdkmcp.CallToolResult, any, error) {
			return &sdkmcp.CallToolResult{}, nil, nil
		})

	select {
	case got := <-changed:
		if got != "dynamic" {
			t.Fatalf("server name = %q, want dynamic", got)
		}
	case <-ctx.Done():
		t.Fatal("tool list change was not forwarded")
	}
}

func TestCreateCommandTransportIncludesServerEnvironment(t *testing.T) {
	t.Setenv("MCP_PARENT", "parent")
	transport, err := createTransport(ServerConfig{
		Command: "server",
		Args:    []string{"--stdio"},
		Env:     map[string]string{"MCP_TOKEN": "secret"},
	}, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	command, ok := transport.(*sdkmcp.CommandTransport)
	if !ok {
		t.Fatalf("transport type = %T", transport)
	}
	if command.Command.Dir == "" || command.Command.Path == "" {
		t.Fatalf("command = %#v", command.Command)
	}
	joined := strings.Join(command.Command.Env, "\n")
	if !strings.Contains(joined, "MCP_PARENT=parent") || !strings.Contains(joined, "MCP_TOKEN=secret") {
		t.Fatalf("environment missing parent or server value: %q", joined)
	}
	if len(command.Command.Env) == 0 {
		t.Fatal("command environment was not set")
	}
}

func TestManagerEmitsMCPTelemetryAndPropagatesContext(t *testing.T) {
	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	metricReader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(metricReader))
	t.Cleanup(func() {
		_ = tracerProvider.Shutdown(context.Background())
		_ = meterProvider.Shutdown(context.Background())
	})
	tel, err := telemetry.New(context.Background(), telemetry.Options{
		TracerProvider:        tracerProvider,
		MeterProvider:         meterProvider,
		CaptureMessageContent: telemetry.ContentCaptureSpanOnly,
	})
	if err != nil {
		t.Fatal(err)
	}

	serverConfig := ServerConfig{Command: "test-server"}
	manager := NewManager(&Config{Servers: map[string]ServerConfig{"weather": serverConfig}})
	manager.SetTelemetry(tel)

	clientTransport, serverTransport := sdkmcp.NewInMemoryTransports()
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
	metaReceived := make(chan map[string]any, 1)
	server.AddReceivingMiddleware(func(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
		return func(ctx context.Context, method string, request sdkmcp.Request) (sdkmcp.Result, error) {
			if method == "tools/call" {
				metaReceived <- maps.Clone(request.GetParams().GetMeta())
			}
			return next(ctx, method, request)
		}
	})
	type toolInput struct {
		City string `json:"city"`
	}
	sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "forecast"},
		func(context.Context, *sdkmcp.CallToolRequest, toolInput) (*sdkmcp.CallToolResult, map[string]any, error) {
			return &sdkmcp.CallToolResult{}, map[string]any{"conditions": "sunny"}, nil
		})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	clientSession, err := manager.newClient("weather", serverConfig).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	manager.AddSession("weather", clientSession)

	result, err := clientSession.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "forecast",
		Arguments: map[string]any{"city": "Zurich"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("tool unexpectedly returned an error")
	}

	remoteCtx, remoteSpan := tracerProvider.Tracer("test").Start(ctx, "remote MCP peer")
	remoteSpanContext := remoteSpan.SpanContext()
	ping := &sdkmcp.PingParams{}
	ping.SetMeta(tel.InjectMCPContext(remoteCtx, nil))
	if err := serverSession.Ping(ctx, ping); err != nil {
		t.Fatal(err)
	}
	remoteSpan.End()
	manager.Close()

	select {
	case meta := <-metaReceived:
		if traceparent, _ := meta["traceparent"].(string); traceparent == "" {
			t.Fatalf("MCP params._meta has no traceparent: %#v", meta)
		}
	case <-ctx.Done():
		t.Fatal("server did not observe the MCP tool call")
	}

	var toolSpan sdktrace.ReadOnlySpan
	for _, span := range spanRecorder.Ended() {
		if value, ok := mcpSpanAttribute(span, "mcp.method.name"); ok && value.AsString() == "tools/call" {
			toolSpan = span
			break
		}
	}
	if toolSpan == nil {
		t.Fatal("no tools/call MCP span was emitted")
	}
	if toolSpan.Name() != "tools/call forecast" || toolSpan.SpanKind() != trace.SpanKindClient {
		t.Fatalf("tool span = %q/%v", toolSpan.Name(), toolSpan.SpanKind())
	}
	for name, want := range map[string]string{
		"gen_ai.operation.name": "execute_tool",
		"gen_ai.tool.name":      "forecast",
		"network.transport":     "pipe",
	} {
		value, ok := mcpSpanAttribute(toolSpan, name)
		if !ok || value.AsString() != want {
			t.Errorf("span attribute %s = %v (present %v), want %q", name, value, ok, want)
		}
	}
	if _, ok := mcpSpanAttribute(toolSpan, "gen_ai.tool.call.arguments"); !ok {
		t.Error("content opt-in did not record MCP tool arguments")
	}
	if _, ok := mcpSpanAttribute(toolSpan, "gen_ai.tool.call.result"); !ok {
		t.Error("content opt-in did not record MCP tool result")
	}

	var pingSpan sdktrace.ReadOnlySpan
	for _, span := range spanRecorder.Ended() {
		if value, ok := mcpSpanAttribute(span, "mcp.method.name"); ok && value.AsString() == "ping" {
			pingSpan = span
			break
		}
	}
	if pingSpan == nil {
		t.Fatal("no receiver-side ping MCP span was emitted")
	}
	if pingSpan.Name() != "ping" || pingSpan.SpanKind() != trace.SpanKindServer {
		t.Fatalf("ping span = %q/%v", pingSpan.Name(), pingSpan.SpanKind())
	}
	if pingSpan.Parent().SpanID() != remoteSpanContext.SpanID() {
		t.Errorf("ping parent = %s, propagated parent = %s", pingSpan.Parent().SpanID(), remoteSpanContext.SpanID())
	}

	var collected metricdata.ResourceMetrics
	if err := metricReader.Collect(context.Background(), &collected); err != nil {
		t.Fatal(err)
	}
	metrics := mcpFloatHistograms(collected)
	operationMetric, ok := metrics["mcp.client.operation.duration"]
	if !ok {
		t.Fatal("mcp.client.operation.duration was not recorded")
	}
	var toolDuration float64
	for _, point := range operationMetric.DataPoints {
		if value, ok := point.Attributes.Value(attribute.Key("mcp.method.name")); ok && value.AsString() == "tools/call" {
			toolDuration = point.Sum
			if _, hasContent := point.Attributes.Value(attribute.Key("gen_ai.tool.call.arguments")); hasContent {
				t.Error("MCP metric contains high-cardinality tool arguments")
			}
			break
		}
	}
	if want := toolSpan.EndTime().Sub(toolSpan.StartTime()).Seconds(); toolDuration != want {
		t.Errorf("MCP operation metric duration = %v, span duration = %v", toolDuration, want)
	}
	if bounds := operationMetric.DataPoints[0].Bounds; !reflect.DeepEqual(bounds, []float64{
		0.01, 0.02, 0.05, 0.1, 0.2, 0.5, 1, 2, 5, 10, 30, 60, 120, 300,
	}) {
		t.Errorf("MCP operation bounds = %v", bounds)
	}
	sessionMetric, ok := metrics["mcp.client.session.duration"]
	if !ok {
		t.Fatal("mcp.client.session.duration was not recorded")
	}
	if !reflect.DeepEqual(sessionMetric.DataPoints[0].Bounds, operationMetric.DataPoints[0].Bounds) {
		t.Errorf("MCP session bounds = %v, operation bounds = %v", sessionMetric.DataPoints[0].Bounds, operationMetric.DataPoints[0].Bounds)
	}
	serverMetric, ok := metrics["mcp.server.operation.duration"]
	if !ok {
		t.Fatal("mcp.server.operation.duration was not recorded")
	}
	var pingDuration float64
	for _, point := range serverMetric.DataPoints {
		if value, ok := point.Attributes.Value(attribute.Key("mcp.method.name")); ok && value.AsString() == "ping" {
			pingDuration = point.Sum
			if _, hasAddress := point.Attributes.Value(attribute.Key("server.address")); hasAddress {
				t.Error("receiver-side MCP metric contains server.address")
			}
			break
		}
	}
	if want := pingSpan.EndTime().Sub(pingSpan.StartTime()).Seconds(); pingDuration != want {
		t.Errorf("MCP server metric duration = %v, span duration = %v", pingDuration, want)
	}
	if !reflect.DeepEqual(serverMetric.DataPoints[0].Bounds, operationMetric.DataPoints[0].Bounds) {
		t.Errorf("MCP server bounds = %v, operation bounds = %v", serverMetric.DataPoints[0].Bounds, operationMetric.DataPoints[0].Bounds)
	}
}

func mcpSpanAttribute(span sdktrace.ReadOnlySpan, name string) (attribute.Value, bool) {
	for _, attr := range span.Attributes() {
		if string(attr.Key) == name {
			return attr.Value, true
		}
	}
	return attribute.Value{}, false
}

func mcpFloatHistograms(resourceMetrics metricdata.ResourceMetrics) map[string]metricdata.Histogram[float64] {
	result := map[string]metricdata.Histogram[float64]{}
	for _, scope := range resourceMetrics.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if histogram, ok := metric.Data.(metricdata.Histogram[float64]); ok {
				result[metric.Name] = histogram
			}
		}
	}
	return result
}
