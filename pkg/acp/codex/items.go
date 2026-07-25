package codex

import (
	"encoding/json"
	"fmt"
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
	title := "View image"
	if it.Path != "" {
		title = "View Image " + it.Path
	}
	opts := []acp.ToolCallStartOpt{
		acp.WithStartKind(acp.ToolKindRead),
		acp.WithStartStatus(acp.ToolCallStatusCompleted),
		acp.WithStartRawInput(map[string]any{"path": it.Path}),
	}
	if it.Path != "" {
		opts = append(opts,
			acp.WithStartContent([]acp.ToolCallContent{acp.ToolContent(acp.ResourceLinkBlock(it.Path, it.Path))}),
			acp.WithStartLocations([]acp.ToolCallLocation{{Path: it.Path}}),
		)
	}
	return acp.StartToolCall(acp.ToolCallId(it.ID), title, opts...), true
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
		acp.WithStartRawInput(map[string]any{"id": id}),
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
			img.Image.Uri = acp.Ptr(it.SavedPath)
		}
		content = append(content, acp.ToolContent(img))
	}
	return content
}

func imageGenRawOutput(it imageGenItem) map[string]any {
	return map[string]any{
		"status":        it.Status,
		"revisedPrompt": it.RevisedPrompt,
		"result":        it.Result,
		"savedPath":     it.SavedPath,
	}
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
	opts = append(opts, acp.WithUpdateRawOutput(imageGenRawOutput(it)))
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
		acp.WithStartRawOutput(imageGenRawOutput(it)),
	}
	if content := imageGenContent(it); len(content) > 0 {
		opts = append(opts, acp.WithStartContent(content))
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
	var states any
	_ = json.Unmarshal(it.AgentsStates, &states)
	return map[string]any{
		"prompt":            it.Prompt,
		"senderThreadId":    it.SenderThreadID,
		"receiverThreadIds": it.ReceiverThreadIDs,
		"agentsStates":      states,
		"status":            it.Status,
	}
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
	return acp.StartToolCall(acp.ToolCallId(it.ID), title,
		acp.WithStartKind(acp.ToolKindOther),
		acp.WithStartStatus(toolStatusFor(it.Status)),
		acp.WithStartRawInput(collabRawInput(it)),
	), true
}

func collabCompleteToolCall(raw json.RawMessage) (acp.SessionUpdate, bool) {
	var it collabItem
	if json.Unmarshal(raw, &it) != nil || it.ID == "" {
		return acp.SessionUpdate{}, false
	}
	opts := []acp.ToolCallUpdateOpt{
		acp.WithUpdateStatus(toolStatusFor(it.Status)),
		acp.WithUpdateRawInput(collabRawInput(it)),
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
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] != "" {
			return parts[i]
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
	}
	return "Subagent " + name
}

func subAgentRawInput(it subAgentItem) map[string]any {
	return map[string]any{
		"agentThreadId": it.AgentThreadID,
		"agentPath":     it.AgentPath,
		"activityKind":  it.Kind,
	}
}

func subAgentStartToolCall(raw json.RawMessage, status acp.ToolCallStatus) (acp.SessionUpdate, bool) {
	var it subAgentItem
	if json.Unmarshal(raw, &it) != nil || it.ID == "" {
		return acp.SessionUpdate{}, false
	}
	return acp.StartToolCall(acp.ToolCallId(it.ID), subAgentTitle(it),
		acp.WithStartKind(acp.ToolKindOther),
		acp.WithStartStatus(status),
		acp.WithStartRawInput(subAgentRawInput(it)),
	), true
}

func subAgentCompleteToolCall(raw json.RawMessage) (acp.SessionUpdate, bool) {
	var it subAgentItem
	if json.Unmarshal(raw, &it) != nil || it.ID == "" {
		return acp.SessionUpdate{}, false
	}
	return acp.UpdateToolCall(acp.ToolCallId(it.ID),
		acp.WithUpdateStatus(acp.ToolCallStatusCompleted),
		acp.WithUpdateRawInput(subAgentRawInput(it)),
	), true
}

// contextCompaction ------------------------------------------------------------

func compactionStartToolCall(id string) acp.SessionUpdate {
	return acp.StartToolCall(acp.ToolCallId(id), "Context compacting",
		acp.WithStartKind(acp.ToolKindOther),
		acp.WithStartStatus(acp.ToolCallStatusInProgress),
	)
}

func compactionCompleteToolCall(id string) acp.SessionUpdate {
	return acp.UpdateToolCall(acp.ToolCallId(id),
		acp.WithUpdateTitle("Context compacted"),
		acp.WithUpdateStatus(acp.ToolCallStatusCompleted),
	)
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
	var rawInput map[string]any
	_ = json.Unmarshal(raw, &rawInput)
	return acp.StartToolCall(acp.ToolCallId(it.ID), webSearchTitle(it),
		acp.WithStartKind(acp.ToolKindSearch),
		acp.WithStartStatus(status),
		acp.WithStartRawInput(rawInput),
	)
}

func webSearchCompleteToolCall(raw json.RawMessage) acp.SessionUpdate {
	var it webSearchItem
	_ = json.Unmarshal(raw, &it)
	var rawInput map[string]any
	_ = json.Unmarshal(raw, &rawInput)
	return acp.UpdateToolCall(acp.ToolCallId(it.ID),
		acp.WithUpdateStatus(acp.ToolCallStatusCompleted),
		acp.WithUpdateTitle(webSearchTitle(it)),
		acp.WithUpdateRawInput(rawInput),
	)
}

func webSearchTitle(it webSearchItem) string {
	a := it.Action
	if a == nil {
		if it.Query != "" {
			return "Web search: " + it.Query
		}
		return "Web search"
	}
	switch a.Type {
	case "search":
		query := it.Query
		if a.Query != nil && *a.Query != "" {
			query = *a.Query
		} else if len(a.Queries) > 0 {
			var qs []string
			for _, q := range a.Queries {
				if q != "" {
					qs = append(qs, q)
				}
			}
			if len(qs) > 0 {
				query = strings.Join(qs, ", ")
			}
		}
		if query != "" {
			return "Web search: " + query
		}
		return "Web search"
	case "openPage":
		if a.URL != nil && *a.URL != "" {
			return "Open page: " + *a.URL
		}
		return "Open page"
	case "findInPage":
		title := "Find in page"
		if a.Pattern != nil && *a.Pattern != "" {
			title += " for '" + *a.Pattern + "'"
		}
		if a.URL != nil && *a.URL != "" {
			title += " in " + *a.URL
		}
		return title
	default:
		return "Web search"
	}
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

func guardianStartToolCall(g guardianNotif, raw json.RawMessage) acp.SessionUpdate {
	var rawInput map[string]any
	_ = json.Unmarshal(raw, &rawInput)
	return acp.StartToolCall(acp.ToolCallId(guardianToolCallID(g.ReviewID)), "Guardian Review",
		acp.WithStartKind(acp.ToolKindThink),
		acp.WithStartStatus(guardianStatus(g.Review.Status)),
		acp.WithStartContent(guardianContent(g)),
		acp.WithStartRawInput(rawInput),
	)
}

func guardianUpdateToolCall(g guardianNotif, raw json.RawMessage) acp.SessionUpdate {
	var rawOutput map[string]any
	_ = json.Unmarshal(raw, &rawOutput)
	return acp.UpdateToolCall(acp.ToolCallId(guardianToolCallID(g.ReviewID)),
		acp.WithUpdateStatus(guardianStatus(g.Review.Status)),
		acp.WithUpdateContent(guardianContent(g)),
		acp.WithUpdateRawOutput(rawOutput),
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
