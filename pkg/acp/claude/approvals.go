package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/coder/acp-go-sdk"
)

const (
	optionAllowOnce   acp.PermissionOptionId = "allow-once"
	optionAllowAlways acp.PermissionOptionId = "allow-always"
	optionRejectOnce  acp.PermissionOptionId = "reject-once"
)

// toolCallTracker decides, for a given tool_use id, whether the next sighting
// should emit a tool_call (the first sighting) or a tool_call_update (any
// later one). The CLI can report a tool_use to us via two independent paths
// that race on different goroutines — the permission control_request (handled
// by approver.handle) and the streamed assistant message (handled by
// emitAssistant) — so the decision and the network write that acts on it run
// while holding the tracker's lock. That serializes the two paths for a given
// id and guarantees the tool_call always reaches the wire before any
// tool_call_update referencing it, regardless of which path is first.
type toolCallTracker struct {
	mu      sync.Mutex
	emitted map[string]bool
}

func newToolCallTracker() *toolCallTracker {
	return &toolCallTracker{emitted: map[string]bool{}}
}

func (t *toolCallTracker) emit(id string, start, refine func() error) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.emitted[id] {
		return refine()
	}
	t.emitted[id] = true
	if err := start(); err != nil {
		delete(t.emitted, id)
		return err
	}
	return nil
}

func (t *toolCallTracker) has(id string) bool {
	if t == nil || id == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.emitted[id]
}

func (t *toolCallTracker) complete(id string) bool {
	if t == nil || id == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.emitted[id] {
		return false
	}
	delete(t.emitted, id)
	return true
}

func permissionOptions() []acp.PermissionOption {
	return []acp.PermissionOption{
		{OptionId: optionAllowOnce, Name: "Allow Once", Kind: acp.PermissionOptionKindAllowOnce},
		{OptionId: optionAllowAlways, Name: "Always Allow", Kind: acp.PermissionOptionKindAllowAlways},
		{OptionId: optionRejectOnce, Name: "Deny", Kind: acp.PermissionOptionKindRejectOnce},
	}
}

type approver struct {
	ctx            context.Context
	conn           *acp.AgentSideConnection
	sid            acp.SessionId
	out            *streamWriter
	cwd            string
	emitted        *toolCallTracker
	parentForAgent func(string) string
	askForm        bool
	applyMode      func(string)
}

func pendingToolCall(id string, kind acp.ToolKind) acp.ToolCallUpdate {
	status := acp.ToolCallStatusPending
	return acp.ToolCallUpdate{ToolCallId: acp.ToolCallId(id), Kind: &kind, Status: &status}
}

func (a *approver) handle(req controlRequest) {
	if req.Request.Subtype != "can_use_tool" {
		a.respondError(req.RequestID, "unsupported control request: "+req.Request.Subtype)
		return
	}

	name := req.Request.ToolName
	id := req.Request.ToolUseID
	agentID := req.Request.AgentID
	if agentID == "" {
		agentID = req.AgentID
	}
	parentToolUseID := ""
	if a.parentForAgent != nil {
		parentToolUseID = a.parentForAgent(agentID)
	}

	// The CLI can invoke can_use_tool before the assistant message's tool_use
	// block streams to us, so a permission request can reference a tool_call
	// the client has never seen. Emit it now if no one has yet, so the client
	// can always associate the prompt below with a known tool call.
	if id != "" && shouldEmitToolCall(name) && a.emitted != nil {
		_ = a.emitted.emit(id, func() error {
			return a.emitToolCallStart(id, name, req.Request.Input, parentToolUseID)
		}, func() error {
			return nil
		})
	}

	if name == "AskUserQuestion" {
		a.handleAskUserQuestion(req, parentToolUseID)
		return
	}

	tc := pendingToolCall(id, toolKindFor(name))
	claudeMeta := map[string]any{"toolName": name}
	if parentToolUseID != "" {
		claudeMeta["parentToolUseId"] = parentToolUseID
	}
	tc.Meta = map[string]any{"claudeCode": claudeMeta}
	title := name
	if title == "" {
		title = "Tool call"
	}
	tc.Title = &title
	if len(req.Request.Input) > 0 {
		var input map[string]any
		if json.Unmarshal(req.Request.Input, &input) == nil {
			tc.RawInput = input
		}
	}
	if req.Request.Description != "" {
		tc.Content = []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(req.Request.Description))}
	}

	options := permissionOptions()
	for i := range options {
		if options[i].OptionId == optionAllowAlways {
			options[i].Meta = map[string]any{"permission": permissionMetadataForAlwaysAllow(req.Request)}
		}
	}
	resp, err := a.conn.RequestPermission(a.ctx, acp.RequestPermissionRequest{
		SessionId: a.sid,
		ToolCall:  tc,
		Options:   options,
	})
	allow, always := false, false
	if err == nil && resp.Outcome.Cancelled == nil && resp.Outcome.Selected != nil {
		always = resp.Outcome.Selected.OptionId == optionAllowAlways
		allow = always || resp.Outcome.Selected.OptionId == optionAllowOnce
	}

	if !allow {
		a.respondDeny(req.RequestID)
		return
	}

	var perms any
	switch {
	case name == "ExitPlanMode":
		perms = []any{map[string]any{"type": "setMode", "mode": defaultModeID, "destination": "session"}}
	case always:
		perms = alwaysAllowPermissions(req.Request)
	}
	a.respondAllow(req.RequestID, req.Request.Input, perms)

	if name == "ExitPlanMode" && a.applyMode != nil {
		a.applyMode(defaultModeID)
	}
}

// alwaysAllowPermissions turns an "Allow for Session" choice into the
// updatedPermissions the CLI needs to stop re-asking: the CLI's own suggested
// rules when it sent any, else a session-scoped allow rule for the tool.
func alwaysAllowPermissions(req controlRequestBody) any {
	if len(req.PermissionSuggestions) > 0 {
		var v []any
		if json.Unmarshal(req.PermissionSuggestions, &v) == nil && len(v) > 0 {
			return v
		}
	}
	if req.ToolName == "" {
		return nil
	}
	return []any{map[string]any{
		"type":        "addRules",
		"rules":       []any{map[string]any{"toolName": req.ToolName}},
		"behavior":    "allow",
		"destination": "session",
	}}
}

func permissionMetadataForAlwaysAllow(req controlRequestBody) map[string]any {
	updates, _ := alwaysAllowPermissions(req).([]any)
	changes := make([]any, 0, len(updates))
	for _, raw := range updates {
		update, _ := raw.(map[string]any)
		if update == nil {
			continue
		}
		typ, _ := update["type"].(string)
		destination, _ := update["destination"].(string)
		lifetime := claudePermissionLifetime(destination)
		switch typ {
		case "addRules", "removeRules", "replaceRules":
			operation := map[string]string{"addRules": "add", "removeRules": "remove", "replaceRules": "replace"}[typ]
			behavior, _ := update["behavior"].(string)
			var targets []any
			var rendered []string
			for _, rawRule := range anySlice(update["rules"]) {
				rule, _ := rawRule.(map[string]any)
				toolName, _ := rule["toolName"].(string)
				if toolName == "" {
					continue
				}
				target := map[string]any{"type": "tool", "toolName": toolName}
				if ruleContent, _ := rule["ruleContent"].(string); ruleContent != "" {
					target["matcher"] = map[string]any{"type": "provider_rule", "provider": "claudeCode", "value": ruleContent}
					rendered = append(rendered, fmt.Sprintf("%s calls matching %s", toolName, ruleContent))
				} else {
					rendered = append(rendered, "all "+toolName+" calls")
				}
				targets = append(targets, target)
			}
			if len(targets) == 0 {
				continue
			}
			verb := "Allow"
			if operation == "remove" {
				verb = "Remove " + behavior + " rules for"
			} else if operation == "replace" {
				verb = "Replace " + behavior + " rules with"
			} else if behavior == "deny" {
				verb = "Deny"
			} else if behavior == "ask" {
				verb = "Ask before"
			}
			changes = append(changes, map[string]any{
				"type": "policy_rule", "operation": operation, "ruleBehavior": behavior,
				"description": verb + " " + strings.Join(rendered, ", "), "lifetime": lifetime, "targets": targets,
			})
		case "addDirectories", "removeDirectories":
			operation := "add"
			verb := "Allow filesystem access under "
			if typ == "removeDirectories" {
				operation = "remove"
				verb = "Remove additional filesystem access under "
			}
			paths := stringValues(update["directories"])
			var targets []any
			for _, path := range paths {
				targets = append(targets, map[string]any{"type": "filesystem", "matcher": map[string]any{"type": "directory", "path": path}})
			}
			if len(paths) > 0 {
				changes = append(changes, map[string]any{
					"type": "policy_rule", "operation": operation, "ruleBehavior": "allow",
					"description": verb + strings.Join(paths, ", "), "lifetime": lifetime, "targets": targets,
				})
			}
		case "setMode":
			mode, _ := update["mode"].(string)
			changes = append(changes, map[string]any{
				"type": "permission_mode", "operation": "set", "provider": "claudeCode", "mode": mode,
				"description": "Set Claude Code permission mode to " + mode, "lifetime": lifetime,
			})
		}
	}
	return map[string]any{"version": 1, "changes": changes}
}

func claudePermissionLifetime(destination string) map[string]any {
	switch destination {
	case "session":
		return map[string]any{"scope": "session"}
	case "cliArg":
		return map[string]any{"scope": "process", "storage": "cli_argument"}
	case "userSettings":
		return map[string]any{"scope": "persistent", "storage": "user"}
	case "projectSettings":
		return map[string]any{"scope": "persistent", "storage": "project"}
	case "localSettings":
		return map[string]any{"scope": "persistent", "storage": "project_local"}
	default:
		return map[string]any{"scope": "unknown"}
	}
}

func anySlice(value any) []any {
	values, _ := value.([]any)
	return values
}

func stringValues(value any) []string {
	var out []string
	for _, value := range anySlice(value) {
		if s, ok := value.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

type askOption struct {
	Label       string
	Description string
}

type askQuestion struct {
	Question    string
	Header      string
	MultiSelect bool
	Options     []askOption
}

func parseAskQuestions(raw json.RawMessage) []askQuestion {
	var p struct {
		Questions []struct {
			Question    string            `json:"question"`
			Header      string            `json:"header"`
			MultiSelect bool              `json:"multiSelect"`
			Options     []json.RawMessage `json:"options"`
		} `json:"questions"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return nil
	}
	out := make([]askQuestion, 0, len(p.Questions))
	for _, q := range p.Questions {
		aq := askQuestion{Question: q.Question, Header: q.Header, MultiSelect: q.MultiSelect}
		for _, o := range q.Options {
			var s string
			if json.Unmarshal(o, &s) == nil {
				if s != "" {
					aq.Options = append(aq.Options, askOption{Label: s})
				}
				continue
			}
			var obj struct {
				Label       string `json:"label"`
				Description string `json:"description"`
			}
			if json.Unmarshal(o, &obj) == nil && obj.Label != "" {
				aq.Options = append(aq.Options, askOption{Label: obj.Label, Description: obj.Description})
			}
		}
		if aq.Question != "" && len(aq.Options) > 0 {
			out = append(out, aq)
		}
	}
	return out
}

func askOptionID(i int) acp.PermissionOptionId {
	return acp.PermissionOptionId("ask-" + strconv.Itoa(i))
}

func askFieldKey(i int) string {
	return "question_" + strconv.Itoa(i)
}

func askCustomKey(i int) string {
	return "question_" + strconv.Itoa(i) + "_custom"
}

func askMessage(questions []askQuestion) string {
	if len(questions) == 1 {
		return questions[0].Question
	}
	return "Please answer the following questions."
}

func askElicitationSchema(questions []askQuestion) acp.UnstableElicitationSchema {
	single := len(questions) == 1
	props := map[string]any{}
	for i, q := range questions {
		options := make([]map[string]any, 0, len(q.Options))
		for _, o := range q.Options {
			opt := map[string]any{"const": o.Label, "title": o.Label}
			if o.Description != "" {
				opt["description"] = o.Description
			}
			options = append(options, opt)
		}
		field := map[string]any{}
		if q.Header != "" {
			field["title"] = q.Header
		}
		if !single {
			field["description"] = q.Question
		}
		if q.MultiSelect {
			field["type"] = "array"
			field["items"] = map[string]any{"anyOf": options}
		} else {
			field["type"] = "string"
			field["oneOf"] = options
		}
		props[askFieldKey(i)] = field
		props[askCustomKey(i)] = map[string]any{
			"type":        "string",
			"title":       "Other",
			"description": "Type your own answer instead of choosing an option above (optional).",
			"_meta": map[string]any{
				"_askUserQuestionCustomAnswer": map[string]any{
					"questionId":     askFieldKey(i),
					"isCustomAnswer": true,
				},
			},
		}
	}
	return acp.UnstableElicitationSchema{Type: acp.UnstableElicitationSchemaTypeObject, Properties: props}
}

func askAnswersFromContent(questions []askQuestion, content map[string]any) map[string]any {
	answers := map[string]any{}
	for i, q := range questions {
		if custom, ok := content[askCustomKey(i)].(string); ok && strings.TrimSpace(custom) != "" {
			answers[q.Question] = strings.TrimSpace(custom)
			continue
		}
		switch v := content[askFieldKey(i)].(type) {
		case string:
			if v != "" {
				answers[q.Question] = v
			}
		case []any:
			var parts []string
			for _, item := range v {
				if s, ok := item.(string); ok && s != "" {
					parts = append(parts, s)
				}
			}
			if len(parts) > 0 {
				answers[q.Question] = strings.Join(parts, ", ")
			}
		}
	}
	return answers
}

// handleAskUserQuestion renders the CLI's AskUserQuestion tool over ACP.
// Clients that advertised form elicitation get a single form carrying all
// questions (full fidelity: multi-select, free-text "Other" answers); other
// clients get one session/request_permission per question with the question's
// options as the permission options. Answers go back to the CLI as
// updatedInput.answers keyed by question text; a skipped question has no
// entry, which the tool reports to the model as the user declining.
func (a *approver) handleAskUserQuestion(req controlRequest, parentToolUseID string) {
	questions := parseAskQuestions(req.Request.Input)
	if len(questions) == 0 {
		a.writeResponse(req.RequestID, map[string]any{"behavior": "deny", "message": "AskUserQuestion called with no valid questions."})
		return
	}

	if a.askForm && a.askViaElicitation(req, questions) {
		return
	}

	answers := map[string]any{}
	presented := false
	for _, q := range questions {
		tc := pendingToolCall(req.Request.ToolUseID, acp.ToolKindOther)
		title := q.Question
		tc.Title = &title
		claudeMeta := map[string]any{"toolName": req.Request.ToolName}
		if parentToolUseID != "" {
			claudeMeta["parentToolUseId"] = parentToolUseID
		}
		tc.Meta = map[string]any{"claudeCode": claudeMeta}

		opts := make([]acp.PermissionOption, 0, len(q.Options)+1)
		for i, o := range q.Options {
			opts = append(opts, acp.PermissionOption{OptionId: askOptionID(i), Name: o.Label, Kind: acp.PermissionOptionKindAllowOnce})
		}
		opts = append(opts, acp.PermissionOption{OptionId: "ask-skip", Name: "Skip", Kind: acp.PermissionOptionKindRejectOnce})

		resp, err := a.conn.RequestPermission(a.ctx, acp.RequestPermissionRequest{
			SessionId: a.sid,
			ToolCall:  tc,
			Options:   opts,
		})
		if err != nil {
			continue
		}
		presented = true
		if resp.Outcome.Cancelled != nil || resp.Outcome.Selected == nil {
			continue
		}
		for i, o := range q.Options {
			if resp.Outcome.Selected.OptionId == askOptionID(i) {
				answers[q.Question] = o.Label
			}
		}
	}
	if !presented {
		a.writeResponse(req.RequestID, map[string]any{"behavior": "deny", "message": "Could not present the question to the user."})
		return
	}
	a.respondAskAnswers(req, answers)
}

// askViaElicitation reports whether the request was fully handled; a false
// return means the client failed the elicitation call despite advertising it,
// and the caller should fall back to the permission flow.
func (a *approver) askViaElicitation(req controlRequest, questions []askQuestion) bool {
	resp, err := a.conn.UnstableCreateElicitation(a.ctx, acp.UnstableCreateElicitationRequest{
		Form: &acp.UnstableCreateElicitationForm{
			Message:         askMessage(questions),
			Mode:            "form",
			RequestedSchema: askElicitationSchema(questions),
		},
	})
	if err != nil {
		return false
	}
	switch {
	case resp.Accept != nil:
		a.respondAskAnswers(req, askAnswersFromContent(questions, resp.Accept.Content))
	case resp.Decline != nil:
		a.respondAskAnswers(req, map[string]any{})
	default:
		a.writeResponse(req.RequestID, map[string]any{"behavior": "deny", "message": "Question cancelled by user."})
	}
	return true
}

func (a *approver) respondAskAnswers(req controlRequest, answers map[string]any) {
	var input map[string]any
	if json.Unmarshal(req.Request.Input, &input) != nil || input == nil {
		input = map[string]any{}
	}
	input["answers"] = answers
	merged, err := json.Marshal(input)
	if err != nil {
		merged = req.Request.Input
	}
	a.respondAllow(req.RequestID, merged, nil)
}

func (a *approver) emitToolCallStart(id, name string, rawInput json.RawMessage, parentToolUseID string) error {
	u := toolCallStartUpdate(id, name, rawInput, a.cwd, acp.ToolCallStatusPending)
	withClaudeToolMeta(&u, name, parentToolUseID)
	return a.conn.SessionUpdate(a.ctx, acp.SessionNotification{
		SessionId: a.sid,
		Update:    u,
	})
}

func (a *approver) respondAllow(requestID string, input json.RawMessage, updatedPermissions any) {
	updated := json.RawMessage("{}")
	if len(input) > 0 {
		updated = input
	}
	body := map[string]any{"behavior": "allow", "updatedInput": updated}
	if updatedPermissions != nil {
		body["updatedPermissions"] = updatedPermissions
	}
	a.writeResponse(requestID, body)
}

func (a *approver) respondDeny(requestID string) {
	a.writeResponse(requestID, map[string]any{"behavior": "deny", "message": "Rejected by user"})
}

func (a *approver) writeResponse(requestID string, body map[string]any) {
	_ = a.out.writeJSON(controlResponse{
		Type:     "control_response",
		Response: controlResponseBody{Subtype: "success", RequestID: requestID, Response: body},
	})
}

func (a *approver) respondError(requestID, msg string) {
	_ = a.out.writeJSON(controlResponse{
		Type:     "control_response",
		Response: controlResponseBody{Subtype: "error", RequestID: requestID, Error: msg},
	})
}
