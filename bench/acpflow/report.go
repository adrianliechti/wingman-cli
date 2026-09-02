package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
)

const reportDataMarker = "__ACPFLOW_REPORT_DATA__"

type comparisonReport struct {
	SchemaVersion int                `json:"schema_version"`
	GeneratedAt   time.Time          `json:"generated_at"`
	Effort        string             `json:"effort"`
	Runs          []comparisonRun    `json:"runs"`
	Capabilities  []capabilityReview `json:"capabilities"`
	Improvements  []improvement      `json:"improvements"`
	Methodology   []string           `json:"methodology"`
	Sources       []reportSource     `json:"sources"`
}

type comparisonRun struct {
	Agent           string              `json:"agent"`
	Model           string              `json:"model"`
	Effort          string              `json:"effort"`
	Scenario        string              `json:"scenario"`
	DurationMS      int64               `json:"duration_ms"`
	InitializeMS    int64               `json:"initialize_ms"`
	NewSessionMS    int64               `json:"new_session_ms"`
	Success         bool                `json:"success"`
	Error           string              `json:"error,omitempty"`
	ToolCalls       int                 `json:"tool_calls"`
	ModelRequests   int                 `json:"model_requests"`
	SourceFileCount int                 `json:"source_file_count,omitempty"`
	Turns           []comparisonTurn    `json:"turns"`
	Telemetry       comparisonTelemetry `json:"telemetry"`
	ResultArtifact  string              `json:"result_artifact"`
	OTELArtifact    string              `json:"otel_artifact"`
}

type comparisonTurn struct {
	Name       string            `json:"name"`
	DurationMS int64             `json:"duration_ms"`
	Success    bool              `json:"success"`
	Error      string            `json:"error,omitempty"`
	ToolCalls  int               `json:"tool_calls"`
	Events     []comparisonEvent `json:"events"`
}

type comparisonEvent struct {
	AtMS     int64  `json:"at_ms"`
	Kind     string `json:"kind"`
	Title    string `json:"title,omitempty"`
	Category string `json:"category,omitempty"`
	Status   string `json:"status,omitempty"`
}

type comparisonTelemetry struct {
	CapturedAt      time.Time      `json:"captured_at"`
	TraceBatches    int            `json:"trace_batches"`
	MetricBatches   int            `json:"metric_batches"`
	LogBatches      int            `json:"log_batches"`
	SpanCount       int            `json:"span_count"`
	MetricFamilies  int            `json:"metric_families"`
	MetricExports   int            `json:"metric_exports"`
	LogRecords      int            `json:"log_records"`
	Resources       map[string]int `json:"resources_by_service,omitempty"`
	Spans           map[string]int `json:"spans,omitempty"`
	Metrics         map[string]int `json:"metrics,omitempty"`
	Events          map[string]int `json:"events,omitempty"`
	TraceModeNotice string         `json:"trace_mode_notice,omitempty"`
}

type capabilityReview struct {
	Capability string `json:"capability"`
	Wingman    string `json:"wingman"`
	Claude     string `json:"claude"`
	Codex      string `json:"codex"`
	Assessment string `json:"assessment"`
	Tone       string `json:"tone"`
}

type improvement struct {
	Priority string `json:"priority"`
	Title    string `json:"title"`
	Why      string `json:"why"`
	Scope    string `json:"scope"`
}

type reportSource struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type reportCandidate struct {
	resultPath string
	otelPath   string
	modified   time.Time
	result     benchResult
}

func buildComparisonReport(directory, effort string) (comparisonReport, error) {
	candidates, err := discoverReportCandidates(directory, effort)
	if err != nil {
		return comparisonReport{}, err
	}
	if len(candidates) == 0 {
		return comparisonReport{}, fmt.Errorf("no ACP result and OTEL sidecar pairs found in %s", directory)
	}

	report := comparisonReport{
		SchemaVersion: 1,
		GeneratedAt:   time.Now().UTC(),
		Effort:        effort,
		Capabilities:  telemetryCapabilityReview(),
		Improvements:  telemetryImprovements(),
		Methodology: []string{
			"Runs execute sequentially over ACP against the same gateway; concurrency would distort wall-clock latency.",
			"Wingman's measured mutation surface is one provider-neutral JSON-schema edit function; it does not advertise a proprietary free-form patch tool.",
			"Tool calls come from ACP lifecycle events. Model-request counts come from each harness's native OTEL signal.",
			"Raw OTLP payloads are retained in the .otel.json sidecars; this report embeds only structural summaries and content-free event timing.",
			"Results are single samples and directional, not a statistically significant model-quality evaluation.",
		},
		Sources: []reportSource{
			{Label: "OpenAI Codex configuration reference", URL: "https://developers.openai.com/codex/config-reference"},
			{Label: "Claude Code observability", URL: "https://code.claude.com/docs/en/agent-sdk/observability"},
			{Label: "OpenTelemetry OTLP exporter configuration", URL: "https://opentelemetry.io/docs/languages/sdk-configuration/otlp-exporter/"},
			{Label: "OpenTelemetry GenAI attributes", URL: "https://opentelemetry.io/docs/specs/semconv/registry/attributes/gen-ai/"},
		},
	}

	for _, candidate := range candidates {
		run, err := normalizeComparisonRun(candidate)
		if err != nil {
			return comparisonReport{}, err
		}
		report.Runs = append(report.Runs, run)
	}
	sort.SliceStable(report.Runs, func(i, j int) bool {
		left, right := report.Runs[i], report.Runs[j]
		if scenarioRank(left.Scenario) != scenarioRank(right.Scenario) {
			return scenarioRank(left.Scenario) < scenarioRank(right.Scenario)
		}
		if agentRank(left.Agent) != agentRank(right.Agent) {
			return agentRank(left.Agent) < agentRank(right.Agent)
		}
		if modelRank(left.Model) != modelRank(right.Model) {
			return modelRank(left.Model) < modelRank(right.Model)
		}
		return left.Model < right.Model
	})
	return report, nil
}

func discoverReportCandidates(directory, effort string) ([]reportCandidate, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	latest := make(map[string]reportCandidate)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".otel.json") {
			continue
		}
		resultPath := filepath.Join(directory, name)
		contents, err := os.ReadFile(resultPath)
		if err != nil {
			continue
		}
		var result benchResult
		if json.Unmarshal(contents, &result) != nil || result.Agent == "" || result.Model == "" || result.Scenario == "" {
			continue
		}
		if effort != "" && result.Effort != effort {
			continue
		}
		otelPath := strings.TrimSuffix(resultPath, filepath.Ext(resultPath)) + ".otel.json"
		if _, err := os.Stat(otelPath); err != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		candidate := reportCandidate{resultPath: resultPath, otelPath: otelPath, modified: info.ModTime(), result: result}
		key := strings.Join([]string{result.Scenario, result.Agent, result.Model, result.Effort}, "\x00")
		if previous, ok := latest[key]; !ok || candidate.modified.After(previous.modified) {
			latest[key] = candidate
		}
	}

	result := make([]reportCandidate, 0, len(latest))
	for _, candidate := range latest {
		result = append(result, candidate)
	}
	return result, nil
}

func normalizeComparisonRun(candidate reportCandidate) (comparisonRun, error) {
	contents, err := os.ReadFile(candidate.otelPath)
	if err != nil {
		return comparisonRun{}, err
	}
	var artifact otlpArtifact
	if err := json.Unmarshal(contents, &artifact); err != nil {
		return comparisonRun{}, fmt.Errorf("decode %s: %w", candidate.otelPath, err)
	}
	summary := rebuiltOTLPSummary(artifact)

	result := candidate.result
	run := comparisonRun{
		Agent:           result.Agent,
		Model:           result.Model,
		Effort:          result.Effort,
		Scenario:        result.Scenario,
		InitializeMS:    result.InitMS,
		NewSessionMS:    result.NewSessionMS,
		Error:           reportFailure(result.Error),
		ResultArtifact:  filepath.Base(candidate.resultPath),
		OTELArtifact:    filepath.Base(candidate.otelPath),
		ModelRequests:   modelRequestCount(result.Agent, summary),
		SourceFileCount: finalSourceFileCount(result.Turns),
		Telemetry: comparisonTelemetry{
			CapturedAt:     artifact.CapturedAt,
			TraceBatches:   summary.ExportBatches["traces"],
			MetricBatches:  summary.ExportBatches["metrics"],
			LogBatches:     summary.ExportBatches["logs"],
			SpanCount:      sumCountMap(summary.Spans),
			MetricFamilies: len(summary.MetricExports),
			MetricExports:  sumCountMap(summary.MetricExports),
			LogRecords:     summary.LogRecords,
			Resources:      cloneCountMap(summary.Resources),
			Spans:          cloneCountMap(summary.Spans),
			Metrics:        cloneCountMap(summary.MetricExports),
			Events:         cloneCountMap(summary.LogEvents),
		},
	}
	if result.Agent == "codex" && run.Telemetry.TraceBatches == 0 {
		run.Telemetry.TraceModeNotice = "Codex trace export was intentionally disabled for the timed matrix: its native trace stream contains thousands of implementation-level spans. Logs and metrics remain enabled."
	}

	if result.Scenario == "dashboard" {
		run.Success = result.Error == "" && len(result.Turns) > 0
		for _, turn := range result.Turns {
			normalized := normalizeTurn(turn.Name, turn.TotalMS, turn.BuildPassed && turn.RequirementsPassed && turn.Error == "", reportFailure(turn.Error), turn.Events)
			run.Turns = append(run.Turns, normalized)
			run.DurationMS += turn.TotalMS
			run.ToolCalls += normalized.ToolCalls
			if !normalized.Success {
				run.Success = false
			}
		}
	} else {
		run.DurationMS = result.TotalMS
		run.Success = result.TestsPassed && result.Error == ""
		turn := normalizeTurn("fix_invoice", result.TotalMS, run.Success, reportFailure(result.Error), result.Events)
		run.ToolCalls = turn.ToolCalls
		run.Turns = []comparisonTurn{turn}
	}
	return run, nil
}

func rebuiltOTLPSummary(artifact otlpArtifact) otlpSummary {
	if len(artifact.Exports) == 0 {
		return artifact.Summary
	}
	var summary otlpSummary
	for _, export := range artifact.Exports {
		message, _ := otlpMessages(export.Signal)
		if message == nil || protojson.Unmarshal(export.Payload, message) != nil {
			continue
		}
		compactOTLP(message)
		summary.add(export.Signal, message)
	}
	if len(summary.ExportBatches) == 0 {
		return artifact.Summary
	}
	return summary
}

func normalizeTurn(name string, duration int64, success bool, failure string, events []timedEvent) comparisonTurn {
	turn := comparisonTurn{Name: name, DurationMS: duration, Success: success, Error: failure}
	for _, event := range events {
		category := toolCategory(event.Title)
		normalized := comparisonEvent{
			AtMS:     event.AtMS,
			Kind:     event.Kind,
			Title:    reportToolTitle(category),
			Category: category,
			Status:   sanitizeReportText(event.Status, 32),
		}
		turn.Events = append(turn.Events, normalized)
		if event.Kind == "tool_start" {
			turn.ToolCalls++
		}
	}
	return turn
}

func reportFailure(value string) string {
	value = strings.ToLower(value)
	switch {
	case value == "":
		return ""
	case strings.Contains(value, "deadline"), strings.Contains(value, "timed out"), strings.Contains(value, "timeout"):
		return "timeout"
	case strings.Contains(value, "cancel"):
		return "canceled"
	case strings.Contains(value, "build"), strings.Contains(value, "test"), strings.Contains(value, "verification"):
		return "verification failed"
	default:
		return "agent error"
	}
}

func reportToolTitle(category string) string {
	switch category {
	case "read":
		return "Read"
	case "search":
		return "Search"
	case "edit":
		return "Edit"
	case "execute":
		return "Execute"
	case "delegate":
		return "Delegate"
	case "other":
		return "Tool"
	default:
		return ""
	}
}

func sanitizeReportText(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > limit {
		value = value[:limit] + "…"
	}
	return value
}

func toolCategory(title string) string {
	value := strings.ToLower(title)
	switch {
	case strings.Contains(value, "read"):
		return "read"
	case strings.Contains(value, "find"), strings.Contains(value, "search"), strings.Contains(value, "list"), strings.Contains(value, "glob"):
		return "search"
	case strings.Contains(value, "patch"), strings.Contains(value, "edit"), strings.Contains(value, "write"):
		return "edit"
	case strings.Contains(value, "command"), strings.Contains(value, "execute"), strings.Contains(value, "terminal"), strings.Contains(value, "shell"):
		return "execute"
	case strings.Contains(value, "agent"), strings.Contains(value, "task"):
		return "delegate"
	case title != "":
		return "other"
	default:
		return ""
	}
}

func modelRequestCount(agent string, summary otlpSummary) int {
	switch agent {
	case "wingman":
		total := 0
		for name, count := range summary.Spans {
			if strings.HasPrefix(name, "chat ") || name == "chat" {
				total += count
			}
		}
		return total
	case "claude":
		return summary.Spans["claude_code.llm_request"]
	case "codex":
		return summary.LogEvents["codex.api_request"]
	default:
		return 0
	}
}

func finalSourceFileCount(turns []benchTurn) int {
	if len(turns) == 0 {
		return 0
	}
	return turns[len(turns)-1].FileCount
}

func sumCountMap(values map[string]int) int {
	total := 0
	for _, value := range values {
		total += value
	}
	return total
}

func scenarioRank(value string) int {
	switch value {
	case "invoice":
		return 0
	case "invoice_dense":
		return 1
	case "dashboard":
		return 2
	default:
		return 3
	}
}

func agentRank(value string) int {
	switch value {
	case "wingman":
		return 0
	case "codex":
		return 1
	case "claude":
		return 2
	default:
		return 3
	}
}

func modelRank(value string) int {
	switch {
	case strings.Contains(value, "sonnet"):
		return 0
	case strings.Contains(value, "opus"):
		return 1
	default:
		return 2
	}
}

func telemetryCapabilityReview() []capabilityReview {
	return []capabilityReview{
		{Capability: "Signal coverage", Wingman: "Traces, metrics, optional log events", Claude: "Traces, metrics, audit events", Codex: "Traces, metrics, audit events", Assessment: "All three cover the core signals.", Tone: "good"},
		{Capability: "Semantic conventions", Wingman: "Native GenAI + MCP conventions", Claude: "Vendor spans with selected gen_ai attributes", Codex: "Primarily codex.* vendor schema", Assessment: "Wingman is easiest to consume with vendor-neutral tooling.", Tone: "good"},
		{Capability: "Tool timing", Wingman: "One execute_tool span", Claude: "Tool + permission wait + execution", Codex: "Tool events plus deep internal spans", Assessment: "Wingman cannot yet separate approval latency from execution latency.", Tone: "gap"},
		{Capability: "Retries and failures", Wingman: "Logical request status + exception", Claude: "Attempts, request IDs, status and retry events", Codex: "Attempt/status events and transport metrics", Assessment: "Wingman needs attempt-level diagnostics.", Tone: "gap"},
		{Capability: "Audit events", Wingman: "Inference detail and exception only", Claude: "Prompt, response, tool, permission, MCP, hook, skill, compaction", Codex: "Prompt, approval, tool, sandbox, network and lifecycle", Assessment: "Logs-only Wingman deployments cannot reconstruct the agent loop.", Tone: "gap"},
		{Capability: "Trace propagation", Wingman: "Agent hierarchy + MCP client/server W3C context", Claude: "Caller, subagent, shell and HTTP MCP propagation", Codex: "W3C context across app-server and model paths", Assessment: "Wingman's MCP support is strong; command subprocesses remain disconnected.", Tone: "mixed"},
		{Capability: "Privacy controls", Wingman: "No content by default; span/event capture modes", Claude: "Granular prompt, response, tool detail/content and raw-body gates", Codex: "Prompt opt-in plus exporter routing controls", Assessment: "Wingman's default is safe, but capture controls are less granular than Claude's.", Tone: "mixed"},
		{Capability: "Exporter operations", Wingman: "OTLP HTTP/protobuf; headers, TLS, compression and timeout via Go SDK", Claude: "gRPC, HTTP JSON/protobuf, console/Prometheus, diagnostics", Codex: "gRPC or HTTP JSON/binary, TLS, headers, bounded shutdown", Assessment: "Wingman covers the common collector path but lacks diagnostics and transport choice.", Tone: "mixed"},
		{Capability: "Product telemetry", Wingman: "Latency, tokens, calls, tool and MCP duration", Claude: "Adds cost, active time, LoC, commits and adoption", Codex: "Adds startup, memory, sandbox, network, skills and cost", Assessment: "Do not copy all vendor metrics; add only metrics tied to an operational question.", Tone: "neutral"},
	}
}

func telemetryImprovements() []improvement {
	return []improvement{
		{Priority: "P1", Title: "Add a structural audit-event layer", Why: "Tool decisions/results, permission outcomes, compaction, hooks, skills and MCP connection changes are invisible when only logs are exported.", Scope: "Content-free by default; correlate with trace_id, span_id, conversation and tool-call IDs."},
		{Priority: "P1", Title: "Expose request attempts and tool phases", Why: "A slow request may be model latency, retries, permission wait or execution. Wingman currently merges each pair into one duration.", Scope: "Attempt span events plus permission_wait and execution child spans; retain the current semantic parent spans."},
		{Priority: "P1", Title: "Propagate W3C context into command subprocesses", Why: "Instrumented builds and tests should appear below the exec tool span instead of as unrelated traces.", Scope: "Inject TRACEPARENT/TRACESTATE per process without forwarding OTLP credentials."},
		{Priority: "P2", Title: "Add exporter diagnostics and runtime controls", Why: "Asynchronous exporter failures are hard to see, and the current constructors do not honor sampling or processor interval settings themselves.", Scope: "Opt-in stderr diagnostics, dropped-item counters, sampler and batch/metric interval configuration, bounded shutdown reporting."},
		{Priority: "P2", Title: "Improve resource and agent correlation", Why: "service.version, turn ID, agent ID/parent ID and event sequence make multi-turn and delegated work much easier to reconstruct.", Scope: "Keep high-cardinality identifiers off metrics; attach them to spans and events only."},
		{Priority: "P3", Title: "Broaden exporters only on demand", Why: "Claude and Codex support gRPC and HTTP/JSON, but HTTP/protobuf covers the normal collector deployment.", Scope: "Add gRPC or JSON only if a target backend cannot accept HTTP/protobuf; avoid complexity without a real consumer."},
	}
}

func writeComparisonArtifacts(directory, effort, htmlPath string) (string, error) {
	report, err := buildComparisonReport(directory, effort)
	if err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')

	extension := filepath.Ext(htmlPath)
	dataPath := strings.TrimSuffix(htmlPath, extension) + ".json"
	if err := writeReportFile(dataPath, data); err != nil {
		return "", err
	}
	compact, err := json.Marshal(report)
	if err != nil {
		return "", err
	}
	if !strings.Contains(acpflowReportHTML, reportDataMarker) {
		return "", errors.New("ACP report template has no data marker")
	}
	html := strings.Replace(acpflowReportHTML, reportDataMarker, string(compact), 1)
	if err := writeReportFile(htmlPath, []byte(html)); err != nil {
		return "", err
	}
	return dataPath, nil
}

func writeReportFile(path string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".acpflow-report-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func automaticReportPath(value, directory, effort string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "none", "off", "false", "-":
		return ""
	case "", "auto":
		name := "acpflow-report.html"
		if effort != "" {
			name = "acpflow-" + effort + "-report.html"
		}
		return filepath.Join(directory, name)
	default:
		return value
	}
}
