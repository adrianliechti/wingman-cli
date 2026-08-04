package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
)

// askClient drives the approver's AskUserQuestion tiers with configurable
// support: form elicitation, permission selection, or hard failures.
type askClient struct {
	stubClient
	mu          sync.Mutex
	permCalls   int
	formCalls   int
	permErr     error
	permPick    string
	formErr     error
	formContent map[string]any
	formDecline bool
	formCancel  bool
}

func (c *askClient) RequestPermission(_ context.Context, p acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	c.mu.Lock()
	c.permCalls++
	c.mu.Unlock()
	if c.permErr != nil {
		return acp.RequestPermissionResponse{}, c.permErr
	}
	for _, o := range p.Options {
		if o.Name == c.permPick {
			return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
				Selected: &acp.RequestPermissionOutcomeSelected{OptionId: o.OptionId},
			}}, nil
		}
	}
	return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
		Cancelled: &acp.RequestPermissionOutcomeCancelled{},
	}}, nil
}

func (c *askClient) UnstableCreateElicitation(_ context.Context, req acp.UnstableCreateElicitationRequest) (acp.UnstableCreateElicitationResponse, error) {
	c.mu.Lock()
	c.formCalls++
	c.mu.Unlock()
	switch {
	case c.formErr != nil:
		return acp.UnstableCreateElicitationResponse{}, c.formErr
	case c.formCancel:
		return acp.UnstableCreateElicitationResponse{Cancel: &acp.UnstableCreateElicitationCancel{Action: "cancel"}}, nil
	case c.formDecline:
		return acp.UnstableCreateElicitationResponse{Decline: &acp.UnstableCreateElicitationDecline{Action: "decline"}}, nil
	}
	return acp.UnstableCreateElicitationResponse{Accept: &acp.UnstableCreateElicitationAccept{
		Action:  "accept",
		Content: c.formContent,
	}}, nil
}

func (c *askClient) UnstableCompleteElicitation(context.Context, acp.UnstableCompleteElicitationNotification) error {
	return nil
}

func (c *askClient) SessionUpdate(context.Context, acp.SessionNotification) error {
	return nil
}

func (c *askClient) calls() (perm, form int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.permCalls, c.formCalls
}

type askResponse struct {
	Behavior string          `json:"behavior"`
	Message  string          `json:"message"`
	Input    json.RawMessage `json:"updatedInput"`
}

func runAskUserQuestion(t *testing.T, client acp.Client, askForm bool) (askResponse, *askClient) {
	t.Helper()
	agentSide, clientSide := net.Pipe()
	t.Cleanup(func() { _ = agentSide.Close(); _ = clientSide.Close() })
	conn := acp.NewAgentSideConnection(New(Options{}), agentSide, agentSide)
	_ = acp.NewClientSideConnection(client, clientSide, clientSide)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var out bytes.Buffer
	app := &approver{ctx: ctx, conn: conn, sid: "test", out: &streamWriter{w: &out}, askForm: askForm}

	var req controlRequest
	req.RequestID = "r1"
	req.Request.Subtype = "can_use_tool"
	req.Request.ToolName = "AskUserQuestion"
	req.Request.ToolUseID = "tu1"
	req.Request.Input = json.RawMessage(`{"questions":[{"question":"Which color?","header":"Color","options":[{"label":"Red"},{"label":"Blue"}]}]}`)
	app.handle(req)

	var env struct {
		Response struct {
			Response askResponse `json:"response"`
		} `json:"response"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("parse control response %q: %v", out.String(), err)
	}
	ac, _ := client.(*askClient)
	return env.Response.Response, ac
}

func askAnswerOf(t *testing.T, resp askResponse) map[string]any {
	t.Helper()
	var input struct {
		Answers map[string]any `json:"answers"`
	}
	if err := json.Unmarshal(resp.Input, &input); err != nil {
		t.Fatalf("parse updatedInput %s: %v", resp.Input, err)
	}
	return input.Answers
}

func TestAskUserQuestionFormTier(t *testing.T) {
	client := &askClient{formContent: map[string]any{"question_0": "Blue"}}
	resp, c := runAskUserQuestion(t, client, true)
	if resp.Behavior != "allow" {
		t.Fatalf("behavior = %q (%s)", resp.Behavior, resp.Message)
	}
	if answers := askAnswerOf(t, resp); answers["Which color?"] != "Blue" {
		t.Errorf("answers = %#v", answers)
	}
	if perm, form := c.calls(); form != 1 || perm != 0 {
		t.Errorf("calls: form=%d perm=%d, want form only", form, perm)
	}
}

func TestAskUserQuestionFormDecline(t *testing.T) {
	client := &askClient{formDecline: true}
	resp, _ := runAskUserQuestion(t, client, true)
	if resp.Behavior != "allow" {
		t.Fatalf("behavior = %q", resp.Behavior)
	}
	if answers := askAnswerOf(t, resp); len(answers) != 0 {
		t.Errorf("declined form should yield empty answers, got %#v", answers)
	}
}

func TestAskUserQuestionFormCancel(t *testing.T) {
	client := &askClient{formCancel: true}
	resp, _ := runAskUserQuestion(t, client, true)
	if resp.Behavior != "deny" || !strings.Contains(resp.Message, "cancelled") {
		t.Fatalf("cancel should deny, got %q (%s)", resp.Behavior, resp.Message)
	}
}

func TestAskUserQuestionFormErrorFallsBackToPermissions(t *testing.T) {
	client := &askClient{formErr: errors.New("method not supported"), permPick: "Blue"}
	resp, c := runAskUserQuestion(t, client, true)
	if resp.Behavior != "allow" {
		t.Fatalf("behavior = %q (%s)", resp.Behavior, resp.Message)
	}
	if answers := askAnswerOf(t, resp); answers["Which color?"] != "Blue" {
		t.Errorf("answers = %#v", answers)
	}
	if perm, form := c.calls(); form != 1 || perm != 1 {
		t.Errorf("calls: form=%d perm=%d, want fallback after form error", form, perm)
	}
}

func TestAskUserQuestionPermissionTier(t *testing.T) {
	client := &askClient{permPick: "Red"}
	resp, c := runAskUserQuestion(t, client, false)
	if resp.Behavior != "allow" {
		t.Fatalf("behavior = %q", resp.Behavior)
	}
	if answers := askAnswerOf(t, resp); answers["Which color?"] != "Red" {
		t.Errorf("answers = %#v", answers)
	}
	if perm, form := c.calls(); form != 0 || perm != 1 {
		t.Errorf("calls: form=%d perm=%d, want permission only", form, perm)
	}
}

func TestAskUserQuestionPermissionSkip(t *testing.T) {
	client := &askClient{permPick: "Skip"}
	resp, _ := runAskUserQuestion(t, client, false)
	if resp.Behavior != "allow" {
		t.Fatalf("behavior = %q", resp.Behavior)
	}
	if answers := askAnswerOf(t, resp); len(answers) != 0 {
		t.Errorf("skip should yield empty answers, got %#v", answers)
	}
}

func TestAskUserQuestionUnsupportedClientDenies(t *testing.T) {
	client := &askClient{formErr: errors.New("no form"), permErr: errors.New("no permissions")}
	resp, _ := runAskUserQuestion(t, client, true)
	if resp.Behavior != "deny" || !strings.Contains(resp.Message, "Could not present") {
		t.Fatalf("unsupported client should deny with reason, got %q (%s)", resp.Behavior, resp.Message)
	}
}

func TestAskUserQuestionInvalidInputDenies(t *testing.T) {
	agentSide, clientSide := net.Pipe()
	t.Cleanup(func() { _ = agentSide.Close(); _ = clientSide.Close() })
	conn := acp.NewAgentSideConnection(New(Options{}), agentSide, agentSide)
	_ = acp.NewClientSideConnection(&askClient{}, clientSide, clientSide)

	var out bytes.Buffer
	app := &approver{ctx: context.Background(), conn: conn, sid: "test", out: &streamWriter{w: &out}}
	var req controlRequest
	req.RequestID = "r1"
	req.Request.Subtype = "can_use_tool"
	req.Request.ToolName = "AskUserQuestion"
	req.Request.Input = json.RawMessage(`{"questions":[]}`)
	app.handle(req)
	if !strings.Contains(out.String(), `"deny"`) || !strings.Contains(out.String(), "no valid questions") {
		t.Fatalf("invalid input should deny, got %s", out.String())
	}
}

func TestToolCallTrackerCompletesAndDropsFailedStarts(t *testing.T) {
	tracker := newToolCallTracker()
	starts, refinements := 0, 0
	if err := tracker.emit("tool-1", func() error { starts++; return nil }, func() error { refinements++; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := tracker.emit("tool-1", func() error { starts++; return nil }, func() error { refinements++; return nil }); err != nil {
		t.Fatal(err)
	}
	if starts != 1 || refinements != 1 || !tracker.has("tool-1") {
		t.Fatalf("starts=%d refinements=%d active=%v", starts, refinements, tracker.has("tool-1"))
	}
	if !tracker.complete("tool-1") || tracker.has("tool-1") || tracker.complete("tool-1") {
		t.Fatal("completed tool call should be removed exactly once")
	}

	sentinel := errors.New("send failed")
	if err := tracker.emit("tool-2", func() error { return sentinel }, func() error { return nil }); !errors.Is(err, sentinel) {
		t.Fatalf("start error = %v", err)
	}
	if tracker.has("tool-2") {
		t.Fatal("failed tool-call start must not remain active")
	}
}

func TestPermissionMetadataForAlwaysAllow(t *testing.T) {
	req := controlRequestBody{
		ToolName:              "Bash",
		PermissionSuggestions: json.RawMessage(`[{"type":"addRules","rules":[{"toolName":"Bash","ruleContent":"npm *"}],"behavior":"allow","destination":"projectSettings"}]`),
	}
	meta := permissionMetadataForAlwaysAllow(req)
	changes, _ := meta["changes"].([]any)
	if len(changes) != 1 {
		t.Fatalf("metadata = %#v", meta)
	}
	change := changes[0].(map[string]any)
	if change["description"] != "Allow Bash calls matching npm *" {
		t.Fatalf("description = %q", change["description"])
	}
	lifetime := change["lifetime"].(map[string]any)
	if lifetime["scope"] != "persistent" || lifetime["storage"] != "project" {
		t.Fatalf("lifetime = %#v", lifetime)
	}
}
