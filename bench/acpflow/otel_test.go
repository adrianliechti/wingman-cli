package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	collectorlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/proto"
)

func TestOTLPReceiverStoresJSONAndProtobufAsCanonicalJSON(t *testing.T) {
	receiver := newOTLPReceiver()
	defer receiver.Close()

	traceJSON := `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex-cli"}},{"key":"host.name","value":{"stringValue":"private-host"}}]},"scopeSpans":[{"spans":[{"traceId":"AQIDBAUGBwgJCgsMDQ4PEA==","spanId":"AQIDBAUGBwg=","name":"codex.api_request","attributes":[{"key":"user_prompt","value":{"stringValue":"secret prompt"}},{"key":"gen_ai.tool.call.arguments","value":{"stringValue":"secret tool input"}},{"key":"gen_ai.tool.call.result","value":{"stringValue":"secret tool result"}}]}]}]}]}`
	request, err := http.NewRequest(http.MethodPost, receiver.URL()+"/v1/traces", strings.NewReader(traceJSON))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("trace status = %s", response.Status)
	}

	binary, err := proto.Marshal(&collectorlogsv1.ExportLogsServiceRequest{})
	if err != nil {
		t.Fatal(err)
	}
	response, err = http.Post(receiver.URL()+"/logs", "application/x-protobuf", bytes.NewReader(binary))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("log status = %s", response.Status)
	}

	artifact := receiver.snapshot()
	if len(artifact.Exports) != 2 || artifact.Exports[0].Signal != "traces" || artifact.Exports[1].Signal != "logs" {
		t.Fatalf("exports = %#v", artifact.Exports)
	}
	if !bytes.Contains(artifact.Exports[0].Payload, []byte("codex.api_request")) ||
		!bytes.Contains(artifact.Exports[0].Payload, []byte("service.name")) {
		t.Fatalf("canonical trace payload = %s", artifact.Exports[0].Payload)
	}
	if bytes.Contains(artifact.Exports[0].Payload, []byte("private-host")) ||
		bytes.Contains(artifact.Exports[0].Payload, []byte("secret prompt")) ||
		bytes.Contains(artifact.Exports[0].Payload, []byte("secret tool")) {
		t.Fatalf("canonical trace payload contains private values: %s", artifact.Exports[0].Payload)
	}
	if artifact.Summary.ExportBatches["traces"] != 1 ||
		artifact.Summary.Resources["codex-cli"] != 1 ||
		artifact.Summary.Spans["codex.api_request"] != 1 {
		t.Fatalf("summary = %#v", artifact.Summary)
	}
}

func TestCompactOTLPOmitsCodexStreamDeltas(t *testing.T) {
	record := func(name string) *logsv1.LogRecord {
		return &logsv1.LogRecord{Attributes: []*commonv1.KeyValue{{
			Key: "event.name",
			Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{
				StringValue: name,
			}},
		}}}
	}
	request := &collectorlogsv1.ExportLogsServiceRequest{ResourceLogs: []*logsv1.ResourceLogs{{
		ScopeLogs: []*logsv1.ScopeLogs{{LogRecords: []*logsv1.LogRecord{
			record("codex.sse_event"),
			record("codex.api_request"),
		}}},
	}}}

	compactOTLP(request)
	records := request.ResourceLogs[0].ScopeLogs[0].LogRecords
	if len(records) != 1 || attributeValue(records[0].Attributes, "event.name") != "codex.api_request" {
		t.Fatalf("compacted records = %#v", records)
	}
}

func TestOTLPReceiverAcceptsGzipAndWritesArtifact(t *testing.T) {
	receiver := newOTLPReceiver()
	defer receiver.Close()

	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write([]byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, receiver.URL()+"/v1/metrics", bytes.NewReader(compressed.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Content-Encoding", "gzip")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("metrics status = %s", response.Status)
	}

	path := t.TempDir() + "/telemetry.json"
	if err := receiver.writeJSON(path); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var artifact otlpArtifact
	if err := json.Unmarshal(contents, &artifact); err != nil {
		t.Fatal(err)
	}
	if len(artifact.Exports) != 1 || artifact.Exports[0].Signal != "metrics" {
		t.Fatalf("artifact = %#v", artifact)
	}
	if bytes.Contains(contents, []byte("\n  \"")) {
		t.Fatalf("telemetry artifact is unexpectedly pretty-printed: %s", contents)
	}
}
