package codex

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/coder/acp-go-sdk"
)

// imageView -------------------------------------------------------------------

type imageViewItem struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

func imageViewToolCall(raw json.RawMessage) (acp.SessionUpdate, bool) {
	var it imageViewItem
	if json.Unmarshal(raw, &it) != nil || it.ID == "" {
		return acp.SessionUpdate{}, false
	}
	opts := []acp.ToolCallStartOpt{
		acp.WithStartKind(acp.ToolKindRead),
		acp.WithStartStatus(acp.ToolCallStatusCompleted),
	}
	if it.Path != "" {
		opts = appendDisplayLocations(opts, []acp.ToolCallLocation{{Path: it.Path}})
	}
	return acp.StartToolCall(acp.ToolCallId(it.ID), "View image", opts...), true
}

// imageGeneration -------------------------------------------------------------

type imageGenItem struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	RevisedPrompt string `json:"revisedPrompt"`
	Result        string `json:"result"`
	SavedPath     string `json:"savedPath"`
}

func imageGenStatus(s string) acp.ToolCallStatus {
	switch s {
	case "generating", "in_progress", "inProgress", "incomplete":
		return acp.ToolCallStatusInProgress
	case "failed":
		return acp.ToolCallStatusFailed
	default:
		return acp.ToolCallStatusCompleted
	}
}

func imageGenStartToolCall(id string) acp.SessionUpdate {
	return acp.StartToolCall(acp.ToolCallId(id), "Image generation",
		acp.WithStartKind(acp.ToolKindOther),
		acp.WithStartStatus(acp.ToolCallStatusInProgress),
	)
}

func imageGenContent(it imageGenItem) []acp.ToolCallContent {
	var content []acp.ToolCallContent
	if strings.TrimSpace(it.RevisedPrompt) != "" {
		content = append(content, acp.ToolContent(acp.TextBlock("Revised prompt: "+it.RevisedPrompt)))
	}
	if strings.TrimSpace(it.Result) != "" {
		img := acp.ImageBlock(it.Result, "image/png")
		if it.SavedPath != "" && img.Image != nil {
			img.Image.Uri = new(it.SavedPath)
		}
		content = append(content, acp.ToolContent(img))
	}
	return content
}

func imageGenCompleteToolCall(raw json.RawMessage) (acp.SessionUpdate, bool) {
	var it imageGenItem
	if json.Unmarshal(raw, &it) != nil || it.ID == "" {
		return acp.SessionUpdate{}, false
	}
	opts := []acp.ToolCallUpdateOpt{acp.WithUpdateStatus(imageGenStatus(it.Status))}
	if content := imageGenContent(it); len(content) > 0 {
		opts = append(opts, acp.WithUpdateContent(content))
	}
	if it.SavedPath != "" {
		opts = append(opts, acp.WithUpdateLocations([]acp.ToolCallLocation{{Path: it.SavedPath}}))
	}
	return acp.UpdateToolCall(acp.ToolCallId(it.ID), opts...), true
}

// imageGenToolCall emits a single completed tool_call for history replay, where
// no prior `in_progress` start was streamed.
func imageGenToolCall(raw json.RawMessage) (acp.SessionUpdate, bool) {
	var it imageGenItem
	if json.Unmarshal(raw, &it) != nil || it.ID == "" {
		return acp.SessionUpdate{}, false
	}
	opts := []acp.ToolCallStartOpt{
		acp.WithStartKind(acp.ToolKindOther),
		acp.WithStartStatus(imageGenStatus(it.Status)),
	}
	if content := imageGenContent(it); len(content) > 0 {
		opts = append(opts, acp.WithStartContent(content))
	}
	if it.SavedPath != "" {
		opts = appendDisplayLocations(opts, []acp.ToolCallLocation{{Path: it.SavedPath}})
	}
	return acp.StartToolCall(acp.ToolCallId(it.ID), "Image generation", opts...), true
}

// collabAgentToolCall ---------------------------------------------------------

type collabItem struct {
	ID                string          `json:"id"`
	Tool              string          `json:"tool"`
	Status            string          `json:"status"`
	SenderThreadID    string          `json:"senderThreadId"`
	ReceiverThreadIDs []string        `json:"receiverThreadIds"`
	Prompt            string          `json:"prompt"`
	AgentsStates      json.RawMessage `json:"agentsStates"`
}

func collabRawInput(it collabItem) map[string]any {
	input := make(map[string]any)
	if it.Prompt != "" {
		input["prompt"] = it.Prompt
	}
	return input
}

func collabStartToolCall(raw json.RawMessage) (acp.SessionUpdate, bool) {
	var it collabItem
	if json.Unmarshal(raw, &it) != nil || it.ID == "" {
		return acp.SessionUpdate{}, false
	}
	title := it.Tool
	if title == "" {
		title = "Collab agent"
	}
	opts := []acp.ToolCallStartOpt{
		acp.WithStartKind(acp.ToolKindOther),
		acp.WithStartStatus(toolStatusFor(it.Status)),
	}
	if input := collabRawInput(it); len(input) > 0 {
		opts = append(opts, acp.WithStartRawInput(input))
	}
	return acp.StartToolCall(acp.ToolCallId(it.ID), title, opts...), true
}

func collabCompleteToolCall(raw json.RawMessage) (acp.SessionUpdate, bool) {
	var it collabItem
	if json.Unmarshal(raw, &it) != nil || it.ID == "" {
		return acp.SessionUpdate{}, false
	}
	opts := []acp.ToolCallUpdateOpt{
		acp.WithUpdateStatus(toolStatusFor(it.Status)),
	}
	if input := collabRawInput(it); len(input) > 0 {
		opts = append(opts, acp.WithUpdateRawInput(input))
	}
	if it.Tool != "" {
		opts = append(opts, acp.WithUpdateTitle(it.Tool))
	}
	return acp.UpdateToolCall(acp.ToolCallId(it.ID), opts...), true
}

// subAgentActivity -------------------------------------------------------------

type subAgentItem struct {
	ID            string `json:"id"`
	AgentThreadID string `json:"agentThreadId"`
	AgentPath     string `json:"agentPath"`
	Kind          string `json:"kind"`
}

func subAgentName(path string) string {
	parts := strings.Split(path, "/")
	for _, part := range slices.Backward(parts) {
		if part != "" {
			return part
		}
	}
	return "subagent"
}

func subAgentTitle(it subAgentItem) string {
	name := subAgentName(it.AgentPath)
	switch it.Kind {
	case "started":
		return "Start subagent " + name
	case "interacted":
		return "Interact with subagent " + name
	case "interrupted":
		return "Interrupt subagent " + name
	case "completed":
		return "Complete subagent " + name
	}
	return "Subagent " + name
}

func subAgentStartToolCall(raw json.RawMessage, status acp.ToolCallStatus) (acp.SessionUpdate, bool) {
	var it subAgentItem
	if json.Unmarshal(raw, &it) != nil || it.ID == "" {
		return acp.SessionUpdate{}, false
	}
	return acp.StartToolCall(acp.ToolCallId(it.ID), subAgentTitle(it),
		acp.WithStartKind(acp.ToolKindOther),
		acp.WithStartStatus(status),
	), true
}

func subAgentCompleteToolCall(raw json.RawMessage) (acp.SessionUpdate, bool) {
	var it subAgentItem
	if json.Unmarshal(raw, &it) != nil || it.ID == "" {
		return acp.SessionUpdate{}, false
	}
	return acp.UpdateToolCall(acp.ToolCallId(it.ID),
		acp.WithUpdateStatus(acp.ToolCallStatusCompleted),
	), true
}

// contextCompaction ------------------------------------------------------------

func compactionStartToolCall(id string) acp.SessionUpdate {
	u := acp.StartToolCall(acp.ToolCallId(id), "Compact conversation",
		acp.WithStartKind(acp.ToolKindThink),
		acp.WithStartStatus(acp.ToolCallStatusInProgress),
	)
	u.ToolCall.Meta = contextCompactionMeta()
	return u
}

func compactionCompleteToolCall(id string) acp.SessionUpdate {
	u := acp.UpdateToolCall(acp.ToolCallId(id),
		acp.WithUpdateTitle("Compact conversation"),
		acp.WithUpdateStatus(acp.ToolCallStatusCompleted),
	)
	u.ToolCallUpdate.Meta = contextCompactionMeta()
	return u
}

func completedCompactionToolCall(id string) acp.SessionUpdate {
	u := acp.StartToolCall(acp.ToolCallId(id), "Compact conversation",
		acp.WithStartKind(acp.ToolKindThink),
		acp.WithStartStatus(acp.ToolCallStatusCompleted),
	)
	u.ToolCall.Meta = contextCompactionMeta()
	return u
}

func contextCompactionMeta() map[string]any {
	return map[string]any{
		"contextCompaction": map[string]any{"version": 1},
	}
}

// webSearch -------------------------------------------------------------------

type webSearchItem struct {
	ID     string `json:"id"`
	Query  string `json:"query"`
	Action *struct {
		Type    string   `json:"type"`
		Query   *string  `json:"query"`
		Queries []string `json:"queries"`
		URL     *string  `json:"url"`
		Pattern *string  `json:"pattern"`
	} `json:"action"`
}

func webSearchStartToolCall(raw json.RawMessage, status acp.ToolCallStatus) acp.SessionUpdate {
	var it webSearchItem
	_ = json.Unmarshal(raw, &it)
	opts := []acp.ToolCallStartOpt{
		acp.WithStartKind(acp.ToolKindSearch),
		acp.WithStartStatus(status),
	}
	if input := webSearchInput(it); len(input) > 0 {
		opts = append(opts, acp.WithStartRawInput(input))
	}
	return acp.StartToolCall(acp.ToolCallId(it.ID), webSearchTitle(it), opts...)
}

func webSearchCompleteToolCall(raw json.RawMessage) acp.SessionUpdate {
	var it webSearchItem
	_ = json.Unmarshal(raw, &it)
	opts := []acp.ToolCallUpdateOpt{
		acp.WithUpdateStatus(acp.ToolCallStatusCompleted),
		acp.WithUpdateTitle(webSearchTitle(it)),
	}
	if input := webSearchInput(it); len(input) > 0 {
		opts = append(opts, acp.WithUpdateRawInput(input))
	}
	return acp.UpdateToolCall(acp.ToolCallId(it.ID), opts...)
}

func webSearchTitle(it webSearchItem) string {
	a := it.Action
	if a == nil || a.Type == "search" {
		return "Web search"
	}
	switch a.Type {
	case "openPage":
		return "Open page"
	case "findInPage":
		return "Find in page"
	default:
		return "Web search"
	}
}

func webSearchInput(it webSearchItem) map[string]any {
	input := make(map[string]any)
	a := it.Action
	if a == nil {
		if it.Query != "" {
			input["query"] = it.Query
		}
		return input
	}
	switch a.Type {
	case "search":
		query := it.Query
		if a.Query != nil && *a.Query != "" {
			query = *a.Query
		}
		if query != "" {
			input["query"] = query
		}
		if len(a.Queries) > 0 {
			queries := make([]string, 0, len(a.Queries))
			for _, query := range a.Queries {
				if query != "" {
					queries = append(queries, query)
				}
			}
			if len(queries) > 0 {
				input["queries"] = queries
				delete(input, "query")
			}
		}
	case "openPage":
		if a.URL != nil && *a.URL != "" {
			input["url"] = *a.URL
		}
	case "findInPage":
		if a.Pattern != nil && *a.Pattern != "" {
			input["pattern"] = *a.Pattern
		}
		if a.URL != nil && *a.URL != "" {
			input["url"] = *a.URL
		}
	}
	return input
}

// guardian (item/autoApprovalReview/*) ----------------------------------------

type guardianNotif struct {
	ReviewID string `json:"reviewId"`
	Review   struct {
		Status            string  `json:"status"`
		RiskLevel         *string `json:"riskLevel"`
		UserAuthorization *string `json:"userAuthorization"`
		Rationale         *string `json:"rationale"`
	} `json:"review"`
	Action json.RawMessage `json:"action"`
}

func guardianToolCallID(reviewID string) string { return "guardian_assessment:" + reviewID }

func guardianStatus(s string) acp.ToolCallStatus {
	switch s {
	case "inProgress":
		return acp.ToolCallStatusInProgress
	case "approved":
		return acp.ToolCallStatusCompleted
	default: // denied, aborted, timedOut
		return acp.ToolCallStatusFailed
	}
}

func guardianStatusLabel(s string) string {
	switch s {
	case "inProgress":
		return "In progress"
	case "approved":
		return "Approved"
	case "denied":
		return "Denied"
	case "aborted":
		return "Aborted"
	case "timedOut":
		return "Timed out"
	default:
		return s
	}
}

func guardianActionSummary(raw json.RawMessage) string {
	var a struct {
		Type          string   `json:"type"`
		Command       string   `json:"command"`
		Program       string   `json:"program"`
		Argv          []string `json:"argv"`
		ProcessID     string   `json:"processId"`
		Files         []string `json:"files"`
		Host          string   `json:"host"`
		Target        string   `json:"target"`
		Server        string   `json:"server"`
		ConnectorName string   `json:"connectorName"`
		ToolName      string   `json:"toolName"`
		Reason        string   `json:"reason"`
	}
	if json.Unmarshal(raw, &a) != nil || a.Type == "" {
		return ""
	}
	switch a.Type {
	case "command":
		return strings.TrimSpace("shell " + a.Command)
	case "execve":
		cmd := a.Argv
		if len(cmd) == 0 && a.Program != "" {
			cmd = []string{a.Program}
		}
		return strings.TrimSpace("exec " + strings.Join(cmd, " "))
	case "writeStdin":
		return "write stdin to process " + a.ProcessID
	case "applyPatch":
		if len(a.Files) == 1 {
			return "apply_patch touching " + a.Files[0]
		}
		return fmt.Sprintf("apply_patch touching %d files", len(a.Files))
	case "networkAccess":
		label := a.Target
		if label == "" {
			label = a.Host
		}
		return "network access to " + label
	case "mcpToolCall":
		label := a.ConnectorName
		if label == "" {
			label = a.Server
		}
		return fmt.Sprintf("MCP %s on %s", a.ToolName, label)
	case "requestPermissions":
		if a.Reason != "" {
			return a.Reason
		}
		return "request additional permissions"
	default:
		return ""
	}
}

func guardianContent(g guardianNotif) []acp.ToolCallContent {
	lines := []string{"Status: " + guardianStatusLabel(g.Review.Status)}
	if summary := guardianActionSummary(g.Action); summary != "" {
		lines = append(lines, "Action: "+summary)
	}
	if g.Review.RiskLevel != nil && *g.Review.RiskLevel != "" {
		lines = append(lines, "Risk: "+*g.Review.RiskLevel)
	}
	if g.Review.UserAuthorization != nil && *g.Review.UserAuthorization != "" {
		lines = append(lines, "Authorization: "+*g.Review.UserAuthorization)
	}
	if g.Review.Rationale != nil && strings.TrimSpace(*g.Review.Rationale) != "" {
		lines = append(lines, "Rationale: "+*g.Review.Rationale)
	}
	return []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(strings.Join(lines, "\n")))}
}

func guardianStartToolCall(g guardianNotif) acp.SessionUpdate {
	return acp.StartToolCall(acp.ToolCallId(guardianToolCallID(g.ReviewID)), "Guardian Review",
		acp.WithStartKind(acp.ToolKindThink),
		acp.WithStartStatus(guardianStatus(g.Review.Status)),
		acp.WithStartContent(guardianContent(g)),
	)
}

func guardianUpdateToolCall(g guardianNotif) acp.SessionUpdate {
	return acp.UpdateToolCall(acp.ToolCallId(guardianToolCallID(g.ReviewID)),
		acp.WithUpdateStatus(guardianStatus(g.Review.Status)),
		acp.WithUpdateContent(guardianContent(g)),
	)
}

// goal (thread/goal/*) --------------------------------------------------------

func goalStatusLabel(s string) string {
	switch s {
	case "budgetLimited":
		return "budget limited"
	case "usageLimited":
		return "usage limited"
	default:
		return s
	}
}
