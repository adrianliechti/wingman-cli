package claude

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/coder/acp-go-sdk"
)

type toolUseCache map[string]string

type toolInfo struct {
	title     string
	kind      acp.ToolKind
	rawInput  any
	locations []acp.ToolCallLocation
	content   []acp.ToolCallContent
}

// toolCallStartUpdate builds the tool_call notification for a tool_use. It's
// shared by the two sites that can be first to surface one — the streamed
// tool_use block (events.go) and the eager permission-request path
// (approvals.go) — so they can't drift apart.
func toolCallStartUpdate(id, name string, rawInput json.RawMessage, cwd string, status acp.ToolCallStatus) acp.SessionUpdate {
	info := toolInfoFromToolUse(name, rawInput, cwd)
	opts := []acp.ToolCallStartOpt{
		acp.WithStartKind(info.kind),
		acp.WithStartStatus(status),
	}
	if len(info.content) > 0 {
		opts = append(opts, acp.WithStartContent(info.content))
	}
	if len(info.locations) > 0 {
		opts = append(opts, acp.WithStartLocations(info.locations))
	}
	// WithStartLocations mirrors a lone location into rawInput.path. Apply the
	// display input afterwards so a file chip never becomes a duplicate hint or
	// expanded path argument.
	if info.rawInput != nil || len(info.locations) > 0 {
		opts = append(opts, acp.WithStartRawInput(info.rawInput))
	}
	return acp.StartToolCall(acp.ToolCallId(id), info.title, opts...)
}

// toolCallRefineUpdate builds the tool_call_update that refines a tool_call
// already emitted by the other path with the now-complete info, instead of
// duplicating it. See toolCallTracker.
func toolCallRefineUpdate(id, name string, rawInput json.RawMessage, cwd string, status acp.ToolCallStatus) acp.SessionUpdate {
	info := toolInfoFromToolUse(name, rawInput, cwd)
	opts := []acp.ToolCallUpdateOpt{
		acp.WithUpdateTitle(info.title),
		acp.WithUpdateKind(info.kind),
		acp.WithUpdateStatus(status),
	}
	if info.rawInput != nil {
		opts = append(opts, acp.WithUpdateRawInput(info.rawInput))
	}
	if len(info.content) > 0 {
		opts = append(opts, acp.WithUpdateContent(info.content))
	}
	if len(info.locations) > 0 {
		opts = append(opts, acp.WithUpdateLocations(info.locations))
	}
	return acp.UpdateToolCall(acp.ToolCallId(id), opts...)
}

func unmarshalAny(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, false
	}
	return v, true
}

func displayInput(raw json.RawMessage, omit ...string) any {
	input, ok := unmarshalAny(raw)
	if !ok {
		return nil
	}
	args, ok := input.(map[string]any)
	if !ok {
		return input
	}
	for _, key := range omit {
		delete(args, key)
	}
	if len(args) == 0 {
		return nil
	}
	return args
}

func displayLocation(path, cwd string, line int) []acp.ToolCallLocation {
	if path == "" {
		return nil
	}
	if !filepath.IsAbs(path) && cwd != "" {
		path = filepath.Join(cwd, path)
	}
	location := acp.ToolCallLocation{Path: path}
	if line > 0 {
		location.Line = new(line)
	}
	return []acp.ToolCallLocation{location}
}

func toolInfoFromToolUse(name string, rawInput json.RawMessage, cwd string) toolInfo {
	switch name {
	case "Agent", "Task":
		var in struct {
			Description string `json:"description"`
			Prompt      string `json:"prompt"`
		}
		_ = json.Unmarshal(rawInput, &in)
		title := "Task"
		if in.Description != "" {
			title = in.Description
		}
		var content []acp.ToolCallContent
		if in.Prompt != "" {
			content = []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(in.Prompt))}
		}
		return toolInfo{title: title, kind: acp.ToolKindThink, content: content}

	case "Bash":
		var in struct {
			Command     string `json:"command"`
			Description string `json:"description"`
		}
		_ = json.Unmarshal(rawInput, &in)
		var content []acp.ToolCallContent
		if in.Description != "" {
			content = []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(in.Description))}
		}
		return toolInfo{
			title: "Run command", kind: acp.ToolKindExecute,
			rawInput: displayInput(rawInput, "description"), content: content,
		}

	case "Read":
		var in struct {
			FilePath string `json:"file_path"`
			Offset   int    `json:"offset"`
			Limit    int    `json:"limit"`
		}
		_ = json.Unmarshal(rawInput, &in)
		return toolInfo{
			title: "Read file", kind: acp.ToolKindRead,
			rawInput:  displayInput(rawInput, "file_path", "offset"),
			locations: displayLocation(in.FilePath, cwd, in.Offset),
		}

	case "Write":
		var in struct {
			FilePath string `json:"file_path"`
			Content  string `json:"content"`
		}
		_ = json.Unmarshal(rawInput, &in)
		var content []acp.ToolCallContent
		var locations []acp.ToolCallLocation
		if in.FilePath != "" {
			content = []acp.ToolCallContent{acp.ToolDiffContent(in.FilePath, in.Content)}
			locations = displayLocation(in.FilePath, cwd, 0)
		} else if in.Content != "" {
			content = []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(in.Content))}
		}
		return toolInfo{title: "Write file", kind: acp.ToolKindEdit, content: content, locations: locations}

	case "Edit":
		var in struct {
			FilePath  string `json:"file_path"`
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
		}
		_ = json.Unmarshal(rawInput, &in)
		var content []acp.ToolCallContent
		var locations []acp.ToolCallLocation
		var display any
		if in.FilePath != "" {
			if in.OldString != "" {
				content = []acp.ToolCallContent{acp.ToolDiffContent(in.FilePath, in.NewString, in.OldString)}
			} else if in.NewString != "" {
				content = []acp.ToolCallContent{acp.ToolDiffContent(in.FilePath, in.NewString)}
			}
			locations = displayLocation(in.FilePath, cwd, 0)
		}
		if len(content) > 0 {
			display = displayInput(rawInput, "file_path", "old_string", "new_string")
		} else {
			display = displayInput(rawInput, "file_path")
		}
		return toolInfo{title: "Edit file", kind: acp.ToolKindEdit, rawInput: display, content: content, locations: locations}

	case "NotebookEdit":
		var in struct {
			NotebookPath string `json:"notebook_path"`
		}
		_ = json.Unmarshal(rawInput, &in)
		return toolInfo{
			title: "Edit notebook", kind: acp.ToolKindEdit,
			rawInput:  displayInput(rawInput, "notebook_path"),
			locations: displayLocation(in.NotebookPath, cwd, 0),
		}

	case "Glob":
		var in struct {
			Path    string `json:"path"`
			Pattern string `json:"pattern"`
		}
		_ = json.Unmarshal(rawInput, &in)
		return toolInfo{
			title: "Find files", kind: acp.ToolKindSearch,
			rawInput:  displayInput(rawInput, "path"),
			locations: displayLocation(in.Path, cwd, 0),
		}

	case "Grep":
		var in struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(rawInput, &in)
		return toolInfo{
			title: "Search files", kind: acp.ToolKindSearch,
			rawInput:  displayInput(rawInput, "path"),
			locations: displayLocation(in.Path, cwd, 0),
		}

	case "WebFetch":
		var in struct {
			URL    string `json:"url"`
			Prompt string `json:"prompt"`
		}
		_ = json.Unmarshal(rawInput, &in)
		var content []acp.ToolCallContent
		if in.Prompt != "" {
			content = []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(in.Prompt))}
		}
		return toolInfo{
			title: "Open page", kind: acp.ToolKindFetch,
			rawInput: displayInput(rawInput, "prompt"), content: content,
		}

	case "WebSearch":
		var in struct {
			Query          string   `json:"query"`
			AllowedDomains []string `json:"allowed_domains"`
			BlockedDomains []string `json:"blocked_domains"`
		}
		_ = json.Unmarshal(rawInput, &in)
		return toolInfo{title: "Web search", kind: acp.ToolKindFetch, rawInput: displayInput(rawInput)}

	case "TaskOutput":
		return toolInfo{title: "Read command output", kind: acp.ToolKindExecute, rawInput: displayInput(rawInput)}

	case "TaskStop":
		return toolInfo{title: "Stop command", kind: acp.ToolKindExecute, rawInput: displayInput(rawInput)}

	case "EnterPlanMode":
		return toolInfo{title: "Planning", kind: acp.ToolKindSwitchMode, rawInput: displayInput(rawInput)}

	case "ExitPlanMode":
		var in struct {
			Plan string `json:"plan"`
		}
		_ = json.Unmarshal(rawInput, &in)
		var content []acp.ToolCallContent
		if in.Plan != "" {
			content = []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(in.Plan))}
		}
		return toolInfo{title: "Ready to code?", kind: acp.ToolKindSwitchMode, content: content}

	case "Skill":
		return toolInfo{title: "Load skill", kind: acp.ToolKindOther, rawInput: displayInput(rawInput)}

	case "AskUserQuestion":
		return toolInfo{title: "Request input", kind: acp.ToolKindOther}

	default:
		title := name
		if title == "" {
			title = "Tool call"
		}
		return toolInfo{title: title, kind: toolKindFor(name), rawInput: displayInput(rawInput)}
	}
}

// shouldEagerlyEmitToolCall reports whether a permission request may publish
// the tool before its assistant tool_use arrives. Task/Agent wait for the
// assistant event so subagent calls never appear merely because a permission
// prompt raced ahead of the model output.
func shouldEagerlyEmitToolCall(name string) bool {
	return name != "Agent" && name != "Task"
}

var fencePattern = regexp.MustCompile("(?m)^`{3,}")

func markdownEscape(text string) string {
	fence := "```"
	for _, m := range fencePattern.FindAllString(text, -1) {
		for len(m) >= len(fence) {
			fence += "`"
		}
	}
	suffix := ""
	if !strings.HasSuffix(text, "\n") {
		suffix = "\n"
	}
	return fence + "\n" + text + suffix + fence
}
