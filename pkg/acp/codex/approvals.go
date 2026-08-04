package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/coder/acp-go-sdk"
)

const (
	optionAllowOnce   acp.PermissionOptionId = "allow-once"
	optionAllowAlways acp.PermissionOptionId = "allow-always"
	optionRejectOnce  acp.PermissionOptionId = "reject-once"

	optionExecpolicyAmendment     acp.PermissionOptionId = "accept-execpolicy-amendment"
	optionAllowPermissionsTurn    acp.PermissionOptionId = "allow-permissions-turn"
	optionAllowPermissionsSession acp.PermissionOptionId = "allow-permissions-session"
	optionRejectPermissions       acp.PermissionOptionId = "reject-permissions"
)

func permissionOptions() []acp.PermissionOption {
	return []acp.PermissionOption{
		{OptionId: optionAllowOnce, Name: "Allow Once", Kind: acp.PermissionOptionKindAllowOnce},
		{OptionId: optionAllowAlways, Name: "Allow for Session", Kind: acp.PermissionOptionKindAllowAlways},
		{OptionId: optionRejectOnce, Name: "Reject", Kind: acp.PermissionOptionKindRejectOnce},
	}
}

type approvalChoice struct {
	option   acp.PermissionOption
	decision any
}

func permissionOption(id acp.PermissionOptionId, name string, kind acp.PermissionOptionKind, changes ...map[string]any) acp.PermissionOption {
	option := acp.PermissionOption{OptionId: id, Name: name, Kind: kind}
	if len(changes) > 0 {
		option.Meta = map[string]any{"permission": map[string]any{"version": 1, "changes": changes}}
	}
	return option
}

type approver struct {
	ctx       context.Context
	conn      *acp.AgentSideConnection
	sessionID acp.SessionId
	client    acp.ClientCapabilities

	mu          sync.Mutex
	pendingURLs map[acp.UnstableElicitationId]struct{}
}

func newApprover(ctx context.Context, conn *acp.AgentSideConnection, sid acp.SessionId, client acp.ClientCapabilities) *approver {
	return &approver{
		ctx: ctx, conn: conn, sessionID: sid, client: client,
		pendingURLs: make(map[acp.UnstableElicitationId]struct{}),
	}
}

func pendingToolCall(id string, kind acp.ToolKind) acp.ToolCallUpdate {
	status := acp.ToolCallStatusPending
	return acp.ToolCallUpdate{ToolCallId: acp.ToolCallId(id), Kind: &kind, Status: &status}
}

func (a *approver) ask(tc acp.ToolCallUpdate) (id acp.PermissionOptionId, ok bool) {
	return a.askWithOptions(tc, permissionOptions())
}

func (a *approver) askWithOptions(tc acp.ToolCallUpdate, options []acp.PermissionOption) (id acp.PermissionOptionId, ok bool) {
	resp, err := a.conn.RequestPermission(a.ctx, acp.RequestPermissionRequest{
		SessionId: a.sessionID,
		ToolCall:  tc,
		Options:   options,
	})
	if err != nil || resp.Outcome.Cancelled != nil {
		return "", false
	}
	return resp.Outcome.Selected.OptionId, true
}

func (a *approver) handleExec(p execApprovalParams) execApprovalResponse {
	tc := pendingToolCall(p.ItemID, acp.ToolKindExecute)
	if p.Reason != "" {
		tc.Content = []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(p.Reason))}
	}
	if p.Command != "" {
		tc.RawInput = map[string]any{"command": stripShellPrefix(p.Command), "cwd": p.Cwd}
	}

	choices := commandApprovalChoices(p)
	id, ok := a.askWithOptions(tc, approvalOptions(choices))
	if !ok {
		return execApprovalResponse{Decision: "cancel"}
	}
	for _, choice := range choices {
		if choice.option.OptionId == id {
			return execApprovalResponse{Decision: choice.decision}
		}
	}
	return execApprovalResponse{Decision: "decline"}
}

func (a *approver) handleFile(p fileApprovalParams) fileApprovalResponse {
	tc := pendingToolCall(p.ItemID, acp.ToolKindEdit)
	if p.Reason != "" {
		tc.Content = []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(p.Reason))}
	}

	choices := fileApprovalChoices(p)
	id, ok := a.askWithOptions(tc, approvalOptions(choices))
	if !ok {
		return fileApprovalResponse{Decision: "cancel"}
	}
	for _, choice := range choices {
		if choice.option.OptionId == id {
			return fileApprovalResponse{Decision: choice.decision.(string)}
		}
	}
	return fileApprovalResponse{Decision: "decline"}
}

func (a *approver) handlePermissions(p permissionsApprovalParams) permissionsApprovalResponse {
	title := p.Reason
	if title == "" {
		title = "Permissions Request"
	}
	tc := pendingToolCall(p.ItemID, acp.ToolKindOther)
	tc.Title = &title
	tc.RawInput = p
	if content := formatRequestedPermissions(p.Permissions); content != "" {
		if p.Reason != "" {
			content = p.Reason + "\n\n" + content
		}
		tc.Content = []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(content))}
	}

	options := []acp.PermissionOption{
		permissionOption(optionAllowPermissionsSession, "Allow for Session", acp.PermissionOptionKindAllowAlways, permissionGrantChanges(p.Permissions, "session")...),
		permissionOption(optionAllowPermissionsTurn, "Allow Once", acp.PermissionOptionKindAllowOnce, permissionGrantChanges(p.Permissions, "turn")...),
		permissionOption(optionRejectPermissions, "Reject", acp.PermissionOptionKindRejectOnce),
	}
	id, ok := a.askWithOptions(tc, options)
	if !ok {
		return rejectPermissionsResponse()
	}
	switch id {
	case optionAllowPermissionsSession, optionAllowAlways:
		return permissionsApprovalResponse{Permissions: grantedPermissions(p.Permissions), Scope: "session", StrictAutoReview: false}
	case optionAllowPermissionsTurn, optionAllowOnce:
		return permissionsApprovalResponse{Permissions: grantedPermissions(p.Permissions), Scope: "turn", StrictAutoReview: false}
	default:
		return rejectPermissionsResponse()
	}
}

func approvalOptions(choices []approvalChoice) []acp.PermissionOption {
	options := make([]acp.PermissionOption, 0, len(choices))
	for _, choice := range choices {
		options = append(options, choice.option)
	}
	return options
}

func commandApprovalChoices(p execApprovalParams) []approvalChoice {
	alwaysName := "Allow for Session"
	var alwaysChanges []map[string]any
	if p.NetworkApprovalContext != nil && p.NetworkApprovalContext.Host != "" {
		alwaysName = "Allow Host for Session"
		matcher := map[string]any{"type": "host", "host": p.NetworkApprovalContext.Host}
		if p.NetworkApprovalContext.Protocol != "" {
			matcher["protocol"] = p.NetworkApprovalContext.Protocol
		}
		alwaysChanges = append(alwaysChanges, map[string]any{
			"type": "grant", "operation": "grant",
			"description": fmt.Sprintf("Allow access to %s for this session", p.NetworkApprovalContext.Host),
			"lifetime":    map[string]any{"scope": "session"},
			"targets":     []any{map[string]any{"type": "network", "matcher": matcher}},
		})
	}
	choices := []approvalChoice{
		{permissionOption(optionAllowOnce, "Allow Once", acp.PermissionOptionKindAllowOnce), "accept"},
		{permissionOption(optionAllowAlways, alwaysName, acp.PermissionOptionKindAllowAlways, alwaysChanges...), "acceptForSession"},
	}
	if len(p.ProposedExecpolicyAmendment) > 0 {
		prefix := strings.Join(p.ProposedExecpolicyAmendment, " ")
		label := "Allow and Remember Command Pattern"
		if prefix != "" && !strings.ContainsAny(prefix, "\r\n") {
			label = fmt.Sprintf("Allow Commands Starting With `%s`", prefix)
		}
		choices = append(choices, approvalChoice{
			permissionOption(optionExecpolicyAmendment, label, acp.PermissionOptionKindAllowAlways, map[string]any{
				"type": "policy_rule", "operation": "add", "ruleBehavior": "allow",
				"description": "Allow commands starting with " + prefix,
				"targets":     []any{map[string]any{"type": "command", "matcher": map[string]any{"type": "argv_prefix", "argv": p.ProposedExecpolicyAmendment}}},
			}),
			map[string]any{"acceptWithExecpolicyAmendment": map[string]any{"execpolicy_amendment": p.ProposedExecpolicyAmendment}},
		})
	}
	for i, amendment := range p.ProposedNetworkPolicyAmendments {
		kind := acp.PermissionOptionKindAllowAlways
		verb := "Allow"
		future := "Allow"
		if amendment.Action == "deny" {
			kind = acp.PermissionOptionKindRejectAlways
			verb = "Block"
			future = "Block"
		}
		id := acp.PermissionOptionId(fmt.Sprintf("apply-network-policy-amendment:%d", i))
		choices = append(choices, approvalChoice{
			permissionOption(id, fmt.Sprintf("%s %s in the Future", future, amendment.Host), kind, map[string]any{
				"type": "policy_rule", "operation": "add", "ruleBehavior": amendment.Action,
				"description": fmt.Sprintf("%s access to %s", verb, amendment.Host),
				"targets":     []any{map[string]any{"type": "network", "matcher": map[string]any{"type": "host", "host": amendment.Host}}},
			}),
			map[string]any{"applyNetworkPolicyAmendment": map[string]any{"network_policy_amendment": amendment}},
		})
	}
	return append(choices, approvalChoice{permissionOption(optionRejectOnce, "Reject", acp.PermissionOptionKindRejectOnce), "decline"})
}

func fileApprovalChoices(p fileApprovalParams) []approvalChoice {
	name := "Allow for Session"
	var changes []map[string]any
	if p.GrantRoot != "" {
		name = "Allow Root for Session"
		changes = append(changes, map[string]any{
			"type": "grant", "operation": "grant",
			"description": fmt.Sprintf("Allow writes under %s for this session", p.GrantRoot),
			"lifetime":    map[string]any{"scope": "session"},
			"targets":     []any{map[string]any{"type": "filesystem", "access": []any{"write"}, "matcher": map[string]any{"type": "directory", "path": p.GrantRoot}}},
		})
	}
	return []approvalChoice{
		{permissionOption(optionAllowOnce, "Allow Once", acp.PermissionOptionKindAllowOnce), "accept"},
		{permissionOption(optionAllowAlways, name, acp.PermissionOptionKindAllowAlways, changes...), "acceptForSession"},
		{permissionOption(optionRejectOnce, "Reject", acp.PermissionOptionKindRejectOnce), "decline"},
	}
}

func rejectPermissionsResponse() permissionsApprovalResponse {
	return permissionsApprovalResponse{Permissions: map[string]any{}, Scope: "turn", StrictAutoReview: true}
}

func grantedPermissions(requested map[string]any) map[string]any {
	granted := map[string]any{}
	for _, key := range []string{"network", "fileSystem"} {
		if value, ok := requested[key]; ok && value != nil {
			granted[key] = value
		}
	}
	return granted
}

func permissionGrantChanges(permissions map[string]any, scope string) []map[string]any {
	var changes []map[string]any
	lifetime := map[string]any{"scope": scope}
	suffix := " for this turn"
	if scope == "session" {
		suffix = " for this session"
	}
	if network, _ := permissions["network"].(map[string]any); network != nil {
		if enabled, ok := network["enabled"].(bool); ok {
			change := map[string]any{
				"type": "grant", "operation": "grant", "description": "Allow network access" + suffix,
				"lifetime": lifetime, "targets": []any{map[string]any{"type": "network", "matcher": map[string]any{"type": "any"}}},
			}
			if !enabled {
				change["type"] = "policy_rule"
				change["operation"] = "add"
				change["ruleBehavior"] = "deny"
				change["description"] = "Deny network access" + suffix
			}
			changes = append(changes, change)
		}
	}
	if fs, _ := permissions["fileSystem"].(map[string]any); fs != nil {
		for _, access := range []string{"read", "write"} {
			for _, path := range stringSlice(fs[access]) {
				changes = append(changes, map[string]any{
					"type": "grant", "operation": "grant", "description": fmt.Sprintf("Allow %s access to %s%s", access, path, suffix),
					"lifetime": lifetime,
					"targets":  []any{map[string]any{"type": "filesystem", "access": []any{access}, "matcher": map[string]any{"type": "exact_path", "path": path}}},
				})
			}
		}
	}
	return changes
}

func stringSlice(value any) []string {
	switch values := value.(type) {
	case []string:
		return values
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if s, ok := value.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func formatRequestedPermissions(permissions map[string]any) string {
	var lines []string
	if network, _ := permissions["network"].(map[string]any); network != nil {
		if enabled, ok := network["enabled"].(bool); ok {
			lines = append(lines, fmt.Sprintf("Network Access: %t", enabled))
		}
	}
	if fs, _ := permissions["fileSystem"].(map[string]any); fs != nil {
		if paths := stringSlice(fs["read"]); len(paths) > 0 {
			lines = append(lines, "File System Read Access: "+strings.Join(paths, ", "))
		}
		if paths := stringSlice(fs["write"]); len(paths) > 0 {
			lines = append(lines, "File System Write Access: "+strings.Join(paths, ", "))
		}
		if entries, ok := fs["entries"]; ok {
			if raw, err := json.Marshal(entries); err == nil && string(raw) != "[]" && string(raw) != "null" {
				lines = append(lines, "File System Entries: "+string(raw))
			}
		}
	}
	return strings.Join(lines, "\n\n")
}

func (a *approver) handleElicitation(p elicitationParams) elicitationResponse {
	if a.client.Elicitation != nil {
		switch {
		case p.Mode == "form" && a.client.Elicitation.Form != nil:
			return a.handleFormElicitation(p)
		case p.Mode == "url" && a.client.Elicitation.Url != nil:
			return a.handleURLElicitation(p)
		}
	}
	if p.Mode != "form" && p.Mode != "openai/form" {
		return elicitationResponse{Action: "decline", Content: nil, Meta: nil}
	}
	title := p.Message
	if title == "" {
		title = "Approve MCP request"
	}
	if p.ServerName != "" {
		title = fmt.Sprintf("%s: %s", p.ServerName, title)
	}
	tc := pendingToolCall(fmt.Sprintf("mcp-elicitation:%s", p.ServerName), acp.ToolKindOther)
	tc.Title = &title
	if p.Message != "" {
		tc.Content = []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(p.Message))}
	}

	id, ok := a.ask(tc)
	if !ok {
		return elicitationResponse{Action: "cancel"}
	}
	switch id {
	case optionAllowOnce, optionAllowAlways:
		return elicitationResponse{Action: "accept"}
	default:
		return elicitationResponse{Action: "decline"}
	}
}

func (a *approver) elicitationMeta(meta map[string]any) map[string]any {
	out := make(map[string]any, len(meta)+1)
	for key, value := range meta {
		out[key] = value
	}
	// acp-go-sdk v0.13.5 does not yet expose the request's top-level session
	// scope. Preserve it in metadata until the generated type catches up.
	out["sessionId"] = string(a.sessionID)
	return out
}

func (a *approver) handleFormElicitation(p elicitationParams) elicitationResponse {
	var schema acp.UnstableElicitationSchema
	if len(p.RequestedSchema) > 0 {
		if err := json.Unmarshal(p.RequestedSchema, &schema); err != nil {
			return elicitationResponse{Action: "cancel", Content: nil, Meta: nil}
		}
	}
	if schema.Type == "" {
		schema.Type = acp.UnstableElicitationSchemaTypeObject
	}
	if schema.Properties == nil {
		schema.Properties = map[string]any{}
	}

	resp, err := a.conn.UnstableCreateElicitation(a.ctx, acp.UnstableCreateElicitationRequest{
		Form: &acp.UnstableCreateElicitationForm{
			Meta:            a.elicitationMeta(p.Meta),
			Message:         p.Message,
			Mode:            "form",
			RequestedSchema: schema,
		},
	})
	if err != nil || resp.Cancel != nil {
		return elicitationResponse{Action: "cancel", Content: nil, Meta: nil}
	}
	if resp.Decline != nil {
		return elicitationResponse{Action: "decline", Content: nil, Meta: resp.Decline.Meta}
	}
	if resp.Accept != nil {
		return elicitationResponse{Action: "accept", Content: resp.Accept.Content, Meta: resp.Accept.Meta}
	}
	return elicitationResponse{Action: "cancel", Content: nil, Meta: nil}
}

func (a *approver) handleURLElicitation(p elicitationParams) elicitationResponse {
	id := acp.UnstableElicitationId(p.ElicitationID)
	if id == "" || p.URL == "" {
		return elicitationResponse{Action: "cancel", Content: nil, Meta: nil}
	}
	resp, err := a.conn.UnstableCreateElicitation(a.ctx, acp.UnstableCreateElicitationRequest{
		Url: &acp.UnstableCreateElicitationUrl{
			Meta:          a.elicitationMeta(p.Meta),
			ElicitationId: id,
			Message:       p.Message,
			Mode:          "url",
			Url:           p.URL,
		},
	})
	if err != nil || resp.Cancel != nil {
		return elicitationResponse{Action: "cancel", Content: nil, Meta: nil}
	}
	if resp.Decline != nil {
		return elicitationResponse{Action: "decline", Content: nil, Meta: resp.Decline.Meta}
	}
	if resp.Accept != nil {
		a.mu.Lock()
		a.pendingURLs[id] = struct{}{}
		a.mu.Unlock()
		return elicitationResponse{Action: "accept", Content: resp.Accept.Content, Meta: resp.Accept.Meta}
	}
	return elicitationResponse{Action: "cancel", Content: nil, Meta: nil}
}

func (a *approver) handleNotification(method string) {
	if method != "serverRequest/resolved" {
		return
	}
	a.mu.Lock()
	ids := make([]acp.UnstableElicitationId, 0, len(a.pendingURLs))
	for id := range a.pendingURLs {
		ids = append(ids, id)
	}
	clear(a.pendingURLs)
	a.mu.Unlock()
	for _, id := range ids {
		_ = a.conn.UnstableCompleteElicitation(a.ctx, acp.UnstableCompleteElicitationNotification{ElicitationId: id})
	}
}
