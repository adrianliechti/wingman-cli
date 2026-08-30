package agent

import (
	"os"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/telemetry"
)

// Options controls the built-in tools exposed by an Agent. Environment
// variables are resolved once when the agent starts and are combined with the
// explicitly supplied options.
type Options struct {
	DisableShell     bool
	DisableWebSearch bool
	DisableWebFetch  bool

	// Telemetry overrides the pipeline on the underlying agent.Config. A nil
	// value inherits that config, including automatic standard OTEL environment
	// configuration from agent.DefaultConfig.
	Telemetry *telemetry.Telemetry

	// ShutdownTelemetryOnClose transfers ownership of the selected telemetry
	// pipeline to this Agent. The Agent closes associated workspace MCP sessions
	// before shutting the pipeline down so their final metrics can be exported.
	// Most embedders should leave ownership with the component that created the
	// pipeline.
	ShutdownTelemetryOnClose bool
}

func resolveOptions(options []Options) Options {
	resolved := Options{
		DisableShell:     environmentEnabled("WINGMAN_DISABLE_SHELL"),
		DisableWebSearch: environmentEnabled("WINGMAN_DISABLE_WEBSEARCH"),
		DisableWebFetch:  environmentEnabled("WINGMAN_DISABLE_WEBFETCH"),
	}
	for _, option := range options {
		resolved.DisableShell = resolved.DisableShell || option.DisableShell
		resolved.DisableWebSearch = resolved.DisableWebSearch || option.DisableWebSearch
		resolved.DisableWebFetch = resolved.DisableWebFetch || option.DisableWebFetch
		if option.Telemetry != nil {
			resolved.Telemetry = option.Telemetry
		}
		resolved.ShutdownTelemetryOnClose = resolved.ShutdownTelemetryOnClose || option.ShutdownTelemetryOnClose
	}
	return resolved
}

func environmentEnabled(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
