package lsp

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

func intArg(args map[string]any, key string) int {
	switch v := args[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	case int64:
		return int(v)
	default:
		return 0
	}
}

func parseLocationResponse(data json.RawMessage) ([]Location, error) {
	if data == nil || string(data) == "null" {
		return nil, nil
	}

	var loc Location
	if err := json.Unmarshal(data, &loc); err == nil && loc.URI != "" {
		return []Location{loc}, nil
	}

	var locs []Location
	if err := json.Unmarshal(data, &locs); err == nil {
		return locs, nil
	}

	var links []struct {
		TargetURI   string `json:"targetUri"`
		TargetRange Range  `json:"targetRange"`
	}
	if err := json.Unmarshal(data, &links); err == nil {
		for _, link := range links {
			locs = append(locs, Location{
				URI:   link.TargetURI,
				Range: link.TargetRange,
			})
		}
		return locs, nil
	}

	return nil, fmt.Errorf("unexpected location response format")
}

func formatLocations(title string, locations []Location, workingDir string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s (%d found):\n", title, len(locations)))

	for _, loc := range locations {
		path := uriToPath(loc.URI)
		if rel, err := filepath.Rel(workingDir, path); err == nil && !strings.HasPrefix(rel, "..") {
			path = rel
		}
		sb.WriteString(fmt.Sprintf("  %s:%d:%d\n", path, loc.Range.Start.Line+1, loc.Range.Start.Character+1))
	}

	return sb.String()
}

func formatDiagnostics(diagnostics []Diagnostic, filePath string, workingDir string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Diagnostics (%d found):\n", len(diagnostics)))

	displayPath := filePath
	if rel, err := filepath.Rel(workingDir, filePath); err == nil && !strings.HasPrefix(rel, "..") {
		displayPath = rel
	}

	for _, diag := range diagnostics {
		severity := diagnosticSeverityName(diag.Severity)
		line := diag.Range.Start.Line + 1
		col := diag.Range.Start.Character + 1
		source := ""
		if diag.Source != "" {
			source = fmt.Sprintf("[%s] ", diag.Source)
		}
		sb.WriteString(fmt.Sprintf("  %s:%d:%d %s: %s%s\n", displayPath, line, col, severity, source, diag.Message))
	}

	return sb.String()
}
