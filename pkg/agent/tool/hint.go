package tool

import (
	"encoding/json"
	"fmt"
	"strings"
)

var fsTools = map[string]bool{
	"read": true, "write": true, "edit": true,
	"grep": true, "glob": true,
}

var workingDirTools = map[string]bool{
	"grep": true, "glob": true,
}

func ExtractHint(argsJSON, toolName string) string {
	if toolName == "todo" {
		return TodoHint(argsJSON)
	}

	args, ok := parseArgs(argsJSON)
	if !ok {
		return wdFallback(toolName)
	}

	if desc, ok := args["description"]; ok {
		if str, ok := desc.(string); ok && str != "" {
			label := strings.Join(strings.Fields(str), " ")

			if toolName == "agent" {
				if at, ok := args["agent_type"].(string); ok && at != "" {
					label += " (" + strings.TrimSpace(at) + ")"
				}
			}
			return label
		}
	}

	if toolName == "exec_session" {
		if id, ok := IntArg(args, "session_id"); ok {
			hint := fmt.Sprintf("session %d", id)
			if kill, _ := args["kill"].(bool); kill {
				return hint + " (kill)"
			}
			if input, _ := args["input"].(string); input != "" {
				return hint + ": " + strings.Join(strings.Fields(input), " ")
			}
			return hint
		}
	}

	if toolName == "elicit" {
		if hint := ElicitHint(args); hint != "" {
			return hint
		}
	}

	if toolName == "task_send" || toolName == "task_stop" || toolName == "task_output" {
		id, _ := args["id"].(string)
		id = strings.TrimSpace(id)
		if message, _ := args["message"].(string); message != "" {
			message = strings.Join(strings.Fields(message), " ")
			if id != "" {
				return id + ": " + message
			}
			return message
		}
		return id
	}

	hintKeys := []string{
		"query",
		"pattern",
		"command",
		"prompt",
		"question",
		"file_path",
		"path",
		"file",
		"url",
		"name",
	}

	for _, key := range hintKeys {
		val, ok := args[key]
		if !ok {
			continue
		}
		str, ok := val.(string)
		if !ok || str == "" {
			continue
		}
		normalized := strings.Join(strings.Fields(str), " ")
		if (key == "file_path" || key == "path" || key == "file") && fsTools[toolName] {
			normalized = normalizeWorkspacePath(normalized)
		}
		return normalized
	}

	return wdFallback(toolName)
}

func parseArgs(argsJSON string) (map[string]any, bool) {
	var args map[string]any
	if json.Unmarshal([]byte(argsJSON), &args) == nil {
		return args, true
	}
	if json.Unmarshal([]byte(closeJSONPrefix(argsJSON)), &args) != nil {
		return nil, false
	}
	return args, true
}

type TodoItem struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

// ParseTodoItems extracts a todo checklist from complete or streaming arguments.
func ParseTodoItems(argsJSON string) []TodoItem {
	var args struct {
		Items []TodoItem `json:"items"`
		Plan  []struct {
			Step   string `json:"step"`
			Status string `json:"status"`
		} `json:"plan"`
	}
	if json.Unmarshal([]byte(argsJSON), &args) != nil {
		if json.Unmarshal([]byte(closeJSONPrefix(argsJSON)), &args) != nil {
			return nil
		}
	}

	items := args.Items
	if len(items) == 0 {
		items = make([]TodoItem, 0, len(args.Plan))
		for _, item := range args.Plan {
			items = append(items, TodoItem{Content: item.Step, Status: item.Status})
		}
	}
	filtered := items[:0]
	for _, item := range items {
		if strings.TrimSpace(item.Content) != "" {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

// TodoHint summarizes a todo call as progress plus the active step.
func TodoHint(argsJSON string) string {
	items := ParseTodoItems(argsJSON)
	if len(items) == 0 {
		return ""
	}

	completed := 0
	current := ""
	for _, item := range items {
		if item.Status == "completed" {
			completed++
		}
		if item.Status == "in_progress" && current == "" {
			current = strings.Join(strings.Fields(item.Content), " ")
		}
	}

	hint := fmt.Sprintf("%d/%d", completed, len(items))
	if current != "" {
		hint += " · " + current
	}
	return hint
}

// ElicitHint summarizes an elicit tool call as its first question plus a
// count of the rest; shared by every surface that labels tool calls.
func ElicitHint(args map[string]any) string {
	questions, ok := args["questions"].([]any)
	if !ok || len(questions) == 0 {
		return ""
	}

	entry, ok := questions[0].(map[string]any)
	if !ok {
		return ""
	}

	text, _ := entry["question"].(string)
	if text == "" {
		return ""
	}

	label := strings.Join(strings.Fields(text), " ")
	if len(questions) > 1 {
		label += fmt.Sprintf(" (+%d more)", len(questions)-1)
	}
	return label
}

func normalizeWorkspacePath(p string) string {
	if p == "" || p == "." || p == "./" {
		return "/"
	}
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~") {
		return p
	}
	return "/" + p
}

func wdFallback(toolName string) string {
	if workingDirTools[toolName] {
		return "/"
	}
	return ""
}
