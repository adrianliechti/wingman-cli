package agent

import (
	"slices"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

// NewToolPresentation creates display metadata without changing the execution
// name or arguments stored on a tool call.
func NewToolPresentation(name, kind, args string, locations []ToolLocation) *ToolPresentation {
	p := tool.Present(name, kind, args, len(locations) > 0)
	locs := slices.Clone(locations)
	if len(locs) == 0 && p.Path != "" {
		locs = []ToolLocation{{Path: p.Path, Line: p.Line}}
	}
	return &ToolPresentation{
		Title: p.Title, Kind: p.Kind, Args: p.Args, Hint: p.Hint, Locations: locs,
	}
}
