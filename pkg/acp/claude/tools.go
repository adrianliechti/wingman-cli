package claude

import (
	"encoding/json"
	"fmt"
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
	if input, ok := unmarshalAny(rawInput); ok {
		opts = append(opts, acp.WithStartRawInput(input))
	}
	if len(info.content) > 0 {
		opts = append(opts, acp.WithStartContent(info.content))
	}
	if len(info.locations) > 0 {
		opts = append(opts, acp.WithStartLocations(info.locations))
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
	if input, ok := unmarshalAny(rawInput); ok {
		opts = append(opts, acp.WithUpdateRawInput(input))
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

func toDisplayPath(filePath, cwd string) string {
	if cwd == "" || filePath == "" {
		return filePath
	}
	rc, err1 := filepath.Abs(cwd)
	rf, err2 := filepath.Abs(filePath)
	if err1 != nil || err2 != nil {
		return filePath
	}
	if rf == rc || strings.HasPrefix(rf, rc+string(filepath.Separator)) {
		if rel, err := filepath.Rel(rc, rf); err == nil {
			return rel
		}
	}
	return filePath
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
		title := "Terminal"
		if in.Command != "" {
			title = in.Command
		}
		var content []acp.ToolCallContent
		if in.Description != "" {
			content = []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(in.Description))}
		}
		return toolInfo{title: title, kind: acp.ToolKindExecute, content: content}

	case "Read":
		var in struct {
			FilePath string `json:"file_path"`
			Offset   int    `json:"offset"`
			Limit    int    `json:"limit"`
		}
		_ = json.Unmarshal(rawInput, &in)
		span := ""
		switch {
		case in.Limit > 0:
			start := in.Offset
			if start == 0 {
				start = 1
			}
			span = fmt.Sprintf(" (%d - %d)", start, start+in.Limit-1)
		case in.Offset != 0:
			span = fmt.Sprintf(" (from line %d)", in.Offset)
		}
		display := "File"
		var locations []acp.ToolCallLocation
		if in.FilePath != "" {
			display = toDisplayPath(in.FilePath, cwd)
			line := in.Offset
			if line == 0 {
				line = 1
			}
			locations = []acp.ToolCallLocation{{Path: in.FilePath, Line: new(line)}}
		}
		return toolInfo{title: "Read " + display + span, kind: acp.ToolKindRead, locations: locations}

	case "Write":
		var in struct {
			FilePath string `json:"file_path"`
			Content  string `json:"content"`
		}
		_ = json.Unmarshal(rawInput, &in)
		var content []acp.ToolCallContent
		var locations []acp.ToolCallLocation
		title := "Write"
		if in.FilePath != "" {
			content = []acp.ToolCallContent{acp.ToolDiffContent(in.FilePath, in.Content)}
			locations = []acp.ToolCallLocation{{Path: in.FilePath}}
			title = "Write " + toDisplayPath(in.FilePath, cwd)
		} else if in.Content != "" {
			content = []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(in.Content))}
		}
		return toolInfo{title: title, kind: acp.ToolKindEdit, content: content, locations: locations}

	case "Edit", "MultiEdit":
		var in struct {
			FilePath  string `json:"file_path"`
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
		}
		_ = json.Unmarshal(rawInput, &in)
		var content []acp.ToolCallContent
		var locations []acp.ToolCallLocation
		title := "Edit"
		if in.FilePath != "" {
			if in.OldString != "" {
				content = []acp.ToolCallContent{acp.ToolDiffContent(in.FilePath, in.NewString, in.OldString)}
			} else if in.NewString != "" {
				content = []acp.ToolCallContent{acp.ToolDiffContent(in.FilePath, in.NewString)}
			}
			locations = []acp.ToolCallLocation{{Path: in.FilePath}}
			title = "Edit " + toDisplayPath(in.FilePath, cwd)
		}
		return toolInfo{title: title, kind: acp.ToolKindEdit, content: content, locations: locations}

	case "Glob":
		var in struct {
			Path    string `json:"path"`
			Pattern string `json:"pattern"`
		}
		_ = json.Unmarshal(rawInput, &in)
		label := "Find"
		if in.Path != "" {
			label += " `" + in.Path + "`"
		}
		if in.Pattern != "" {
			label += " `" + in.Pattern + "`"
		}
		var locations []acp.ToolCallLocation
		if in.Path != "" {
			locations = []acp.ToolCallLocation{{Path: in.Path}}
		}
		return toolInfo{title: label, kind: acp.ToolKindSearch, locations: locations}

	case "Grep":
		return toolInfo{title: grepLabel(rawInput), kind: acp.ToolKindSearch}

	case "WebFetch":
		var in struct {
			URL    string `json:"url"`
			Prompt string `json:"prompt"`
		}
		_ = json.Unmarshal(rawInput, &in)
		title := "Fetch"
		if in.URL != "" {
			title = "Fetch " + in.URL
		}
		var content []acp.ToolCallContent
		if in.Prompt != "" {
			content = []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(in.Prompt))}
		}
		return toolInfo{title: title, kind: acp.ToolKindFetch, content: content}

	case "WebSearch":
		var in struct {
			Query          string   `json:"query"`
			AllowedDomains []string `json:"allowed_domains"`
			BlockedDomains []string `json:"blocked_domains"`
		}
		_ = json.Unmarshal(rawInput, &in)
		label := "Web search"
		if in.Query != "" {
			label = `"` + in.Query + `"`
		}
		if len(in.AllowedDomains) > 0 {
			label += " (allowed: " + strings.Join(in.AllowedDomains, ", ") + ")"
		}
		if len(in.BlockedDomains) > 0 {
			label += " (blocked: " + strings.Join(in.BlockedDomains, ", ") + ")"
		}
		return toolInfo{title: label, kind: acp.ToolKindFetch}

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

	case "AskUserQuestion":
		title := "Asking for your input"
		if qs := parseAskQuestions(rawInput); len(qs) > 0 {
			title = qs[0].Question
		}
		return toolInfo{title: title, kind: acp.ToolKindOther}

	default:
		title := name
		if title == "" {
			title = "Tool call"
		}
		var content []acp.ToolCallContent
		if len(rawInput) > 0 {
			if pretty, err := json.MarshalIndent(json.RawMessage(rawInput), "", "  "); err == nil {
				content = []acp.ToolCallContent{acp.ToolContent(acp.TextBlock("```json\n" + string(pretty) + "\n```"))}
			}
		}
		return toolInfo{title: title, kind: toolKindFor(name), content: content}
	}
}

func grepLabel(rawInput json.RawMessage) string {
	var in struct {
		Pattern    string `json:"pattern"`
		Path       string `json:"path"`
		Glob       string `json:"glob"`
		Type       string `json:"type"`
		OutputMode string `json:"output_mode"`
		I          bool   `json:"-i"`
		N          bool   `json:"-n"`
		A          *int   `json:"-A"`
		B          *int   `json:"-B"`
		C          *int   `json:"-C"`
		HeadLimit  *int   `json:"head_limit"`
		Multiline  bool   `json:"multiline"`
	}
	_ = json.Unmarshal(rawInput, &in)

	label := "grep"
	if in.I {
		label += " -i"
	}
	if in.N {
		label += " -n"
	}
	if in.A != nil {
		label += fmt.Sprintf(" -A %d", *in.A)
	}
	if in.B != nil {
		label += fmt.Sprintf(" -B %d", *in.B)
	}
	if in.C != nil {
		label += fmt.Sprintf(" -C %d", *in.C)
	}
	switch in.OutputMode {
	case "files_with_matches":
		label += " -l"
	case "count":
		label += " -c"
	}
	if in.HeadLimit != nil {
		label += fmt.Sprintf(" | head -%d", *in.HeadLimit)
	}
	if in.Glob != "" {
		label += fmt.Sprintf(` --include="%s"`, in.Glob)
	}
	if in.Type != "" {
		label += " --type=" + in.Type
	}
	if in.Multiline {
		label += " -P"
	}
	if in.Pattern != "" {
		label += fmt.Sprintf(` "%s"`, in.Pattern)
	}
	if in.Path != "" {
		label += " " + in.Path
	}
	return label
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

// shouldEmitToolCall reports whether a tool_use should surface as its own
// ACP tool_call. TodoWrite renders as a plan update instead, and Task/Agent
// (subagent launches) are excluded so a permission prompt for one never
// surfaces a stray tool_call ahead of however the subagent's own activity
// is rendered.
func shouldEmitToolCall(name string) bool {
	return !isPlanTool(name) && name != "Agent" && name != "Task"
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
