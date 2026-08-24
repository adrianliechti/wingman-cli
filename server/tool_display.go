package server

import "github.com/adrianliechti/wingman-agent/pkg/agent"

type displayedTool struct {
	name      string
	kind      string
	args      string
	hint      string
	locations []agent.ToolLocation
}

// displayTool renders metadata supplied by the tool producer. The fallback
// keeps sessions written before presentation metadata was introduced usable.
func displayTool(
	name, kind, args string,
	locations []agent.ToolLocation,
	presentation *agent.ToolPresentation,
) displayedTool {
	if presentation == nil {
		presentation = agent.NewToolPresentation(name, kind, args, locations)
	}
	title := presentation.Title
	if title == "" {
		title = name
	}
	displayKind := presentation.Kind
	if displayKind == "" {
		displayKind = kind
	}
	displayLocations := presentation.Locations
	if len(displayLocations) == 0 && len(locations) > 0 {
		displayLocations = locations
	}
	return displayedTool{
		name: title, kind: displayKind, args: presentation.Args,
		hint: presentation.Hint, locations: displayLocations,
	}
}
