package server

import (
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
)

func TestDisplayToolKeepsFilePathOnlyAsChip(t *testing.T) {
	display := displayTool(
		"read", "", `{"file_path":"pkg/main.go","offset":12,"limit":4}`, nil, nil,
	)
	if display.name != "Read file" || display.kind != "read" || display.hint != "" {
		t.Fatalf("display = %#v", display)
	}
	if len(display.locations) != 1 || display.locations[0].Path != "pkg/main.go" || display.locations[0].Line != 12 {
		t.Fatalf("locations = %#v", display.locations)
	}
	if display.args != `{"limit":4}` {
		t.Fatalf("args = %s", display.args)
	}
}

func TestDisplayToolRecoversLocationFromHumanizedHistory(t *testing.T) {
	display := displayTool("Search files", "search", `{"path":"pkg","pattern":"TODO"}`, nil, nil)
	if display.hint != "TODO" || display.args != "" {
		t.Fatalf("display = %#v", display)
	}
	if len(display.locations) != 1 || display.locations[0].Path != "pkg" {
		t.Fatalf("locations = %#v", display.locations)
	}
}

func TestDisplayToolCompactsEditAndPreservesProvidedLocation(t *testing.T) {
	display := displayTool(
		"edit", "edit",
		`{"file_path":"ignored.go","old_string":"before","new_string":"after"}`,
		[]agent.ToolLocation{{Path: "actual.go", Line: 8}},
		nil,
	)
	if display.name != "Edit file" || display.kind != "edit" || display.args != "" || display.hint != "" {
		t.Fatalf("display = %#v", display)
	}
	if len(display.locations) != 1 || display.locations[0].Path != "actual.go" || display.locations[0].Line != 8 {
		t.Fatalf("locations = %#v", display.locations)
	}
}

func TestDisplayToolPromotesCommandWithoutRepeatingIt(t *testing.T) {
	display := displayTool("exec_command", "", `{"command":"go test ./...","workdir":"pkg"}`, nil, nil)
	if display.name != "Run command" || display.kind != "execute" || display.hint != "go test ./..." {
		t.Fatalf("display = %#v", display)
	}
	if display.args != `{"workdir":"pkg"}` {
		t.Fatalf("expanded args = %s", display.args)
	}
}

func TestDisplayToolPromotesSearchPatternWithoutRepeatingIt(t *testing.T) {
	display := displayTool("Find files", "search", `{"pattern":"README*","limit":20}`, nil, nil)
	if display.name != "Find files" || display.hint != "README*" || display.args != `{"limit":20}` {
		t.Fatalf("display = %#v", display)
	}
}

func TestDisplayToolPromotesWebInputWithoutRepeatingIt(t *testing.T) {
	display := displayTool("Open page", "fetch", `{"url":"https://example.com","line":40}`, nil, nil)
	if display.hint != "https://example.com" || display.args != `{"line":40}` {
		t.Fatalf("display = %#v", display)
	}
}

func TestDisplayToolSummarizesMultipleWebQueries(t *testing.T) {
	display := displayTool("Web search", "fetch", `{"queries":["first","second","third"],"allowed_domains":["example.com"]}`, nil, nil)
	if display.hint != "first · second +1" || display.args != `{"allowed_domains":["example.com"]}` {
		t.Fatalf("display = %#v", display)
	}
}

func TestDisplayToolUsesFriendlyCollaborationAction(t *testing.T) {
	display := displayTool("spawn_agent", "", `{"prompt":"Review the tool UX","reasoning_effort":"high"}`, nil, nil)
	if display.name != "Delegate task" || display.hint != "Review the tool UX" || display.args != `{"reasoning_effort":"high"}` {
		t.Fatalf("display = %#v", display)
	}
}

func TestDisplayToolUsesProducerPresentation(t *testing.T) {
	display := displayTool(
		"internal_name", "other", `{"internal":true}`, nil,
		&agent.ToolPresentation{
			Title: "External action", Kind: "search", Args: `{"limit":2}`,
			Hint: "needle", Locations: []agent.ToolLocation{{Path: "pkg/main.go"}},
		},
	)
	if display.name != "External action" || display.kind != "search" || display.args != `{"limit":2}` || display.hint != "needle" {
		t.Fatalf("display = %#v", display)
	}
	if len(display.locations) != 1 || display.locations[0].Path != "pkg/main.go" {
		t.Fatalf("locations = %#v", display.locations)
	}
}

func TestConvertMessagesUsesSameCompactPresentation(t *testing.T) {
	messages := convertMessages([]agent.Message{{
		Role: agent.RoleAssistant,
		Content: []agent.Content{
			{ToolCall: &agent.ToolCall{
				ID: "read-1", Name: "read", Args: `{"file_path":"main.go"}`,
			}},
			{ToolResult: &agent.ToolResult{
				ID: "read-1", Name: "read", Args: `{"file_path":"main.go"}`, Content: "contents",
			}},
		},
	}})
	call := messages[0].Content[0].ToolCall
	result := messages[0].Content[1].ToolResult
	if call == nil || result == nil || call.Name != "Read file" || result.Name != "Read file" {
		t.Fatalf("messages = %#v", messages)
	}
	if call.Args != "" || result.Args != "" || len(call.Locations) != 1 || len(result.Locations) != 1 {
		t.Fatalf("call = %#v, result = %#v", call, result)
	}
}

func TestConvertMessagesPromotesPrimaryArgumentInHistory(t *testing.T) {
	messages := convertMessages([]agent.Message{{
		Role: agent.RoleAssistant,
		Content: []agent.Content{{ToolCall: &agent.ToolCall{
			ID: "glob-1", Name: "Find files", Kind: "search",
			Args: `{"pattern":"README*","limit":20}`,
		}}},
	}})
	call := messages[0].Content[0].ToolCall
	if call == nil || call.Name != "Find files" || call.Hint != "README*" || call.Args != `{"limit":20}` {
		t.Fatalf("call = %#v", call)
	}
}
