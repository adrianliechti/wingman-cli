package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"
)

type eventDispatcher struct {
	ctx       context.Context
	conn      *acp.AgentSideConnection
	sessionID acp.SessionId
	done      chan turnCompleted

	toolOut         map[string]*strings.Builder
	seenReasoning   map[string]bool
	seenAgentText   map[string]bool
	guardianStarted map[string]bool
	startedTools    map[string]bool
	agentPhases     map[string]string
	lastGoal        string

	mu            sync.Mutex
	failure       error
	usage         *acp.Usage
	completedPlan *completedPlan
}

type completedPlan struct {
	itemID string
	text   string
}

func newEventDispatcher(ctx context.Context, conn *acp.AgentSideConnection, sid acp.SessionId) *eventDispatcher {
	return &eventDispatcher{
		ctx:             ctx,
		conn:            conn,
		sessionID:       sid,
		done:            make(chan turnCompleted, 1),
		toolOut:         map[string]*strings.Builder{},
		seenReasoning:   map[string]bool{},
		seenAgentText:   map[string]bool{},
		guardianStarted: map[string]bool{},
		startedTools:    map[string]bool{},
		agentPhases:     map[string]string{},
	}
}

func (d *eventDispatcher) setFailure(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failure == nil {
		d.failure = err
	}
}

func (d *eventDispatcher) setUsage(u *acp.Usage) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.usage = u
}

func (d *eventDispatcher) getUsage() *acp.Usage {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.usage
}

func (d *eventDispatcher) getFailure() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.failure
}

func (d *eventDispatcher) takeCompletedPlan() *completedPlan {
	d.mu.Lock()
	defer d.mu.Unlock()
	plan := d.completedPlan
	d.completedPlan = nil
	return plan
}

func (d *eventDispatcher) setCompletedPlan(itemID, text string) {
	d.mu.Lock()
	d.completedPlan = &completedPlan{itemID: itemID, text: text}
	d.mu.Unlock()
}

// isFatalTurnError reports whether a codex `error` notification represents an
// unrecoverable turn failure (auth/usage-limit/connection 401). The codex
// app-server is always launched with a configured provider + token, so these
// surface as a turn error rather than an ACP auth-required prompt (matching the
// reference's authConfigured=internalError path).
func isFatalTurnError(info json.RawMessage) bool {
	if len(info) == 0 {
		return false
	}
	var s string
	if json.Unmarshal(info, &s) == nil {
		return s == "unauthorized" || s == "usageLimitExceeded" || s == "rateLimitExceeded"
	}
	var obj map[string]struct {
		HTTPStatusCode int `json:"httpStatusCode"`
	}
	if json.Unmarshal(info, &obj) != nil {
		return false
	}
	for _, key := range []string{
		"httpConnectionFailed",
		"responseStreamConnectionFailed",
		"responseStreamDisconnected",
		"responseTooManyFailedAttempts",
	} {
		if v, ok := obj[key]; ok && v.HTTPStatusCode == 401 {
			return true
		}
	}
	return false
}

func (d *eventDispatcher) update(u acp.SessionUpdate) {
	d.updateWithContext(d.ctx, u)
}

func (d *eventDispatcher) updateWithContext(ctx context.Context, u acp.SessionUpdate) {
	if ctx.Err() != nil {
		return
	}
	_ = d.conn.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: d.sessionID,
		Update:    u,
	})
}

func (d *eventDispatcher) appendToolOut(itemID, text string) {
	b := d.toolOut[itemID]
	if b == nil {
		b = &strings.Builder{}
		d.toolOut[itemID] = b
	}
	b.WriteString(text)
	d.update(acp.UpdateToolCall(acp.ToolCallId(itemID), acp.WithUpdateContent([]acp.ToolCallContent{
		acp.ToolContent(acp.TextBlock(b.String())),
	})))
}

func (d *eventDispatcher) handle(method string, params json.RawMessage) {
	switch method {
	case "item/agentMessage/delta":
		var p struct {
			ItemID string `json:"itemId"`
			Delta  string `json:"delta"`
		}
		if json.Unmarshal(params, &p) == nil && p.Delta != "" {
			d.seenAgentText[p.ItemID] = true
			d.update(agentMessageUpdate(p.Delta, p.ItemID, d.agentPhases[p.ItemID]))
		}

	case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
		var p struct {
			ItemID string `json:"itemId"`
			Delta  string `json:"delta"`
		}
		if json.Unmarshal(params, &p) == nil && p.ItemID != "" {
			d.seenReasoning[p.ItemID] = true
			if p.Delta != "" {
				d.update(agentThoughtUpdate(p.Delta, p.ItemID))
			}
		}

	case "item/reasoning/summaryPartAdded":
		var p struct {
			ItemID string `json:"itemId"`
		}
		if json.Unmarshal(params, &p) == nil && p.ItemID != "" {
			d.seenReasoning[p.ItemID] = true
			d.update(agentThoughtUpdate("\n\n", p.ItemID))
		}

	case "item/started":
		d.handleItemStarted(params)

	case "item/completed":
		d.handleItemCompleted(params)

	case "item/autoApprovalReview/started":
		var g guardianNotif
		if json.Unmarshal(params, &g) == nil && g.ReviewID != "" {
			if d.guardianStarted[g.ReviewID] {
				d.update(guardianUpdateToolCall(g))
			} else {
				d.guardianStarted[g.ReviewID] = true
				d.update(guardianStartToolCall(g))
			}
		}

	case "item/autoApprovalReview/completed":
		var g guardianNotif
		if json.Unmarshal(params, &g) == nil && g.ReviewID != "" {
			if d.guardianStarted[g.ReviewID] {
				delete(d.guardianStarted, g.ReviewID)
				d.update(guardianUpdateToolCall(g))
			} else {
				d.update(guardianStartToolCall(g))
			}
		}

	case "thread/goal/updated":
		d.handleGoalUpdated(params)

	case "thread/goal/cleared":
		if d.lastGoal != "" {
			d.lastGoal = ""
			d.update(acp.UpdateAgentMessageText("\n\nGoal cleared.\n\n"))
		}

	case "error":
		var p struct {
			Error struct {
				Message        string          `json:"message"`
				CodexErrorInfo json.RawMessage `json:"codexErrorInfo"`
			} `json:"error"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		if p.Error.Message != "" {
			d.update(acp.UpdateAgentMessageText(p.Error.Message + "\n\n"))
		}
		if isFatalTurnError(p.Error.CodexErrorInfo) {
			msg := p.Error.Message
			if msg == "" {
				msg = "codex turn failed"
			}
			d.setFailure(errors.New(msg))

			select {
			case d.done <- turnCompleted{}:
			default:
			}
		}

	case "warning":
		var p struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(params, &p) == nil && p.Message != "" {
			d.update(acp.UpdateAgentMessageText("Warning: " + p.Message + "\n\n"))
		}

	case "item/commandExecution/outputDelta":

		var p struct {
			ItemID string `json:"itemId"`
			Delta  string `json:"delta"`
		}
		if json.Unmarshal(params, &p) == nil && p.Delta != "" && p.ItemID != "" {
			d.appendToolOut(p.ItemID, p.Delta)
		}

	case "thread/tokenUsage/updated":
		d.handleTokenUsage(params)

	case "configWarning":
		var p struct {
			Summary string `json:"summary"`
			Details string `json:"details"`
		}
		if json.Unmarshal(params, &p) == nil && p.Summary != "" {
			text := "Config warning: " + p.Summary
			if p.Details != "" {
				text += "\n\n" + p.Details
			}
			d.update(acp.UpdateAgentMessageText(text + "\n\n"))
		}

	case "model/rerouted":
		var p struct {
			FromModel string `json:"fromModel"`
			ToModel   string `json:"toModel"`
			Reason    string `json:"reason"`
		}
		if json.Unmarshal(params, &p) == nil && p.ToModel != "" {
			d.update(acp.UpdateAgentThoughtText(fmt.Sprintf("Model rerouted from %s to %s (%s).\n\n", p.FromModel, p.ToModel, p.Reason)))
		}

	case "turn/completed":
		var tc turnCompleted
		if json.Unmarshal(params, &tc) == nil {
			select {
			case d.done <- tc:
			default:
			}
		}
	}
}

func (d *eventDispatcher) handleGoalUpdated(params json.RawMessage) {
	var p struct {
		Goal struct {
			Objective string `json:"objective"`
			Status    string `json:"status"`
		} `json:"goal"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	objective := strings.TrimSpace(p.Goal.Objective)
	snapshot := p.Goal.Status + "\x00" + objective
	if snapshot == d.lastGoal {
		return
	}
	d.lastGoal = snapshot

	status := goalStatusLabel(p.Goal.Status)
	var text string
	if strings.Contains(objective, "\n") {
		text = fmt.Sprintf("Goal updated (%s):\n%s", status, objective)
	} else {
		text = fmt.Sprintf("Goal updated (%s): %s", status, objective)
	}
	d.update(acp.UpdateAgentMessageText("\n\n" + text + "\n\n"))
}

func (d *eventDispatcher) handleItemStarted(params json.RawMessage) {
	var env struct {
		Item json.RawMessage `json:"item"`
	}
	if err := json.Unmarshal(params, &env); err != nil {
		return
	}
	id, kind, ok := peekItem(env.Item)
	if !ok {
		return
	}

	switch kind {
	case "agentMessage":
		var it struct {
			Phase string `json:"phase"`
		}
		_ = json.Unmarshal(env.Item, &it)
		d.agentPhases[id] = it.Phase

	case "fileChange":
		opts := []acp.ToolCallStartOpt{
			acp.WithStartKind(acp.ToolKindEdit),
			acp.WithStartStatus(acp.ToolCallStatusInProgress),
		}
		if content := fileChangeContent(env.Item); len(content) > 0 {
			opts = append(opts, acp.WithStartContent(content))
		}
		opts = appendDisplayLocations(opts, fileChangeLocations(env.Item))
		d.update(acp.StartToolCall(acp.ToolCallId(id), "Edit files", opts...))

	case "commandExecution", "mcpToolCall", "dynamicToolCall":
		if u, ok := itemToolCallStart(env.Item, id, kind, acp.ToolCallStatusInProgress); ok {
			d.update(u)
		}

	case "webSearch":
		d.update(webSearchStartToolCall(env.Item, acp.ToolCallStatusInProgress))

	case "imageView":
		if u, ok := imageViewToolCall(env.Item); ok {
			d.startedTools[id] = true
			d.update(u)
		}

	case "imageGeneration":
		d.update(imageGenStartToolCall(id))

	case "collabAgentToolCall":
		if u, ok := collabStartToolCall(env.Item); ok {
			d.update(u)
		}

	case "subAgentActivity":
		if u, ok := subAgentStartToolCall(env.Item, acp.ToolCallStatusInProgress); ok {
			d.startedTools[id] = true
			d.update(u)
		}

	case "contextCompaction":
		d.update(compactionStartToolCall(id))
	}
}

func (d *eventDispatcher) handleItemCompleted(params json.RawMessage) {
	var env struct {
		Item json.RawMessage `json:"item"`
	}
	if err := json.Unmarshal(params, &env); err != nil {
		return
	}
	id, kind, ok := peekItem(env.Item)
	if !ok {
		return
	}

	switch kind {
	case "agentMessage":
		var it struct {
			Phase string `json:"phase"`
			Text  string `json:"text"`
		}
		_ = json.Unmarshal(env.Item, &it)
		d.agentPhases[id] = it.Phase
		if !d.seenAgentText[id] && it.Text != "" {
			d.update(agentMessageUpdate(it.Text, id, it.Phase))
		}
		delete(d.seenAgentText, id)
		delete(d.agentPhases, id)

	case "commandExecution":
		var it struct {
			Status           string  `json:"status"`
			AggregatedOutput *string `json:"aggregatedOutput"`
		}
		_ = json.Unmarshal(env.Item, &it)
		opts := []acp.ToolCallUpdateOpt{acp.WithUpdateStatus(toolStatusFor(it.Status))}

		if _, streamed := d.toolOut[id]; !streamed && it.AggregatedOutput != nil && *it.AggregatedOutput != "" {
			opts = append(opts, acp.WithUpdateContent([]acp.ToolCallContent{
				acp.ToolContent(acp.TextBlock(*it.AggregatedOutput)),
			}))
		}
		delete(d.toolOut, id)
		d.update(acp.UpdateToolCall(acp.ToolCallId(id), opts...))

	case "fileChange":
		var it struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(env.Item, &it)
		opts := []acp.ToolCallUpdateOpt{acp.WithUpdateStatus(toolStatusFor(it.Status))}
		if content := fileChangeContent(env.Item); len(content) > 0 {
			opts = append(opts, acp.WithUpdateContent(content))
		}
		d.update(acp.UpdateToolCall(acp.ToolCallId(id), opts...))

	case "dynamicToolCall":
		var it struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(env.Item, &it)
		d.update(acp.UpdateToolCall(acp.ToolCallId(id), acp.WithUpdateStatus(toolStatusFor(it.Status))))

	case "mcpToolCall":
		var it struct {
			Server string          `json:"server"`
			Tool   string          `json:"tool"`
			Args   json.RawMessage `json:"arguments"`
			Status string          `json:"status"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		_ = json.Unmarshal(env.Item, &it)
		var args any
		_ = json.Unmarshal(it.Args, &args)
		opts := []acp.ToolCallUpdateOpt{
			acp.WithUpdateStatus(toolStatusFor(it.Status)),
			acp.WithUpdateRawInput(args),
		}
		if out := mcpRawOutput(it.Result, it.Error); out != nil {
			opts = append(opts, acp.WithUpdateRawOutput(out))
		}
		delete(d.toolOut, id)
		d.update(acp.UpdateToolCall(acp.ToolCallId(id), opts...))

	case "webSearch":
		d.update(webSearchCompleteToolCall(env.Item))

	case "imageView":
		if !d.startedTools[id] {
			if u, ok := imageViewToolCall(env.Item); ok {
				d.update(u)
			}
		}
		delete(d.startedTools, id)

	case "imageGeneration":
		if u, ok := imageGenCompleteToolCall(env.Item); ok {
			d.update(u)
		}

	case "collabAgentToolCall":
		if u, ok := collabCompleteToolCall(env.Item); ok {
			d.update(u)
		}

	case "subAgentActivity":
		if d.startedTools[id] {
			delete(d.startedTools, id)
			if u, ok := subAgentCompleteToolCall(env.Item); ok {
				d.update(u)
			}
		} else if u, ok := subAgentStartToolCall(env.Item, acp.ToolCallStatusCompleted); ok {
			d.update(u)
		}

	case "contextCompaction":
		d.update(compactionCompleteToolCall(id))

	case "plan":
		var it struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(env.Item, &it)
		if it.Text != "" {
			d.update(acp.UpdateAgentMessageText("Plan:\n" + it.Text))
			d.setCompletedPlan(id, it.Text)
		}

	case "exitedReviewMode":
		var it struct {
			Review string `json:"review"`
		}
		_ = json.Unmarshal(env.Item, &it)
		if text := strings.TrimSpace(it.Review); text != "" {
			d.update(acp.UpdateAgentMessageText(text + "\n\n"))
		}

	case "reasoning":
		if d.seenReasoning[id] {
			delete(d.seenReasoning, id)
			return
		}
		var it struct {
			Summary []string `json:"summary"`
			Content []string `json:"content"`
		}
		_ = json.Unmarshal(env.Item, &it)
		if text := joinReasoning(it.Summary, it.Content); text != "" {
			d.update(acp.UpdateAgentThoughtText(text))
		}
	}
}

// rateLimitNote speaks only when constrained; the notification is a per-turn heartbeat.
func rateLimitNote(params json.RawMessage) string {
	var p struct {
		RateLimits struct {
			LimitName            string  `json:"limitName"`
			SpendControlReached  *bool   `json:"spendControlReached"`
			RateLimitReachedType *string `json:"rateLimitReachedType"`
			Primary              *struct {
				UsedPercent int    `json:"usedPercent"`
				ResetsAt    *int64 `json:"resetsAt"`
			} `json:"primary"`
		} `json:"rateLimits"`
	}
	if json.Unmarshal(params, &p) != nil {
		return ""
	}

	limits := p.RateLimits
	reached := limits.RateLimitReachedType != nil && *limits.RateLimitReachedType != ""
	spend := limits.SpendControlReached != nil && *limits.SpendControlReached
	if !reached && !spend {
		return ""
	}

	note := "Rate limit reached"
	if spend && !reached {
		note = "Spend limit reached"
	}
	if limits.LimitName != "" {
		note += " (" + limits.LimitName + ")"
	}
	if limits.Primary != nil && limits.Primary.ResetsAt != nil {
		note += ", resets " + time.Unix(*limits.Primary.ResetsAt, 0).Format(time.RFC3339)
	}
	return "*" + note + ".*\n\n"
}

func mcpRawOutput(result, mcpErr json.RawMessage) map[string]any {
	hasResult := len(result) > 0 && string(result) != "null"
	hasErr := len(mcpErr) > 0 && string(mcpErr) != "null"
	if !hasResult && !hasErr {
		return nil
	}
	var res, e any
	_ = json.Unmarshal(result, &res)
	_ = json.Unmarshal(mcpErr, &e)
	return map[string]any{"result": res, "error": e}
}

func joinReasoning(summary, content []string) string {
	parts := summary
	if len(parts) == 0 {
		parts = content
	}
	var nonEmpty []string
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	return strings.Join(nonEmpty, "\n\n")
}

func (d *eventDispatcher) handleTokenUsage(params json.RawMessage) {
	var p struct {
		TokenUsage struct {
			Last struct {
				TotalTokens           int `json:"totalTokens"`
				InputTokens           int `json:"inputTokens"`
				CachedInputTokens     int `json:"cachedInputTokens"`
				OutputTokens          int `json:"outputTokens"`
				ReasoningOutputTokens int `json:"reasoningOutputTokens"`
			} `json:"last"`
			ModelContextWindow int `json:"modelContextWindow"`
		} `json:"tokenUsage"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	last := p.TokenUsage.Last
	cachedRead := last.CachedInputTokens
	reasoning := last.ReasoningOutputTokens
	d.setUsage(&acp.Usage{
		TotalTokens:      last.TotalTokens,
		InputTokens:      max(0, last.InputTokens-last.CachedInputTokens),
		OutputTokens:     last.OutputTokens,
		CachedReadTokens: &cachedRead,
		ThoughtTokens:    &reasoning,
	})
	if size := p.TokenUsage.ModelContextWindow; size > 0 {
		d.update(acp.SessionUpdate{UsageUpdate: &acp.SessionUsageUpdate{
			SessionUpdate: "usage_update",
			Used:          last.TotalTokens,
			Size:          size,
		}})
	}
}

func peekItem(item json.RawMessage) (id, kind string, ok bool) {
	var probe struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(item, &probe); err != nil {
		return "", "", false
	}
	return probe.ID, probe.Type, probe.ID != "" && probe.Type != ""
}

func toolStatusFor(s string) acp.ToolCallStatus {
	switch s {
	case "inProgress":
		return acp.ToolCallStatusInProgress
	case "failed", "declined", "interrupted":
		return acp.ToolCallStatusFailed
	default:
		return acp.ToolCallStatusCompleted
	}
}

type commandAction struct {
	Type  string `json:"type"`
	Path  string `json:"path"`
	Query string `json:"query"`
}

// The ACP SDK mirrors a lone location into rawInput.path. Codex already
// supplies the location as a file chip, so clear that synthetic input to keep
// clients from rendering the same path again as a hint and expanded argument.
func appendDisplayLocations(opts []acp.ToolCallStartOpt, locations []acp.ToolCallLocation) []acp.ToolCallStartOpt {
	if len(locations) == 0 {
		return opts
	}
	return append(opts, acp.WithStartLocations(locations), acp.WithStartRawInput(nil))
}

func commandRawInput(command, cwd string) map[string]any {
	input := map[string]any{"command": command}
	if cwd != "" {
		input["cwd"] = cwd
	}
	return input
}

func commandActionToolCall(actions []commandAction) (title string, kind acp.ToolKind, rawInput any, locations []acp.ToolCallLocation, ok bool) {
	if len(actions) != 1 {
		return "", "", nil, nil, false
	}
	a := actions[0]
	switch a.Type {
	case "read":
		if a.Path == "" {
			return "", "", nil, nil, false
		}
		return "Read file", acp.ToolKindRead, nil, []acp.ToolCallLocation{{Path: a.Path}}, true
	case "search":
		var locations []acp.ToolCallLocation
		if a.Path != "" {
			locations = []acp.ToolCallLocation{{Path: a.Path}}
		}
		var input any
		if a.Query != "" {
			input = map[string]any{"query": a.Query}
		}
		return "Search files", acp.ToolKindSearch, input, locations, true
	case "listFiles":
		var locations []acp.ToolCallLocation
		if a.Path != "" {
			locations = []acp.ToolCallLocation{{Path: a.Path}}
		}
		return "List files", acp.ToolKindRead, nil, locations, true
	}
	return "", "", nil, nil, false
}

type fileChange struct {
	Path string `json:"path"`
	Kind struct {
		Type string `json:"type"`
	} `json:"kind"`
	Diff string `json:"diff"`
}

func fileChangeContent(raw json.RawMessage) []acp.ToolCallContent {
	var it struct {
		Changes []fileChange `json:"changes"`
	}
	if err := json.Unmarshal(raw, &it); err != nil {
		return nil
	}
	var content []acp.ToolCallContent
	for _, ch := range it.Changes {
		if ch.Path == "" {
			continue
		}
		var oldText *string
		var newText string
		if ch.Kind.Type == "add" && !isUnifiedDiff(ch.Diff) {

			newText = ch.Diff
		} else {
			old, nw := splitUnifiedDiff(ch.Diff)
			newText = nw
			if ch.Kind.Type != "add" {
				oldText = &old
			}
		}
		content = append(content, acp.ToolCallContent{
			Diff: &acp.ToolCallContentDiff{
				Type:    "diff",
				Path:    ch.Path,
				OldText: oldText,
				NewText: newText,

				Meta: map[string]any{"kind": ch.Kind.Type},
			},
		})
	}
	return content
}

func fileChangeLocations(raw json.RawMessage) []acp.ToolCallLocation {
	var it struct {
		Changes []fileChange `json:"changes"`
	}
	if json.Unmarshal(raw, &it) != nil {
		return nil
	}
	seen := make(map[string]bool)
	locations := make([]acp.ToolCallLocation, 0, len(it.Changes))
	for _, change := range it.Changes {
		if change.Path == "" || seen[change.Path] {
			continue
		}
		seen[change.Path] = true
		locations = append(locations, acp.ToolCallLocation{Path: change.Path})
	}
	return locations
}

func isUnifiedDiff(s string) bool {
	return strings.HasPrefix(s, "--- ") || strings.Contains(s, "\n--- ")
}

func splitUnifiedDiff(diff string) (oldText, newText string) {
	var oldLines, newLines []string
	inHunk := false
	for line := range strings.SplitSeq(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "@@"):
			inHunk = true
		case !inHunk:

		case strings.HasPrefix(line, "\\"):

		case strings.HasPrefix(line, "-"):
			oldLines = append(oldLines, line[1:])
		case strings.HasPrefix(line, "+"):
			newLines = append(newLines, line[1:])
		default:

			text := line
			if strings.HasPrefix(line, " ") {
				text = line[1:]
			}
			oldLines = append(oldLines, text)
			newLines = append(newLines, text)
		}
	}
	return strings.Join(oldLines, "\n"), strings.Join(newLines, "\n")
}

var shellPrefixRe = regexp.MustCompile(`^(?:/bin/)?(?:bash|zsh|sh)\s+(?:-[lc]+\s+)?`)

func stripShellPrefix(cmd string) string {
	c := strings.TrimSpace(cmd)
	c = trimOuterCommandQuotes(c)
	c = shellPrefixRe.ReplaceAllString(c, "")
	return trimOuterCommandQuotes(strings.TrimSpace(c))
}

func trimOuterCommandQuotes(command string) string {
	if len(command) < 2 {
		return command
	}
	first, last := command[0], command[len(command)-1]
	if (first == '\'' || first == '"') && last == first {
		return command[1 : len(command)-1]
	}
	return command
}
