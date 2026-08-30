// Package telemetry configures slim OTLP HTTP/protobuf OpenTelemetry exporters
// and instruments the built-in GenAI agent and MCP client according to the
// latest OpenTelemetry GenAI semantic conventions. Environment-based setup is
// a no-op unless an enabled signal has OTLP exporter configuration.
//
// Prompts, model responses, tool arguments, and tool results are omitted by
// default. They can be routed to inference spans, inference-detail log events,
// or both through ContentCaptureMode. This opt-in data can contain source code,
// credentials, or other sensitive user data.
package telemetry
