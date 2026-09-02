package telemetry

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestConfiguredSignalsUseStandardEnvironment(t *testing.T) {
	clearTelemetryEnvironment(t)
	if traces, metrics := configuredSignals(); traces || metrics {
		t.Fatalf("unconfigured signals = (%v, %v), want both false", traces, metrics)
	}

	t.Run("common OTLP endpoint", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector:4318")
		if traces, metrics := configuredSignals(); !traces || !metrics {
			t.Fatalf("configured signals = (%v, %v), want both true", traces, metrics)
		}
	})

	t.Run("signal endpoint", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "http://collector:4318/v1/traces")
		if traces, metrics := configuredSignals(); !traces || metrics {
			t.Fatalf("configured signals = (%v, %v), want traces only", traces, metrics)
		}
	})

	t.Run("exporter selectors", func(t *testing.T) {
		t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
		t.Setenv("OTEL_METRICS_EXPORTER", "none")
		if traces, metrics := configuredSignals(); !traces || metrics {
			t.Fatalf("configured signals = (%v, %v), want traces only", traces, metrics)
		}
	})

	t.Run("SDK disabled", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector:4318")
		t.Setenv("OTEL_SDK_DISABLED", "true")
		if traces, metrics := configuredSignals(); traces || metrics {
			t.Fatalf("disabled signals = (%v, %v), want both false", traces, metrics)
		}
	})
}

func TestOTLPOnlyConfiguration(t *testing.T) {
	clearTelemetryEnvironment(t)

	t.Run("unconfigured environment is no-op", func(t *testing.T) {
		tel, err := NewFromEnvironment(context.Background(), Options{})
		if err != nil {
			t.Fatalf("NewFromEnvironment: %v", err)
		}
		if tel != nil {
			t.Fatal("NewFromEnvironment returned telemetry without OTLP configuration")
		}
	})

	t.Run("unsupported exporter", func(t *testing.T) {
		t.Setenv("OTEL_TRACES_EXPORTER", "console")
		_, err := NewFromEnvironment(context.Background(), Options{})
		if err == nil || !strings.Contains(err.Error(), "supports only otlp or none") {
			t.Fatalf("NewFromEnvironment error = %v, want unsupported exporter error", err)
		}
	})

	t.Run("unsupported transport", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector:4318")
		t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")
		_, err := NewFromEnvironment(context.Background(), Options{})
		if err == nil || !strings.Contains(err.Error(), "only OTLP over HTTP/protobuf") {
			t.Fatalf("NewFromEnvironment error = %v, want unsupported protocol error", err)
		}
	})

	t.Run("none disables explicit pipeline", func(t *testing.T) {
		t.Setenv("OTEL_TRACES_EXPORTER", "none")
		t.Setenv("OTEL_METRICS_EXPORTER", "none")
		tel, err := New(context.Background(), Options{})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if tel != nil {
			t.Fatal("New returned telemetry with both exporters disabled")
		}
	})

	t.Run("log exporter alone does not enable events", func(t *testing.T) {
		t.Setenv("OTEL_LOGS_EXPORTER", "otlp")
		tel, err := NewFromEnvironment(context.Background(), Options{
			DisableTraces:  true,
			DisableMetrics: true,
		})
		if err != nil {
			t.Fatalf("NewFromEnvironment: %v", err)
		}
		if tel != nil {
			t.Fatal("log exporter configuration enabled events without an event opt-in")
		}
	})

	t.Run("unsupported log exporter", func(t *testing.T) {
		t.Setenv("OTEL_LOGS_EXPORTER", "console")
		t.Setenv(emitEventEnv, "true")
		_, err := NewFromEnvironment(context.Background(), Options{
			DisableTraces:  true,
			DisableMetrics: true,
		})
		if err == nil || !strings.Contains(err.Error(), "supports only otlp or none") {
			t.Fatalf("NewFromEnvironment error = %v, want unsupported log exporter error", err)
		}
	})
}

func TestContentCaptureConfiguration(t *testing.T) {
	clearTelemetryEnvironment(t)

	t.Setenv(captureContentEnv, "span_only")
	mode, err := resolveCaptureMode("")
	if err != nil || mode != ContentCaptureSpanOnly {
		t.Fatalf("environment capture mode = %q, %v; want %q", mode, err, ContentCaptureSpanOnly)
	}

	mode, err = resolveCaptureMode(ContentCaptureEventOnly)
	if err != nil || mode != ContentCaptureEventOnly {
		t.Fatalf("option capture mode = %q, %v; want %q", mode, err, ContentCaptureEventOnly)
	}

	if resolveEventEmission(false, ContentCaptureSpanOnly) {
		t.Fatal("SPAN_ONLY enabled events by default")
	}
	if !resolveEventEmission(false, ContentCaptureEventOnly) {
		t.Fatal("EVENT_ONLY did not enable events by default")
	}
	t.Setenv(emitEventEnv, "false")
	if resolveEventEmission(false, ContentCaptureEventOnly) {
		t.Fatal("explicit event environment false did not override EVENT_ONLY default")
	}
	if !resolveEventEmission(true, ContentCaptureNoContent) {
		t.Fatal("programmatic event opt-in did not override environment false")
	}

	if _, err := resolveCaptureMode("messages_everywhere"); err == nil {
		t.Fatal("invalid content capture mode was accepted")
	}
	t.Setenv(captureContentEnv, "messages_everywhere")
	if mode, err := resolveCaptureMode(""); err != nil || mode != ContentCaptureNoContent {
		t.Fatalf("invalid environment capture mode = %q, %v; want safe default", mode, err)
	}
}

func TestGenAIInferenceDetailEventsAndContentCapture(t *testing.T) {
	clearTelemetryEnvironment(t)

	tests := []struct {
		name          string
		mode          ContentCaptureMode
		wantSpan      bool
		wantEvent     bool
		wantEventData bool
	}{
		{name: "no content", mode: ContentCaptureNoContent},
		{name: "span only", mode: ContentCaptureSpanOnly, wantSpan: true},
		{name: "event only", mode: ContentCaptureEventOnly, wantEvent: true, wantEventData: true},
		{name: "span and event", mode: ContentCaptureSpanAndEvent, wantSpan: true, wantEvent: true, wantEventData: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spanRecorder := tracetest.NewSpanRecorder()
			tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
			logExporter := &memoryLogExporter{}
			loggerProvider := sdklog.NewLoggerProvider(
				sdklog.WithProcessor(sdklog.NewSimpleProcessor(logExporter)),
			)
			t.Cleanup(func() {
				_ = tracerProvider.Shutdown(context.Background())
				_ = loggerProvider.Shutdown(context.Background())
			})

			tel, err := New(context.Background(), Options{
				ProviderName:          "openai",
				TracerProvider:        tracerProvider,
				LoggerProvider:        loggerProvider,
				DisableMetrics:        true,
				CaptureMessageContent: tt.mode,
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			input := attribute.SliceValue(attribute.MapValue(
				attribute.String("role", "user"),
				attribute.Key("parts").Slice(attribute.MapValue(
					attribute.String("type", "text"),
					attribute.String("content", "secret input"),
				)),
			))
			output := attribute.SliceValue(attribute.MapValue(
				attribute.String("role", "assistant"),
				attribute.Key("parts").Slice(attribute.MapValue(
					attribute.String("type", "text"),
					attribute.String("content", "secret output"),
				)),
				attribute.String("finish_reason", "stop"),
			))

			_, inference := tel.StartInference(context.Background(), InferenceRequest{
				Model: "gpt-test",
				Content: InferenceContent{
					InputMessages: input,
				},
			})
			if got := tel.CapturesMessageContent(); got != (tt.wantSpan || tt.wantEventData) {
				t.Fatalf("CapturesMessageContent = %v", got)
			}
			inference.End(InferenceResult{
				ResponseID:     "resp-1",
				ResponseModel:  "gpt-test-2026",
				FinishReasons:  []string{"stop"},
				OutputMessages: output,
				Usage: &TokenUsage{
					InputTokens:  4,
					OutputTokens: 2,
				},
			})

			spans := spanRecorder.Ended()
			if len(spans) != 1 {
				t.Fatalf("ended spans = %d, want 1", len(spans))
			}
			_, spanHasContent := spanAttribute(spans[0], "gen_ai.input.messages")
			if spanHasContent != tt.wantSpan {
				t.Fatalf("span content present = %v, want %v", spanHasContent, tt.wantSpan)
			}

			records := logExporter.Records()
			if got := len(records); got != boolCount(tt.wantEvent) {
				t.Fatalf("event records = %d, want %d", got, boolCount(tt.wantEvent))
			}
			if !tt.wantEvent {
				return
			}
			record := records[0]
			if record.EventName() != inferenceEventName {
				t.Fatalf("event name = %q, want %q", record.EventName(), inferenceEventName)
			}
			if record.TraceID() != spans[0].SpanContext().TraceID() || record.SpanID() != spans[0].SpanContext().SpanID() {
				t.Fatalf("event trace context = %s/%s, want %s/%s", record.TraceID(), record.SpanID(), spans[0].SpanContext().TraceID(), spans[0].SpanContext().SpanID())
			}
			if !record.Timestamp().Equal(spans[0].EndTime()) {
				t.Fatalf("event timestamp = %v, span end = %v", record.Timestamp(), spans[0].EndTime())
			}
			attrs := logRecordAttributes(record)
			if value, ok := attrs["gen_ai.operation.name"]; !ok || value.AsString() != "chat" {
				t.Fatalf("event operation attribute = %v (present %v)", value, ok)
			}
			_, eventHasContent := attrs["gen_ai.input.messages"]
			if eventHasContent != tt.wantEventData {
				t.Fatalf("event content present = %v, want %v", eventHasContent, tt.wantEventData)
			}
			if eventHasContent && attrs["gen_ai.input.messages"].Type() != attribute.SLICE {
				t.Fatalf("event content type = %v, want structured slice", attrs["gen_ai.input.messages"].Type())
			}
		})
	}

	t.Run("metadata-only event", func(t *testing.T) {
		logExporter := &memoryLogExporter{}
		loggerProvider := sdklog.NewLoggerProvider(
			sdklog.WithProcessor(sdklog.NewSimpleProcessor(logExporter)),
		)
		t.Cleanup(func() { _ = loggerProvider.Shutdown(context.Background()) })
		tel, err := New(context.Background(), Options{
			LoggerProvider: loggerProvider,
			DisableTraces:  true,
			DisableMetrics: true,
			EmitEvents:     true,
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_, inference := tel.StartInference(context.Background(), InferenceRequest{Model: "gpt-test"})
		inference.End(InferenceResult{})
		records := logExporter.Records()
		if len(records) != 1 {
			t.Fatalf("events = %d, want 1", len(records))
		}
		if _, ok := logRecordAttributes(records[0])["gen_ai.input.messages"]; ok {
			t.Fatal("metadata-only event captured message content")
		}
	})

	t.Run("standard exception event", func(t *testing.T) {
		logExporter := &memoryLogExporter{}
		loggerProvider := sdklog.NewLoggerProvider(
			sdklog.WithProcessor(sdklog.NewSimpleProcessor(logExporter)),
		)
		t.Cleanup(func() { _ = loggerProvider.Shutdown(context.Background()) })
		tel, err := New(context.Background(), Options{
			LoggerProvider: loggerProvider,
			DisableTraces:  true,
			DisableMetrics: true,
			EmitEvents:     true,
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_, inference := tel.StartInference(context.Background(), InferenceRequest{Model: "gpt-test"})
		inference.End(InferenceResult{Outcome: Outcome{
			Err:       errors.New("request failed"),
			ErrorType: "rate_limit_exceeded",
		}})
		records := logExporter.Records()
		if len(records) != 2 {
			t.Fatalf("events = %d, want detail and exception events", len(records))
		}
		var exception sdklog.Record
		for _, record := range records {
			if record.EventName() == exceptionEventName {
				exception = record
				break
			}
		}
		if exception.EventName() == "" {
			t.Fatal("standard exception event was not emitted")
		}
		attrs := logRecordAttributes(exception)
		if got := attrs["exception.type"].AsString(); got != "*errors.errorString" {
			t.Fatalf("exception.type = %q", got)
		}
		if got := attrs["exception.message"].AsString(); got != "request failed" {
			t.Fatalf("exception.message = %q", got)
		}
		if exception.Severity() != otellog.SeverityWarn {
			t.Fatalf("exception severity = %v, want WARN", exception.Severity())
		}
	})
}

func TestGenAISpansAndMetrics(t *testing.T) {
	clearTelemetryEnvironment(t)
	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	metricReader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(metricReader))
	t.Cleanup(func() {
		_ = tracerProvider.Shutdown(context.Background())
		_ = meterProvider.Shutdown(context.Background())
	})

	tel, err := New(context.Background(), Options{
		AgentName:      "code-agent",
		ProviderName:   "openai",
		ServerAddress:  "api.openai.test",
		ServerPort:     443,
		TracerProvider: tracerProvider,
		MeterProvider:  meterProvider,
	})
	if err != nil {
		t.Fatalf("create telemetry: %v", err)
	}

	agentCtx, invocation := tel.StartAgent(context.Background(), AgentRequest{
		ConversationID: "session-42",
	})
	inferenceCtx, inference := tel.StartInference(agentCtx, InferenceRequest{
		Model:          "gpt-test",
		ConversationID: "session-42",
		Streaming:      true,
		ReasoningLevel: "high",
	})
	ObserveResponseChunk(inferenceCtx)
	ObserveOutputChunk(inferenceCtx)
	time.Sleep(time.Millisecond)
	ObserveOutputChunk(inferenceCtx)
	inference.End(InferenceResult{
		ResponseID:    "resp-1",
		ResponseModel: "gpt-test-2026",
		FinishReasons: []string{"stop"},
		Usage: &TokenUsage{
			InputTokens:           120,
			CacheReadInputTokens:  20,
			CacheWriteInputTokens: 8,
			OutputTokens:          30,
			ReasoningOutputTokens: 12,
		},
	})

	_, execution := tel.StartTool(agentCtx, ToolRequest{
		Name:        "read_file",
		Description: "Read a file",
		CallID:      "call-1",
	})
	execution.End(Outcome{ErrorType: "permission_denied"})
	mcpToolCtx, mcpExecution := tel.StartTool(agentCtx, ToolRequest{
		Name:               "docs_search",
		Type:               "extension",
		MCPMethod:          "tools/call",
		MCPProtocolVersion: "2025-11-25",
	})
	_, mcpOperation := tel.StartMCPClient(mcpToolCtx, MCPClientRequest{
		MCPTransport: MCPTransport{NetworkTransport: "pipe"},
		Method:       "tools/call",
		ToolName:     "search",
		SessionID:    "mcp-session",
	})
	mcpOperation.End(MCPClientResult{ToolError: true})
	mcpExecution.End(Outcome{ErrorType: "_OTHER"})
	invocation.End(Outcome{})

	endedSpans := spanRecorder.Ended()
	if len(endedSpans) != 4 {
		t.Fatalf("ended spans = %d, want agent, inference, and two tool spans", len(endedSpans))
	}
	spans := spansByName(endedSpans)
	agentSpan := spans["invoke_agent code-agent"]
	inferenceSpan := spans["chat gpt-test"]
	toolSpan := spans["execute_tool read_file"]
	mcpSpan := spans["execute_tool docs_search"]
	if agentSpan == nil || inferenceSpan == nil || toolSpan == nil || mcpSpan == nil {
		t.Fatalf("ended span names = %v", reflect.ValueOf(spans).MapKeys())
	}
	if agentSpan.SpanKind() != trace.SpanKindInternal {
		t.Fatalf("agent span kind = %v", agentSpan.SpanKind())
	}
	if inferenceSpan.SpanKind() != trace.SpanKindClient {
		t.Fatalf("inference span kind = %v", inferenceSpan.SpanKind())
	}
	if got := inferenceSpan.InstrumentationScope().SchemaURL; got != genAISchemaURL {
		t.Fatalf("instrumentation schema URL = %q, want %q", got, genAISchemaURL)
	}
	if inferenceSpan.Parent().SpanID() != agentSpan.SpanContext().SpanID() {
		t.Fatal("inference span is not a child of the agent invocation")
	}
	if toolSpan.Parent().SpanID() != agentSpan.SpanContext().SpanID() {
		t.Fatal("tool span is not a child of the agent invocation")
	}
	if mcpSpan.SpanKind() != trace.SpanKindInternal || mcpSpan.Parent().SpanID() != agentSpan.SpanContext().SpanID() {
		t.Fatal("MCP tool call did not reuse the INTERNAL GenAI tool span")
	}
	assertSpanString(t, agentSpan, "gen_ai.operation.name", "invoke_agent")
	assertSpanString(t, agentSpan, "gen_ai.conversation.id", "session-42")
	assertSpanInt64(t, agentSpan, "gen_ai.usage.input_tokens", 120)
	assertSpanInt64(t, agentSpan, "gen_ai.usage.output_tokens", 30)
	assertSpanString(t, inferenceSpan, "gen_ai.provider.name", "openai")
	assertSpanString(t, inferenceSpan, "gen_ai.request.model", "gpt-test")
	assertSpanBool(t, inferenceSpan, "gen_ai.request.stream", true)
	assertSpanString(t, inferenceSpan, "gen_ai.request.reasoning.level", "high")
	assertSpanString(t, inferenceSpan, "openai.api.type", "responses")
	assertSpanString(t, inferenceSpan, "gen_ai.response.id", "resp-1")
	assertSpanInt64(t, inferenceSpan, "gen_ai.usage.input_tokens", 120)
	assertSpanInt64(t, inferenceSpan, "gen_ai.usage.cache_read.input_tokens", 20)
	assertSpanInt64(t, inferenceSpan, "gen_ai.usage.cache_write.input_tokens", 8)
	assertSpanInt64(t, inferenceSpan, "gen_ai.usage.output_tokens", 30)
	assertSpanInt64(t, inferenceSpan, "gen_ai.usage.reasoning.output_tokens", 12)
	assertSpanString(t, toolSpan, "gen_ai.tool.name", "read_file")
	assertSpanString(t, toolSpan, "gen_ai.tool.type", "function")
	assertSpanString(t, toolSpan, "error.type", "permission_denied")
	assertSpanString(t, mcpSpan, "gen_ai.operation.name", "execute_tool")
	assertSpanString(t, mcpSpan, "gen_ai.tool.name", "docs_search")
	assertSpanString(t, mcpSpan, "gen_ai.tool.type", "extension")
	assertSpanString(t, mcpSpan, "mcp.method.name", "tools/call")
	assertSpanString(t, mcpSpan, "mcp.protocol.version", "2025-11-25")
	assertSpanString(t, mcpSpan, "mcp.session.id", "mcp-session")
	assertSpanString(t, mcpSpan, "network.transport", "pipe")
	assertSpanString(t, mcpSpan, "error.type", "tool_error")
	if toolSpan.Status().Code != codes.Error {
		t.Fatalf("tool span status = %v, want error", toolSpan.Status().Code)
	}
	for _, span := range []sdktrace.ReadOnlySpan{agentSpan, inferenceSpan, toolSpan} {
		for _, key := range []string{
			"gen_ai.input.messages", "gen_ai.output.messages",
			"gen_ai.tool.call.arguments", "gen_ai.tool.call.result",
		} {
			if _, ok := spanAttribute(span, key); ok {
				t.Fatalf("span %q captured sensitive attribute %q", span.Name(), key)
			}
		}
	}

	var collected metricdata.ResourceMetrics
	if err := metricReader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	for _, scope := range collected.ScopeMetrics {
		if scope.Scope.Name == instrumentationName && scope.Scope.SchemaURL != genAISchemaURL {
			t.Fatalf("metric schema URL = %q, want %q", scope.Scope.SchemaURL, genAISchemaURL)
		}
	}
	floatMetrics := floatHistogramsByName(collected)
	for _, name := range []string{
		"gen_ai.client.operation.duration",
		"gen_ai.client.operation.time_to_first_chunk",
		"gen_ai.client.operation.time_per_output_chunk",
		"gen_ai.invoke_agent.duration",
		"gen_ai.execute_tool.duration",
		"mcp.client.operation.duration",
	} {
		if _, ok := floatMetrics[name]; !ok {
			t.Errorf("metric %q was not recorded", name)
		}
	}
	intMetrics := intHistogramsByName(collected)
	for _, name := range []string{
		"gen_ai.client.token.usage",
		"gen_ai.invoke_agent.inference_calls",
		"gen_ai.invoke_agent.tool_calls",
	} {
		if _, ok := intMetrics[name]; !ok {
			t.Errorf("metric %q was not recorded", name)
		}
	}
	if got := intMetrics["gen_ai.invoke_agent.inference_calls"].DataPoints[0].Sum; got != 1 {
		t.Errorf("inference calls = %v, want 1", got)
	}
	if got := intMetrics["gen_ai.invoke_agent.tool_calls"].DataPoints[0].Sum; got != 2 {
		t.Errorf("tool calls = %v, want 2", got)
	}
	if points := intMetrics["gen_ai.client.token.usage"].DataPoints; len(points) != 2 {
		t.Errorf("token usage data points = %d, want input and output", len(points))
	}
	if bounds := floatMetrics["gen_ai.execute_tool.duration"].DataPoints[0].Bounds; !reflect.DeepEqual(bounds, clientDurationBounds) {
		t.Errorf("tool duration bounds = %v, want %v", bounds, clientDurationBounds)
	}
	if bounds := floatMetrics["mcp.client.operation.duration"].DataPoints[0].Bounds; !reflect.DeepEqual(bounds, mcpDurationBounds) {
		t.Errorf("MCP duration bounds = %v, want %v", bounds, mcpDurationBounds)
	}
	if bounds := intMetrics["gen_ai.client.token.usage"].DataPoints[0].Bounds; !reflect.DeepEqual(bounds, tokenBounds) {
		t.Errorf("token usage bounds = %v, want %v", bounds, tokenBounds)
	}
	if _, ok := floatMetrics["gen_ai.client.operation.duration"].DataPoints[0].Attributes.Value(keyOpenAIAPIType); ok {
		t.Error("generic GenAI metric contains span-only openai.api.type")
	}
	for _, name := range []string{
		"gen_ai.client.operation.duration",
		"gen_ai.client.operation.time_to_first_chunk",
		"gen_ai.client.operation.time_per_output_chunk",
	} {
		assertMetricString(t, name, floatMetrics[name].DataPoints[0].Attributes, "gen_ai.response.model", "gpt-test-2026")
	}
	for _, match := range []struct {
		metric string
		span   sdktrace.ReadOnlySpan
		tool   string
	}{
		{metric: "gen_ai.client.operation.duration", span: inferenceSpan},
		{metric: "gen_ai.invoke_agent.duration", span: agentSpan},
		{metric: "gen_ai.execute_tool.duration", span: toolSpan, tool: "read_file"},
	} {
		want := match.span.EndTime().Sub(match.span.StartTime()).Seconds()
		point := floatMetrics[match.metric].DataPoints[0]
		if match.tool != "" {
			for _, candidate := range floatMetrics[match.metric].DataPoints {
				if value, ok := candidate.Attributes.Value(attribute.Key("gen_ai.tool.name")); ok && value.AsString() == match.tool {
					point = candidate
					break
				}
			}
		}
		if got := point.Sum; got != want {
			t.Errorf("metric %q duration = %v, span duration = %v", match.metric, got, want)
		}
	}
}

func TestAgentUsageDistinguishesMissingFromReportedZero(t *testing.T) {
	tests := []struct {
		name      string
		usage     *TokenUsage
		wantUsage bool
	}{
		{name: "missing usage"},
		{name: "reported zero", usage: &TokenUsage{}, wantUsage: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spanRecorder := tracetest.NewSpanRecorder()
			tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
			t.Cleanup(func() { _ = tracerProvider.Shutdown(context.Background()) })
			tel, err := New(context.Background(), Options{
				TracerProvider: tracerProvider,
				DisableMetrics: true,
			})
			if err != nil {
				t.Fatal(err)
			}

			ctx, invocation := tel.StartAgent(context.Background(), AgentRequest{Name: "test"})
			_, inference := tel.StartInference(ctx, InferenceRequest{Model: "test"})
			inference.End(InferenceResult{Usage: tt.usage})
			invocation.End(Outcome{})

			span := spansByName(spanRecorder.Ended())["invoke_agent test"]
			if span == nil {
				t.Fatal("agent span was not emitted")
			}
			_, hasInput := spanAttribute(span, "gen_ai.usage.input_tokens")
			_, hasOutput := spanAttribute(span, "gen_ai.usage.output_tokens")
			if hasInput != tt.wantUsage || hasOutput != tt.wantUsage {
				t.Fatalf("usage attributes present = %v/%v, want %v", hasInput, hasOutput, tt.wantUsage)
			}
		})
	}
}

func TestToolContentCapture(t *testing.T) {
	clearTelemetryEnvironment(t)
	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	t.Cleanup(func() { _ = tracerProvider.Shutdown(context.Background()) })

	tel, err := New(context.Background(), Options{
		TracerProvider:        tracerProvider,
		DisableMetrics:        true,
		CaptureMessageContent: ContentCaptureSpanOnly,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, execution := tel.StartTool(context.Background(), ToolRequest{
		Name:      "weather",
		Arguments: map[string]any{"city": "Zurich"},
	})
	execution.SetResult(map[string]any{"temperature": 20})
	execution.End(Outcome{})

	spans := spanRecorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(spans))
	}
	arguments, ok := spanAttribute(spans[0], "gen_ai.tool.call.arguments")
	if !ok || arguments.Type() != attribute.MAP {
		t.Fatalf("tool arguments = %v (present %v), want structured map", arguments, ok)
	}
	result, ok := spanAttribute(spans[0], "gen_ai.tool.call.result")
	if !ok || result.Type() != attribute.MAP {
		t.Fatalf("tool result = %v (present %v), want structured map", result, ok)
	}
}

func TestMCPServerLinksAmbientContextWhenPropagationReplacesParent(t *testing.T) {
	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	t.Cleanup(func() { _ = tracerProvider.Shutdown(context.Background()) })
	tel, err := New(context.Background(), Options{
		TracerProvider: tracerProvider,
		DisableMetrics: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	tracer := tracerProvider.Tracer("test")
	ambientCtx, ambientSpan := tracer.Start(context.Background(), "ambient transport")
	remoteCtx, remoteSpan := tracer.Start(context.Background(), "remote MCP sender")
	meta := tel.InjectMCPContext(remoteCtx, nil)
	ctx := tel.ExtractMCPContext(ambientCtx, meta)
	_, operation := tel.StartMCPServer(ctx, MCPClientRequest{Method: "ping"})
	operation.End(MCPClientResult{})
	ambientSpan.End()
	remoteSpan.End()

	span := spansByName(spanRecorder.Ended())["ping"]
	if span == nil {
		t.Fatal("MCP server span was not emitted")
	}
	if got, want := span.Parent().SpanID(), remoteSpan.SpanContext().SpanID(); got != want {
		t.Fatalf("MCP parent = %s, want propagated parent %s", got, want)
	}
	links := span.Links()
	if len(links) != 1 || links[0].SpanContext.SpanID() != ambientSpan.SpanContext().SpanID() {
		t.Fatalf("MCP ambient links = %#v", links)
	}
}

func clearTelemetryEnvironment(t *testing.T) {
	t.Helper()
	for _, item := range os.Environ() {
		name, _, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		if name == "OTEL_SDK_DISABLED" ||
			name == "OTEL_TRACES_EXPORTER" ||
			name == "OTEL_METRICS_EXPORTER" ||
			name == "OTEL_LOGS_EXPORTER" ||
			name == captureContentEnv ||
			name == emitEventEnv ||
			strings.HasPrefix(name, "OTEL_EXPORTER_OTLP_") {
			t.Setenv(name, "")
		}
	}
}

type memoryLogExporter struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (exporter *memoryLogExporter) Export(_ context.Context, records []sdklog.Record) error {
	exporter.mu.Lock()
	defer exporter.mu.Unlock()
	for i := range records {
		exporter.records = append(exporter.records, records[i].Clone())
	}
	return nil
}

func (*memoryLogExporter) Shutdown(context.Context) error   { return nil }
func (*memoryLogExporter) ForceFlush(context.Context) error { return nil }

func (exporter *memoryLogExporter) Records() []sdklog.Record {
	exporter.mu.Lock()
	defer exporter.mu.Unlock()
	return append([]sdklog.Record(nil), exporter.records...)
}

func logRecordAttributes(record sdklog.Record) map[string]attribute.Value {
	attrs := make(map[string]attribute.Value, record.AttributesLen())
	record.WalkAttributes(func(attr attribute.KeyValue) bool {
		attrs[string(attr.Key)] = attr.Value
		return true
	})
	return attrs
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

func spansByName(spans []sdktrace.ReadOnlySpan) map[string]sdktrace.ReadOnlySpan {
	result := make(map[string]sdktrace.ReadOnlySpan, len(spans))
	for _, span := range spans {
		result[span.Name()] = span
	}
	return result
}

func spanAttribute(span sdktrace.ReadOnlySpan, name string) (attribute.Value, bool) {
	for _, attr := range span.Attributes() {
		if string(attr.Key) == name {
			return attr.Value, true
		}
	}
	return attribute.Value{}, false
}

func assertSpanString(t *testing.T, span sdktrace.ReadOnlySpan, name, want string) {
	t.Helper()
	value, ok := spanAttribute(span, name)
	if !ok || value.AsString() != want {
		t.Fatalf("span %q attribute %s = %v (present %v), want %q", span.Name(), name, value, ok, want)
	}
}

func assertSpanInt64(t *testing.T, span sdktrace.ReadOnlySpan, name string, want int64) {
	t.Helper()
	value, ok := spanAttribute(span, name)
	if !ok || value.AsInt64() != want {
		t.Fatalf("span %q attribute %s = %v (present %v), want %d", span.Name(), name, value, ok, want)
	}
}

func assertSpanBool(t *testing.T, span sdktrace.ReadOnlySpan, name string, want bool) {
	t.Helper()
	value, ok := spanAttribute(span, name)
	if !ok || value.AsBool() != want {
		t.Fatalf("span %q attribute %s = %v (present %v), want %v", span.Name(), name, value, ok, want)
	}
}

func assertMetricString(t *testing.T, metricName string, attrs attribute.Set, name, want string) {
	t.Helper()
	value, ok := attrs.Value(attribute.Key(name))
	if !ok || value.AsString() != want {
		t.Fatalf("metric %q attribute %s = %v (present %v), want %q", metricName, name, value, ok, want)
	}
}

func floatHistogramsByName(resourceMetrics metricdata.ResourceMetrics) map[string]metricdata.Histogram[float64] {
	result := map[string]metricdata.Histogram[float64]{}
	for _, scope := range resourceMetrics.ScopeMetrics {
		for _, m := range scope.Metrics {
			if histogram, ok := m.Data.(metricdata.Histogram[float64]); ok {
				result[m.Name] = histogram
			}
		}
	}
	return result
}

func intHistogramsByName(resourceMetrics metricdata.ResourceMetrics) map[string]metricdata.Histogram[int64] {
	result := map[string]metricdata.Histogram[int64]{}
	for _, scope := range resourceMetrics.ScopeMetrics {
		for _, m := range scope.Metrics {
			if histogram, ok := m.Data.(metricdata.Histogram[int64]); ok {
				result[m.Name] = histogram
			}
		}
	}
	return result
}
