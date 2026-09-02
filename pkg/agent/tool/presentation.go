package tool

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Presentation is the display-ready form of a tool call. Tool producers use
// Present before handing a call to a UI so renderers do not need to infer tool
// semantics from implementation names and raw JSON.
type Presentation struct {
	Title string
	Kind  string
	Args  string
	Hint  string
	Path  string
	Line  int
}

type presentationSpec struct {
	names       []string
	title       string
	kind        string
	omit        []string
	primary     []string
	listPrimary string
	path        []string
	line        string
	derivedHint string
}

var presentationSpecs = indexPresentationSpecs([]presentationSpec{
	{
		names: []string{"read", "Read file"},
		title: "Read file", kind: "read",
		omit: []string{"file_path", "path", "offset"},
		path: []string{"file_path", "path"}, line: "offset",
	},
	{
		names: []string{"write", "Write file"},
		title: "Write file", kind: "edit",
		omit: []string{"file_path", "path", "content"},
		path: []string{"file_path", "path"},
	},
	{
		names: []string{"edit", "Edit file"},
		title: "Edit file", kind: "edit",
		omit: []string{
			"file_path", "path", "old_string", "new_string", "oldText", "newText",
			"replace_all", "replaceAll", "edits",
		},
		path: []string{"file_path", "path"},
	},
	{
		names: []string{"apply_patch"},
		title: "Apply patch", kind: "edit",
	},
	{
		names: []string{"grep", "Search files"},
		title: "Search files", kind: "search", omit: []string{"path"},
		primary: []string{"query", "pattern"}, path: []string{"path"},
	},
	{
		names: []string{"glob", "Find files"},
		title: "Find files", kind: "search", omit: []string{"path"},
		primary: []string{"pattern", "query"}, path: []string{"path"},
	},
	{
		names: []string{"lsp", "Inspect code"},
		title: "Inspect code", kind: "read", omit: []string{"file_path", "line"},
		primary: []string{"query", "symbol", "operation"},
		path:    []string{"file_path"}, line: "line",
	},
	{
		names: []string{"code_graph", "Inspect code graph"},
		title: "Inspect code graph", kind: "read", omit: []string{"file"},
		primary: []string{"query", "symbol", "operation"}, path: []string{"file"},
	},
	{
		names: []string{"view_image", "View image"},
		title: "View image", kind: "read", omit: []string{"file_path", "path"},
		path: []string{"file_path", "path"},
	},
	{
		names: []string{"shell", "exec_command", "Run command"},
		title: "Run command", kind: "execute", primary: []string{"command", "cmd"},
	},
	{
		names: []string{"exec_session", "Continue command"},
		title: "Continue command", kind: "execute",
		omit: []string{"session_id", "input"}, derivedHint: "extract",
	},
	{
		names: []string{"fetch", "Open page"},
		title: "Open page", kind: "fetch", primary: []string{"url"},
	},
	{
		names: []string{"web_search", "Web search"},
		title: "Web search", kind: "fetch", primary: []string{"query", "pattern"},
		listPrimary: "queries",
	},
	{
		names: []string{"Find in page"}, title: "Find in page",
		primary: []string{"pattern"},
	},
	{
		names: []string{"agent", "spawn_agent", "Delegate task"},
		title: "Delegate task", primary: []string{"description", "prompt"},
	},
	{
		names: []string{"followup_task", "Follow up with agent"},
		title: "Follow up with agent", primary: []string{"message"},
	},
	{
		names: []string{"send_message", "Message agent"},
		title: "Message agent", primary: []string{"message"},
	},
	{
		names: []string{"task_output", "Read agent output"},
		title: "Read agent output", omit: []string{"id"}, derivedHint: "extract",
	},
	{
		names: []string{"task_send"},
		title: "Follow up with agent", omit: []string{"id", "message"}, derivedHint: "extract",
	},
	{
		names: []string{"task_stop", "Stop agent"},
		title: "Stop agent", omit: []string{"id"}, derivedHint: "extract",
	},
	{names: []string{"wait_agent", "Wait for agents"}, title: "Wait for agents"},
	{names: []string{"list_agents", "List agents"}, title: "List agents"},
	{
		names: []string{"interrupt_agent", "Interrupt agent"},
		title: "Interrupt agent", primary: []string{"target"},
	},
	{
		names: []string{"elicit", "request_user_input", "Request input"},
		title: "Request input", derivedHint: "elicit",
	},
	{
		names: []string{"create_goal", "Create goal"},
		title: "Create goal", primary: []string{"objective"},
	},
	{
		names: []string{"update_goal", "Update goal"},
		title: "Update goal", primary: []string{"status"},
	},
	{names: []string{"get_goal", "Read goal"}, title: "Read goal"},
	{
		names: []string{"image_gen__imagegen", "Generate image"},
		title: "Generate image", primary: []string{"prompt"},
	},
	{
		names: []string{"Load skill"}, title: "Load skill",
		primary: []string{"skill", "name"},
	},
	{
		names: []string{"schedule_task", "Schedule task"},
		title: "Schedule task", primary: []string{"name", "prompt"},
	},
	{names: []string{"list_tasks", "List scheduled tasks"}, title: "List scheduled tasks"},
	{
		names: []string{"pause_task", "Pause scheduled task"},
		title: "Pause scheduled task", primary: []string{"id"},
	},
	{
		names: []string{"resume_task", "Resume scheduled task"},
		title: "Resume scheduled task", primary: []string{"id"},
	},
	{
		names: []string{"remove_task", "Remove scheduled task"},
		title: "Remove scheduled task", primary: []string{"id"},
	},
	{
		names: []string{"report", "Report result"},
		title: "Report result", omit: []string{"result"},
	},
})

func indexPresentationSpecs(specs []presentationSpec) map[string]presentationSpec {
	index := make(map[string]presentationSpec)
	for _, spec := range specs {
		for _, name := range spec.names {
			index[name] = spec
		}
	}
	return index
}

var defaultPrimaryArgs = []string{
	"description", "query", "pattern", "command", "prompt", "question", "url", "name",
}

// Present turns execution-oriented tool data into a compact presentation.
// hasLocation means the producer already supplied a richer location and only
// needs path arguments removed from the visible input.
func Present(name, kind, argsJSON string, hasLocation bool) Presentation {
	spec := presentationSpecs[name]
	title := spec.title
	if title == "" {
		title = name
	}
	if kind == "" {
		kind = spec.kind
	}
	p := Presentation{Title: title, Kind: kind, Args: argsJSON}

	var args map[string]any
	if strings.TrimSpace(argsJSON) == "" {
		args = map[string]any{}
	} else if json.Unmarshal([]byte(argsJSON), &args) != nil {
		p.Hint = ExtractHint(argsJSON, name)
		return p
	}

	if !hasLocation {
		p.Path, p.Line = presentationLocation(spec, args)
		hasLocation = p.Path != ""
	}
	omit := spec.omit
	if hasLocation && len(omit) == 0 {
		omit = []string{"file_path", "path", "file"}
	}
	for _, key := range omit {
		delete(args, key)
	}

	primary := spec.primary
	if len(primary) == 0 {
		primary = defaultPrimaryArgs
	}
	for _, key := range primary {
		if value := presentationHintValue(args[key]); value != "" {
			p.Hint = value
			delete(args, key)
			break
		}
	}
	if p.Hint == "" && spec.listPrimary != "" {
		if value := presentationHintValue(args[spec.listPrimary]); value != "" {
			p.Hint = value
			delete(args, spec.listPrimary)
		}
	}
	if p.Hint == "" {
		switch spec.derivedHint {
		case "elicit":
			p.Hint = ElicitHint(args)
		case "extract":
			p.Hint = ExtractHint(argsJSON, name)
		}
	}

	if len(args) == 0 {
		p.Args = ""
	} else if raw, err := json.Marshal(args); err == nil {
		p.Args = string(raw)
	}
	return p
}

func presentationHintValue(value any) string {
	if text, ok := value.(string); ok {
		return strings.Join(strings.Fields(text), " ")
	}
	values, ok := value.([]any)
	if !ok {
		return ""
	}
	var labels []string
	for _, value := range values {
		text, _ := value.(string)
		if text = strings.Join(strings.Fields(text), " "); text != "" {
			labels = append(labels, text)
		}
	}
	if len(labels) > 2 {
		return strings.Join(labels[:2], " · ") + " +" + strconv.Itoa(len(labels)-2)
	}
	return strings.Join(labels, " · ")
}

func presentationLocation(spec presentationSpec, args map[string]any) (string, int) {
	var path string
	for _, key := range spec.path {
		path, _ = args[key].(string)
		if path != "" {
			break
		}
	}
	if strings.TrimSpace(path) == "" {
		return "", 0
	}
	line, _ := IntArg(args, spec.line)
	return path, line
}
