package codex

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/coder/acp-go-sdk"
)

func TestCommandApprovalChoicesIncludePolicyAmendments(t *testing.T) {
	p := execApprovalParams{
		ProposedExecpolicyAmendment: []string{"npm", "install"},
		ProposedNetworkPolicyAmendments: []networkPolicyAmendment{{
			Host: "registry.npmjs.org", Action: "allow",
		}},
	}
	choices := commandApprovalChoices(p)
	if len(choices) != 5 {
		t.Fatalf("choices = %#v", choices)
	}

	execChoice := choiceByID(t, choices, optionExecpolicyAmendment)
	got, err := json.Marshal(execChoice.decision)
	if err != nil || string(got) != `{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["npm","install"]}}` {
		t.Fatalf("execpolicy decision = %s, err=%v", got, err)
	}
	if execChoice.option.Kind != acp.PermissionOptionKindAllowAlways || permissionDescription(execChoice.option) != "Allow commands starting with npm install" {
		t.Fatalf("execpolicy option = %#v", execChoice.option)
	}

	networkChoice := choiceByID(t, choices, "apply-network-policy-amendment:0")
	got, err = json.Marshal(networkChoice.decision)
	if err != nil || string(got) != `{"applyNetworkPolicyAmendment":{"network_policy_amendment":{"host":"registry.npmjs.org","action":"allow"}}}` {
		t.Fatalf("network decision = %s, err=%v", got, err)
	}
}

func TestFileApprovalChoiceDescribesGrantRoot(t *testing.T) {
	choice := choiceByID(t, fileApprovalChoices(fileApprovalParams{GrantRoot: "/workspace/generated"}), optionAllowAlways)
	if choice.option.Name != "Allow Root for Session" {
		t.Fatalf("option name = %q", choice.option.Name)
	}
	if got := permissionDescription(choice.option); got != "Allow writes under /workspace/generated for this session" {
		t.Fatalf("permission description = %q", got)
	}
}

func TestPermissionsApprovalDispatchAndSafeFallback(t *testing.T) {
	c := &codexClient{handlers: make(map[string]*threadHandlers)}
	called := false
	c.setThreadHandlers("thread-1", &threadHandlers{
		onPermissionsApproval: func(p permissionsApprovalParams) permissionsApprovalResponse {
			called = true
			if p.ItemID != "permission-1" || p.Permissions["network"] == nil {
				t.Fatalf("params = %#v", p)
			}
			return permissionsApprovalResponse{Permissions: grantedPermissions(p.Permissions), Scope: "session"}
		},
	})
	params := json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","itemId":"permission-1","cwd":"/workspace","permissions":{"network":{"enabled":true}}}`)
	result, rpcErr := c.dispatchRequest(context.Background(), "item/permissions/requestApproval", params)
	if rpcErr != nil || !called {
		t.Fatalf("dispatch result = %#v, rpcErr=%v, called=%v", result, rpcErr, called)
	}
	response := result.(permissionsApprovalResponse)
	if response.Scope != "session" || response.Permissions["network"] == nil {
		t.Fatalf("response = %#v", response)
	}

	params = json.RawMessage(`{"threadId":"unhandled","itemId":"permission-2","permissions":{"network":{"enabled":true}}}`)
	result, rpcErr = c.dispatchRequest(context.Background(), "item/permissions/requestApproval", params)
	response = result.(permissionsApprovalResponse)
	if rpcErr != nil || response.Scope != "turn" || !response.StrictAutoReview || len(response.Permissions) != 0 {
		t.Fatalf("unhandled response = %#v, rpcErr=%v", response, rpcErr)
	}
}

func TestMessageOnlyElicitations(t *testing.T) {
	tests := []struct {
		name   string
		params elicitationParams
		want   bool
	}{
		{name: "form without schema", params: elicitationParams{Mode: "form"}, want: true},
		{name: "null schema", params: elicitationParams{Mode: "form", RequestedSchema: json.RawMessage(`null`)}, want: true},
		{name: "empty object", params: elicitationParams{Mode: "form", RequestedSchema: json.RawMessage(`{"type":"object","properties":{}}`)}, want: true},
		{name: "structured field", params: elicitationParams{Mode: "form", RequestedSchema: json.RawMessage(`{"type":"object","properties":{"code":{"type":"string"}},"required":["code"]}`)}},
		{name: "openai form mode is no longer offered", params: elicitationParams{Mode: "openai/form"}},
		{name: "missing properties", params: elicitationParams{Mode: "form", RequestedSchema: json.RawMessage(`{"type":"object"}`)}},
		{name: "null properties", params: elicitationParams{Mode: "form", RequestedSchema: json.RawMessage(`{"type":"object","properties":null}`)}},
		{name: "unknown mode", params: elicitationParams{Mode: "unknown"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isMessageOnlyElicitation(tt.params); got != tt.want {
				t.Errorf("isMessageOnlyElicitation(%#v) = %v, want %v", tt.params, got, tt.want)
			}
		})
	}

	structured := elicitationParams{Mode: "form", RequestedSchema: json.RawMessage(`{"type":"object","properties":{"code":{"type":"string"}}}`)}
	if got := (&approver{}).handleElicitation(structured); got.Action != "decline" {
		t.Fatalf("structured fallback action = %q, want decline", got.Action)
	}
}

func TestPermissionsGrantMetadata(t *testing.T) {
	permissions := map[string]any{
		"network": map[string]any{"enabled": true},
		"fileSystem": map[string]any{
			"read":  []any{"/workspace"},
			"write": []any{"/workspace/tmp"},
		},
	}
	changes := permissionGrantChanges(permissions, "session")
	if len(changes) != 3 {
		t.Fatalf("changes = %#v", changes)
	}
	if got := formatRequestedPermissions(permissions); got != "Network Access: true\n\nFile System Read Access: /workspace\n\nFile System Write Access: /workspace/tmp" {
		t.Fatalf("formatted permissions = %q", got)
	}
	granted := grantedPermissions(map[string]any{"network": permissions["network"], "unknown": true})
	if granted["network"] == nil || granted["unknown"] != nil {
		t.Fatalf("granted permissions = %#v", granted)
	}
}

func TestPermissionsToolCallUsesOneReadableRepresentation(t *testing.T) {
	tc := permissionsToolCall(permissionsApprovalParams{
		ItemID: "permission-1",
		Reason: "The command needs network access.",
		Permissions: map[string]any{
			"network": map[string]any{"enabled": true},
		},
	})
	if tc.Title == nil || *tc.Title != "Permissions request" {
		t.Fatalf("title = %#v", tc.Title)
	}
	if tc.RawInput != nil {
		t.Fatalf("internal request duplicated as raw input: %#v", tc.RawInput)
	}
	if len(tc.Content) != 1 || tc.Content[0].Content == nil {
		t.Fatalf("content = %#v", tc.Content)
	}
	text := tc.Content[0].Content.Content.Text
	if text == nil || text.Text != "The command needs network access.\n\nNetwork Access: true" {
		t.Fatalf("content text = %#v", text)
	}
}

func choiceByID(t *testing.T, choices []approvalChoice, id acp.PermissionOptionId) approvalChoice {
	t.Helper()
	for _, choice := range choices {
		if choice.option.OptionId == id {
			return choice
		}
	}
	t.Fatalf("choice %q not found in %#v", id, choices)
	return approvalChoice{}
}

func permissionDescription(option acp.PermissionOption) string {
	permission, _ := option.Meta["permission"].(map[string]any)
	changes, _ := permission["changes"].([]map[string]any)
	if len(changes) == 0 {
		return ""
	}
	description, _ := changes[0]["description"].(string)
	return description
}
