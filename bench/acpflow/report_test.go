package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestComparisonReportNormalizesRunsWithoutContent(t *testing.T) {
	directory := t.TempDir()
	writeReportFixture(t, directory, "wingman", benchResult{
		Agent:        "wingman",
		Model:        "gpt-test",
		Effort:       "high",
		Scenario:     "dashboard",
		InitMS:       4,
		NewSessionMS: 6,
		Turns: []benchTurn{
			{Name: "create_dashboard", TotalMS: 1000, BuildPassed: true, RequirementsPassed: true, FileCount: 3, Events: []timedEvent{
				{AtMS: 4, Kind: "first_message", Preview: "private response"},
				{AtMS: 10, Kind: "tool_start", Title: "Read /private/path", Preview: "/private/path"},
			}},
			{Name: "add_range_selector", TotalMS: 500, BuildPassed: true, RequirementsPassed: true, FileCount: 3, Events: []timedEvent{
				{AtMS: 20, Kind: "tool_start", Title: "Apply patch", Preview: "secret patch"},
			}},
		},
	}, otlpArtifact{
		CapturedAt: time.Unix(1, 0).UTC(),
		Summary: otlpSummary{
			ExportBatches: map[string]int{"traces": 2, "metrics": 3, "logs": 1},
			Resources:     map[string]int{"wingman": 6},
			Spans:         map[string]int{"invoke_agent wingman": 2, "chat gpt-test": 7, "execute_tool read": 1},
			MetricExports: map[string]int{"gen_ai.client.operation.duration": 3},
			LogEvents:     map[string]int{"gen_ai.client.inference.operation.details": 7},
			LogRecords:    7,
		},
	})

	report, err := buildComparisonReport(directory, "high")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(report.Runs))
	}
	run := report.Runs[0]
	if run.DurationMS != 1500 || run.ToolCalls != 2 || run.ModelRequests != 7 || !run.Success {
		t.Fatalf("normalized run = %#v", run)
	}
	if run.Telemetry.SpanCount != 10 || run.Telemetry.MetricFamilies != 1 || run.SourceFileCount != 3 {
		t.Fatalf("normalized telemetry = %#v", run.Telemetry)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, sensitive := range []string{"private response", "/private/path", "secret patch"} {
		if strings.Contains(string(encoded), sensitive) {
			t.Fatalf("report contains sensitive event preview %q", sensitive)
		}
	}
}

func TestReportFailureUsesContentFreeCategories(t *testing.T) {
	if got := reportFailure("prompt: context deadline exceeded while reading /private/project"); got != "timeout" {
		t.Fatalf("failure = %q, want timeout", got)
	}
	if got := reportFailure("provider returned private response text"); got != "agent error" {
		t.Fatalf("failure = %q, want agent error", got)
	}
}

func TestComparisonReportKeepsNewestRunPerKey(t *testing.T) {
	directory := t.TempDir()
	oldPath := writeReportFixture(t, directory, "old", benchResult{Agent: "codex", Model: "gpt-test", Effort: "high", Scenario: "invoice", TotalMS: 900}, otlpArtifact{})
	newPath := writeReportFixture(t, directory, "new", benchResult{Agent: "codex", Model: "gpt-test", Effort: "high", Scenario: "invoice", TotalMS: 700}, otlpArtifact{})
	oldTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newPath, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}

	report, err := buildComparisonReport(directory, "high")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Runs) != 1 || report.Runs[0].DurationMS != 700 {
		t.Fatalf("runs = %#v", report.Runs)
	}
}

func TestWriteComparisonArtifactsProducesOfflineHTMLAndJSON(t *testing.T) {
	directory := t.TempDir()
	writeReportFixture(t, directory, "claude", benchResult{
		Agent: "claude", Model: "claude-test", Effort: "high", Scenario: "invoice", TotalMS: 300, TestsPassed: true,
	}, otlpArtifact{Summary: otlpSummary{Spans: map[string]int{"claude_code.llm_request": 2}}})

	htmlPath := filepath.Join(directory, "presentation.html")
	dataPath, err := writeComparisonArtifacts(directory, "high", htmlPath)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dataPath)
	if err != nil {
		t.Fatal(err)
	}
	var report comparisonReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Runs) != 1 || report.Runs[0].ModelRequests != 2 {
		t.Fatalf("report = %#v", report)
	}
	html, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(html), reportDataMarker) || !strings.Contains(string(html), "claude-test") {
		t.Fatal("offline report did not embed normalized data")
	}
}

func writeReportFixture(t *testing.T, directory, name string, result benchResult, telemetry otlpArtifact) string {
	t.Helper()
	resultPath := filepath.Join(directory, name+".json")
	otelPath := filepath.Join(directory, name+".otel.json")
	resultData, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	otelData, err := json.Marshal(telemetry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resultPath, resultData, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otelPath, otelData, 0o644); err != nil {
		t.Fatal(err)
	}
	return resultPath
}
