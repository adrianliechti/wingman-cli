package claude

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

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

	case "Edit", "MultiEdit":
		var in struct {
			FilePath  string `json:"file_path"`
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
			Edits     []struct {
				OldString string `json:"old_string"`
				NewString string `json:"new_string"`
			} `json:"edits"`
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
			for _, edit := range in.Edits {
				content = append(content, acp.ToolDiffContent(in.FilePath, edit.NewString, edit.OldString))
			}
			locations = displayLocation(in.FilePath, cwd, 0)
		}
		if len(content) > 0 {
			display = displayInput(rawInput, "file_path", "old_string", "new_string", "edits")
		} else {
			// MultiEdit carries an edits array which is not representable as one
			// ACP diff, so retain it while still removing the duplicate path.
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

	case "BashOutput":
		return toolInfo{title: "Read command output", kind: acp.ToolKindExecute, rawInput: displayInput(rawInput)}

	case "KillShell":
		return toolInfo{title: "Stop command", kind: acp.ToolKindExecute, rawInput: displayInput(rawInput)}

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

// taskPlan mirrors the CLI's TaskCreate/TaskUpdate task list as ACP plan
// entries. The task id is only revealed in TaskCreate's result text
// ("Task #1 created successfully: …"), so creates resolve in two steps.
type taskPlan struct {
	mu             sync.Mutex
	pendingCreates map[string]string
	pendingUpdates map[string]taskPlanUpdate
	order          []string
	tasks          map[string]*acp.PlanEntry
}

func newTaskPlan() *taskPlan {
	return &taskPlan{
		pendingCreates: map[string]string{},
		pendingUpdates: map[string]taskPlanUpdate{},
		tasks:          map[string]*acp.PlanEntry{},
	}
}

type taskPlanUpdate struct {
	taskID  string
	status  string
	subject string
}

var taskIDPattern = regexp.MustCompile(`Task #([\w-]+)`)
var taskListPattern = regexp.MustCompile(`^#(\S+) \[(pending|in_progress|completed)\] (.+?)(?: \([^()]*\))?(?: \[blocked by .+\])?$`)

func (p *taskPlan) noteCreate(toolUseID string, input json.RawMessage) {
	var in struct {
		Subject string `json:"subject"`
	}
	_ = json.Unmarshal(input, &in)
	if toolUseID != "" && in.Subject != "" {
		p.mu.Lock()
		p.pendingCreates[toolUseID] = in.Subject
		p.mu.Unlock()
	}
}

func (p *taskPlan) completeCreate(toolUseID, resultText string, isError bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	subject, ok := p.pendingCreates[toolUseID]
	if !ok {
		return false
	}
	delete(p.pendingCreates, toolUseID)
	if isError {
		return false
	}
	m := taskIDPattern.FindStringSubmatch(resultText)
	if m == nil {
		return false
	}
	id := m[1]
	if _, exists := p.tasks[id]; !exists {
		p.order = append(p.order, id)
	}
	p.tasks[id] = &acp.PlanEntry{Content: subject, Priority: acp.PlanEntryPriorityMedium, Status: acp.PlanEntryStatusPending}
	return true
}

func (p *taskPlan) noteUpdate(toolUseID string, input json.RawMessage) {
	var in struct {
		TaskID  string `json:"taskId"`
		Status  string `json:"status"`
		Subject string `json:"subject"`
	}
	_ = json.Unmarshal(input, &in)
	if toolUseID == "" || in.TaskID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.tasks[in.TaskID]; !ok {
		return
	}
	p.pendingUpdates[toolUseID] = taskPlanUpdate{taskID: in.TaskID, status: in.Status, subject: in.Subject}
}

func (p *taskPlan) completeUpdate(toolUseID, resultText string, isError bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	update, ok := p.pendingUpdates[toolUseID]
	if !ok {
		return false
	}
	delete(p.pendingUpdates, toolUseID)
	if isError || taskUpdateFailed(resultText, update.taskID) {
		return false
	}
	entry, ok := p.tasks[update.taskID]
	if !ok {
		return false
	}
	if update.status == "deleted" {
		delete(p.tasks, update.taskID)
		for i, id := range p.order {
			if id == update.taskID {
				p.order = append(p.order[:i], p.order[i+1:]...)
				break
			}
		}
		return true
	}
	changed := false
	if update.subject != "" && update.subject != entry.Content {
		entry.Content = update.subject
		changed = true
	}
	var status acp.PlanEntryStatus
	switch update.status {
	case "pending":
		status = acp.PlanEntryStatusPending
	case "in_progress":
		status = acp.PlanEntryStatusInProgress
	case "completed":
		status = acp.PlanEntryStatusCompleted
	}
	if status != "" && status != entry.Status {
		entry.Status = status
		changed = true
	}
	return changed
}

func taskUpdateFailed(resultText, taskID string) bool {
	text := strings.TrimSpace(resultText)
	if strings.EqualFold(text, "Failed to delete task") || strings.Contains(strings.ToLower(text), "task #"+strings.ToLower(taskID)+" not found") {
		return true
	}
	var result struct {
		Success *bool  `json:"success"`
		TaskID  string `json:"taskId"`
	}
	if json.Unmarshal([]byte(text), &result) == nil && result.Success != nil {
		return !*result.Success || (result.TaskID != "" && result.TaskID != taskID)
	}
	return false
}

func (p *taskPlan) applyTaskList(resultText string, isError bool) bool {
	if isError {
		return false
	}
	tasks, ok := parseTaskList(resultText)
	if !ok {
		return false
	}
	p.mu.Lock()
	p.order = p.order[:0]
	clear(p.tasks)
	for _, task := range tasks {
		p.order = append(p.order, task.id)
		p.tasks[task.id] = &acp.PlanEntry{
			Content:  task.subject,
			Status:   planStatus(task.status),
			Priority: acp.PlanEntryPriorityMedium,
		}
	}
	p.mu.Unlock()
	return true
}

type taskListEntry struct {
	id      string
	subject string
	status  string
}

func parseTaskList(resultText string) ([]taskListEntry, bool) {
	text := strings.TrimSpace(resultText)
	var structured struct {
		Tasks []struct {
			ID      string `json:"id"`
			Subject string `json:"subject"`
			Status  string `json:"status"`
		} `json:"tasks"`
	}
	if json.Unmarshal([]byte(text), &structured) == nil && structured.Tasks != nil {
		out := make([]taskListEntry, 0, len(structured.Tasks))
		for _, task := range structured.Tasks {
			if task.ID == "" || task.Subject == "" || !validTaskStatus(task.Status) {
				return nil, false
			}
			out = append(out, taskListEntry{id: task.ID, subject: task.Subject, status: task.Status})
		}
		return out, true
	}
	if text == "No tasks found" {
		return []taskListEntry{}, true
	}
	var out []taskListEntry
	for line := range strings.SplitSeq(text, "\n") {
		match := taskListPattern.FindStringSubmatch(line)
		if match == nil {
			return nil, false
		}
		out = append(out, taskListEntry{id: match[1], status: match[2], subject: match[3]})
	}
	return out, len(out) > 0
}

func validTaskStatus(status string) bool {
	return status == "pending" || status == "in_progress" || status == "completed"
}

func (p *taskPlan) entries() []acp.PlanEntry {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.entriesLocked()
}

func (p *taskPlan) unfinishedEntries() ([]acp.PlanEntry, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	unfinished := false
	for _, entry := range p.tasks {
		if entry.Status != acp.PlanEntryStatusCompleted {
			unfinished = true
			break
		}
	}
	return p.entriesLocked(), unfinished
}

func (p *taskPlan) clear() {
	p.mu.Lock()
	clear(p.pendingCreates)
	clear(p.pendingUpdates)
	p.order = nil
	clear(p.tasks)
	p.mu.Unlock()
}

func (p *taskPlan) entriesLocked() []acp.PlanEntry {
	out := make([]acp.PlanEntry, 0, len(p.order))
	for _, id := range p.order {
		out = append(out, *p.tasks[id])
	}
	return out
}

func isPlanTool(name string) bool { return name == "TodoWrite" }

// shouldEagerlyEmitToolCall reports whether a permission request may publish
// the tool before its assistant tool_use arrives. TodoWrite renders as a plan
// update, while Task/Agent wait for the assistant event so subagent calls never
// appear merely because a permission prompt raced ahead of the model output.
func shouldEagerlyEmitToolCall(name string) bool {
	return !isPlanTool(name) && name != "Agent" && name != "Task"
}

func shouldTrackToolCall(name string) bool {
	return !isPlanTool(name)
}

func planEntriesFromTodoWrite(rawInput json.RawMessage) (entries []acp.PlanEntry, ok bool) {
	var in struct {
		Todos []struct {
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"todos"`
	}
	if err := json.Unmarshal(rawInput, &in); err != nil || in.Todos == nil {
		return nil, false
	}
	entries = make([]acp.PlanEntry, 0, len(in.Todos))
	for _, t := range in.Todos {
		entries = append(entries, acp.PlanEntry{
			Content:  t.Content,
			Status:   planStatus(t.Status),
			Priority: acp.PlanEntryPriorityMedium,
		})
	}
	return entries, true
}

func planStatus(s string) acp.PlanEntryStatus {
	switch s {
	case "in_progress":
		return acp.PlanEntryStatusInProgress
	case "completed":
		return acp.PlanEntryStatusCompleted
	default:
		return acp.PlanEntryStatusPending
	}
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
