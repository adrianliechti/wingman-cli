package main

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"time"

	collectorlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetricsv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const maxOTLPBodyBytes = 64 << 20

type otlpExport struct {
	ReceivedAt time.Time       `json:"received_at"`
	Signal     string          `json:"signal"`
	Payload    json.RawMessage `json:"payload"`
}

type otlpSummary struct {
	ExportBatches map[string]int `json:"export_batches,omitempty"`
	Resources     map[string]int `json:"resources_by_service,omitempty"`
	Spans         map[string]int `json:"spans,omitempty"`
	MetricExports map[string]int `json:"metric_exports,omitempty"`
	LogEvents     map[string]int `json:"log_events,omitempty"`
	LogRecords    int            `json:"log_records,omitempty"`
}

type otlpArtifact struct {
	CapturedAt time.Time    `json:"captured_at"`
	Summary    otlpSummary  `json:"summary"`
	Exports    []otlpExport `json:"exports"`
}

type otlpReceiver struct {
	server *httptest.Server

	mu      sync.Mutex
	exports []otlpExport
	summary otlpSummary
}

func newOTLPReceiver() *otlpReceiver {
	receiver := &otlpReceiver{}
	receiver.server = httptest.NewServer(http.HandlerFunc(receiver.serveHTTP))
	return receiver
}

func (r *otlpReceiver) URL() string {
	if r == nil || r.server == nil {
		return ""
	}
	return r.server.URL
}

func (r *otlpReceiver) Close() {
	if r != nil && r.server != nil {
		r.server.Close()
	}
}

func (r *otlpReceiver) snapshot() otlpArtifact {
	r.mu.Lock()
	defer r.mu.Unlock()
	return otlpArtifact{
		CapturedAt: time.Now().UTC(),
		Summary:    cloneOTLPSummary(r.summary),
		Exports:    append([]otlpExport(nil), r.exports...),
	}
}

func (r *otlpReceiver) writeJSON(path string) error {
	if path == "" {
		return nil
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewEncoder(file).Encode(r.snapshot())
}

func (r *otlpReceiver) serveHTTP(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(w, "OTLP receiver accepts POST only", http.StatusMethodNotAllowed)
		return
	}

	signal := otlpSignal(request.URL.Path)
	if signal == "" {
		http.NotFound(w, request)
		return
	}

	body := io.Reader(http.MaxBytesReader(w, request.Body, maxOTLPBodyBytes))
	if strings.EqualFold(strings.TrimSpace(request.Header.Get("Content-Encoding")), "gzip") {
		compressed, err := gzip.NewReader(body)
		if err != nil {
			http.Error(w, "decode gzip: "+err.Error(), http.StatusBadRequest)
			return
		}
		defer compressed.Close()
		body = compressed
	}
	payload, err := io.ReadAll(body)
	if err != nil {
		http.Error(w, "read OTLP body: "+err.Error(), http.StatusBadRequest)
		return
	}

	requestMessage, responseMessage := otlpMessages(signal)
	if requestMessage == nil {
		http.NotFound(w, request)
		return
	}
	isJSON := strings.Contains(strings.ToLower(request.Header.Get("Content-Type")), "json")
	if isJSON {
		err = (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(payload, requestMessage)
	} else {
		err = proto.Unmarshal(payload, requestMessage)
	}
	if err != nil {
		http.Error(w, "decode OTLP "+signal+": "+err.Error(), http.StatusBadRequest)
		return
	}
	compactOTLP(requestMessage)
	redactOTLP(requestMessage)

	canonical, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(requestMessage)
	if err != nil {
		http.Error(w, "encode OTLP JSON: "+err.Error(), http.StatusInternalServerError)
		return
	}
	r.mu.Lock()
	r.summary.add(signal, requestMessage)
	r.exports = append(r.exports, otlpExport{
		ReceivedAt: time.Now().UTC(),
		Signal:     signal,
		Payload:    append(json.RawMessage(nil), canonical...),
	})
	r.mu.Unlock()

	if isJSON {
		response, marshalErr := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(responseMessage)
		if marshalErr != nil {
			http.Error(w, marshalErr.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(response)
		return
	}
	response, err := proto.Marshal(responseMessage)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	_, _ = w.Write(response)
}

func compactOTLP(message proto.Message) {
	request, ok := message.(*collectorlogsv1.ExportLogsServiceRequest)
	if !ok {
		return
	}
	for _, resourceLogs := range request.ResourceLogs {
		for _, scopeLogs := range resourceLogs.ScopeLogs {
			records := scopeLogs.LogRecords[:0]
			for _, record := range scopeLogs.LogRecords {
				// Codex emits one record for nearly every streamed SSE frame. Request,
				// tool, token, and timing data are already represented by its other
				// logs and metrics, so retaining these deltas only bloats artifacts.
				if attributeValue(record.Attributes, "event.name") == "codex.sse_event" {
					continue
				}
				// Codex emits an implementation-level message when its metrics provider
				// flushes. It is exporter plumbing, not an agent-loop event.
				if record.Body != nil && record.Body.GetStringValue() == "flushing OTEL metrics" {
					continue
				}
				records = append(records, record)
			}
			scopeLogs.LogRecords = records
		}
	}
}

func (s *otlpSummary) add(signal string, message proto.Message) {
	if s.ExportBatches == nil {
		s.ExportBatches = map[string]int{}
		s.Resources = map[string]int{}
		s.Spans = map[string]int{}
		s.MetricExports = map[string]int{}
		s.LogEvents = map[string]int{}
	}
	s.ExportBatches[signal]++
	switch request := message.(type) {
	case *collectortracev1.ExportTraceServiceRequest:
		for _, resourceSpans := range request.ResourceSpans {
			s.Resources[resourceService(resourceSpans.Resource.GetAttributes())]++
			for _, scopeSpans := range resourceSpans.ScopeSpans {
				for _, span := range scopeSpans.Spans {
					s.Spans[span.Name]++
				}
			}
		}
	case *collectormetricsv1.ExportMetricsServiceRequest:
		for _, resourceMetrics := range request.ResourceMetrics {
			s.Resources[resourceService(resourceMetrics.Resource.GetAttributes())]++
			for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
				for _, metric := range scopeMetrics.Metrics {
					s.MetricExports[metric.Name]++
				}
			}
		}
	case *collectorlogsv1.ExportLogsServiceRequest:
		for _, resourceLogs := range request.ResourceLogs {
			s.Resources[resourceService(resourceLogs.Resource.GetAttributes())]++
			for _, scopeLogs := range resourceLogs.ScopeLogs {
				for _, record := range scopeLogs.LogRecords {
					s.LogRecords++
					// Vendor emitters such as Codex use the first-class OTLP field for
					// the source-level tracing event and event.name for the stable business
					// event. Wingman uses the first-class field directly.
					name := attributeValue(record.Attributes, "event.name")
					if name == "" {
						name = record.GetEventName()
					}
					if name == "" && record.Body != nil {
						name = record.Body.GetStringValue()
					}
					if name == "" {
						name = "<unnamed>"
					}
					s.LogEvents[name]++
				}
			}
		}
	}
}

func cloneOTLPSummary(source otlpSummary) otlpSummary {
	clone := source
	clone.ExportBatches = cloneCountMap(source.ExportBatches)
	clone.Resources = cloneCountMap(source.Resources)
	clone.Spans = cloneCountMap(source.Spans)
	clone.MetricExports = cloneCountMap(source.MetricExports)
	clone.LogEvents = cloneCountMap(source.LogEvents)
	return clone
}

func cloneCountMap(source map[string]int) map[string]int {
	if source == nil {
		return nil
	}
	clone := make(map[string]int, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func resourceService(attributes []*commonv1.KeyValue) string {
	if value := attributeValue(attributes, "service.name"); value != "" {
		return value
	}
	return "<unknown>"
}

func attributeValue(attributes []*commonv1.KeyValue, key string) string {
	for _, candidate := range attributes {
		if candidate.Key == key && candidate.Value != nil {
			return candidate.Value.GetStringValue()
		}
	}
	return ""
}

func redactOTLP(message proto.Message) {
	switch request := message.(type) {
	case *collectortracev1.ExportTraceServiceRequest:
		for _, resourceSpans := range request.ResourceSpans {
			if resourceSpans.Resource != nil {
				resourceSpans.Resource.Attributes = redactAttributes(resourceSpans.Resource.Attributes)
			}
			for _, scopeSpans := range resourceSpans.ScopeSpans {
				if scopeSpans.Scope != nil {
					scopeSpans.Scope.Attributes = redactAttributes(scopeSpans.Scope.Attributes)
				}
				for _, span := range scopeSpans.Spans {
					span.Attributes = redactAttributes(span.Attributes)
					for _, event := range span.Events {
						event.Attributes = redactAttributes(event.Attributes)
					}
					for _, link := range span.Links {
						link.Attributes = redactAttributes(link.Attributes)
					}
				}
			}
		}
	case *collectormetricsv1.ExportMetricsServiceRequest:
		for _, resourceMetrics := range request.ResourceMetrics {
			if resourceMetrics.Resource != nil {
				resourceMetrics.Resource.Attributes = redactAttributes(resourceMetrics.Resource.Attributes)
			}
			for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
				if scopeMetrics.Scope != nil {
					scopeMetrics.Scope.Attributes = redactAttributes(scopeMetrics.Scope.Attributes)
				}
				for _, metric := range scopeMetrics.Metrics {
					redactMetric(metric)
				}
			}
		}
	case *collectorlogsv1.ExportLogsServiceRequest:
		for _, resourceLogs := range request.ResourceLogs {
			if resourceLogs.Resource != nil {
				resourceLogs.Resource.Attributes = redactAttributes(resourceLogs.Resource.Attributes)
			}
			for _, scopeLogs := range resourceLogs.ScopeLogs {
				if scopeLogs.Scope != nil {
					scopeLogs.Scope.Attributes = redactAttributes(scopeLogs.Scope.Attributes)
				}
				for _, record := range scopeLogs.LogRecords {
					record.Attributes = redactAttributes(record.Attributes)
					redactLogBody(record)
				}
			}
		}
	}
}

func redactLogBody(record *logsv1.LogRecord) {
	if record.Body == nil {
		return
	}
	value, ok := record.Body.Value.(*commonv1.AnyValue_StringValue)
	if ok && structuralEventName(value.StringValue) {
		return
	}
	// Event bodies are not needed for the benchmark comparison. Keep only stable
	// vendor event names; prompt, response, tool, and arbitrary structured bodies
	// are removed even when an emitter's content capture was enabled elsewhere.
	record.Body = nil
}

func structuralEventName(value string) bool {
	if len(value) == 0 || len(value) > 128 ||
		(!strings.HasPrefix(value, "claude_code.") && !strings.HasPrefix(value, "codex.")) {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func redactMetric(metric *metricsv1.Metric) {
	switch data := metric.Data.(type) {
	case *metricsv1.Metric_Gauge:
		for _, point := range data.Gauge.DataPoints {
			redactNumberPoint(point)
		}
	case *metricsv1.Metric_Sum:
		for _, point := range data.Sum.DataPoints {
			redactNumberPoint(point)
		}
	case *metricsv1.Metric_Histogram:
		for _, point := range data.Histogram.DataPoints {
			point.Attributes = redactAttributes(point.Attributes)
			for _, exemplar := range point.Exemplars {
				exemplar.FilteredAttributes = redactAttributes(exemplar.FilteredAttributes)
			}
		}
	case *metricsv1.Metric_ExponentialHistogram:
		for _, point := range data.ExponentialHistogram.DataPoints {
			point.Attributes = redactAttributes(point.Attributes)
			for _, exemplar := range point.Exemplars {
				exemplar.FilteredAttributes = redactAttributes(exemplar.FilteredAttributes)
			}
		}
	case *metricsv1.Metric_Summary:
		for _, point := range data.Summary.DataPoints {
			point.Attributes = redactAttributes(point.Attributes)
		}
	}
}

func redactNumberPoint(point *metricsv1.NumberDataPoint) {
	point.Attributes = redactAttributes(point.Attributes)
	for _, exemplar := range point.Exemplars {
		exemplar.FilteredAttributes = redactAttributes(exemplar.FilteredAttributes)
	}
}

func redactAttributes(attributes []*commonv1.KeyValue) []*commonv1.KeyValue {
	output := attributes[:0]
	for _, candidate := range attributes {
		key := strings.ToLower(candidate.Key)
		if strings.HasPrefix(key, "enduser.") || strings.HasPrefix(key, "organization.") ||
			strings.HasPrefix(key, "tenant.") || strings.HasPrefix(key, "user.account_") ||
			key == "user.email" || key == "user.id" || key == "user.organization_id" {
			continue
		}
		switch key {
		case "arguments", "command", "conversation.id", "cwd", "error", "error.message", "exception.message",
			"exception.stacktrace", "file.path", "file_path", "full_command", "gen_ai.conversation.id",
			"gen_ai.input.messages", "gen_ai.output.messages", "gen_ai.system_instructions",
			"gen_ai.tool.call.arguments", "gen_ai.tool.call.result", "gen_ai.tool.definitions",
			"host.name", "message.content", "output", "prompt", "request.body", "response", "response.body",
			"process.command_args", "process.command_line", "process.executable.path", "process.owner", "session.id",
			"system_prompt", "tool.content", "tool_input", "tool_output", "tool_parameters", "tool_response",
			"tool_result", "user_prompt":
			continue
		default:
			output = append(output, candidate)
		}
	}
	return output
}

func otlpSignal(path string) string {
	switch {
	case strings.HasSuffix(path, "/v1/traces"), strings.HasSuffix(path, "/traces"):
		return "traces"
	case strings.HasSuffix(path, "/v1/metrics"), strings.HasSuffix(path, "/metrics"):
		return "metrics"
	case strings.HasSuffix(path, "/v1/logs"), strings.HasSuffix(path, "/logs"):
		return "logs"
	default:
		return ""
	}
}

func otlpMessages(signal string) (proto.Message, proto.Message) {
	switch signal {
	case "traces":
		return &collectortracev1.ExportTraceServiceRequest{}, &collectortracev1.ExportTraceServiceResponse{}
	case "metrics":
		return &collectormetricsv1.ExportMetricsServiceRequest{}, &collectormetricsv1.ExportMetricsServiceResponse{}
	case "logs":
		return &collectorlogsv1.ExportLogsServiceRequest{}, &collectorlogsv1.ExportLogsServiceResponse{}
	default:
		panic(fmt.Sprintf("unknown OTLP signal %q", signal))
	}
}
