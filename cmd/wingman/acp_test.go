package main

import (
	"slices"
	"strings"
	"testing"
)

func TestParseACPBackend(t *testing.T) {
	tests := []struct {
		value string
		want  acpBackend
	}{
		{value: "native", want: acpBackendNative},
		{value: " NATIVE ", want: acpBackendNative},
		{value: "wingman", want: acpBackendWingman},
		{value: "Wingman", want: acpBackendWingman},
	}

	for _, tt := range tests {
		got, err := parseACPBackend(tt.value)
		if err != nil {
			t.Errorf("parseACPBackend(%q): %v", tt.value, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseACPBackend(%q) = %q, want %q", tt.value, got, tt.want)
		}
	}
}

func TestParseACPBackendRejectsUnknownValue(t *testing.T) {
	if _, err := parseACPBackend("proxy"); err == nil {
		t.Fatal("parseACPBackend(proxy) succeeded")
	}
}

func TestCodexOTLPArgsEnableLogsAndMetrics(t *testing.T) {
	args := codexOTLPArgs("http://127.0.0.1:4318")
	var configs []string
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--config" {
			configs = append(configs, args[i+1])
		}
	}
	for _, want := range []string{
		"analytics.enabled=true",
		`otel.exporter={ otlp-http = { endpoint = "http://127.0.0.1:4318/v1/logs", protocol = "json" } }`,
		`otel.metrics_exporter={ otlp-http = { endpoint = "http://127.0.0.1:4318/v1/metrics", protocol = "json" } }`,
	} {
		if !slices.Contains(configs, want) {
			t.Errorf("configs = %q, missing %q", configs, want)
		}
	}
	for _, config := range configs {
		if strings.HasPrefix(config, "otel.trace_exporter=") {
			t.Errorf("unexpected noisy trace exporter override %q", config)
		}
		if strings.Contains(config, "update_plan") {
			t.Errorf("unexpected opt-in planning tool override %q", config)
		}
	}
}
