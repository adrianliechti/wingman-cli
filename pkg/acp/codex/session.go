package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/adrianliechti/wingman-agent/pkg/code"
)

type session struct {
	id acp.SessionId

	mu                    sync.Mutex
	modelID               string
	effort                string
	mode                  string
	collaborationMode     string
	additionalDirectories []string
	currentTurnID         string
	cancelTurn            context.CancelFunc
	interruptPending      bool
	interruptIssued       bool
}

type turnStartResult struct {
	response turnStartResponse
	err      error
}

func newSession(id acp.SessionId, model, effort string, additionalDirectories []string) *session {
	return &session{
		id: id, modelID: model, effort: effort, mode: defaultModeID,
		collaborationMode:     defaultCollaborationMode,
		additionalDirectories: append([]string(nil), additionalDirectories...),
	}
}

func (s *session) interrupt(ctx context.Context, cc *codexClient) {
	s.mu.Lock()
	turnID := s.currentTurnID
	cancel := s.cancelTurn
	if cancel != nil && turnID == "" {
		s.interruptPending = true
	}
	if turnID != "" {
		s.interruptIssued = true
	}
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if turnID != "" {
		_ = cc.turnInterrupt(ctx, turnInterruptParams{ThreadID: string(s.id), TurnID: turnID})
	}
}

func (s *session) steer(ctx context.Context, cc *codexClient, prompt []acp.ContentBlock, messageID string) error {
	s.mu.Lock()
	turnID := s.currentTurnID
	s.mu.Unlock()
	if turnID == "" {
		return code.ErrNoActiveTurn
	}
	err := cc.turnSteer(ctx, turnSteerParams{
		ThreadID: string(s.id), ExpectedTurnID: turnID,
		ClientUserMessageID: messageID, Input: promptToInput(prompt),
	})
	return classifySteerError(err)
}

func classifySteerError(err error) error {
	var rpcErr *rpcError
	if err == nil || !errors.As(err, &rpcErr) {
		return err
	}
	switch {
	case rpcErr.Message == "no active turn to steer":
		return code.ErrNoActiveTurn
	case strings.HasPrefix(rpcErr.Message, "expected active turn id "):
		// The backend advanced to another turn before it processed this steer.
		// Nothing was accepted, so preserve the input as a normal follow-up.
		return code.ErrNoActiveTurn
	case strings.HasPrefix(rpcErr.Message, "cannot steer a "):
		return fmt.Errorf("%w: %s", code.ErrTurnNotSteerable, rpcErr.Message)
	default:
		return err
	}
}

func (s *session) runTurn(ctx context.Context, conn *acp.AgentSideConnection, cc *codexClient, clientCapabilities acp.ClientCapabilities, models []modelEntry, prompt []acp.ContentBlock) (acp.StopReason, *acp.Usage, error) {
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	threadID := string(s.id)

	s.mu.Lock()
	s.cancelTurn = cancel
	s.interruptPending = false
	s.interruptIssued = false
	model := s.modelID
	effort := s.effort
	mode := modeFor(s.mode)
	collaborationMode := s.collaborationMode
	additionalDirectories := append([]string(nil), s.additionalDirectories...)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.currentTurnID = ""
		s.cancelTurn = nil
		s.interruptPending = false
		s.interruptIssued = false
		s.mu.Unlock()
	}()

	disp := newEventDispatcher(turnCtx, conn, s.id)
	disp.planUpdates = clientCapabilities.PlanCapabilities != nil
	defer flushPendingPlans(ctx, disp)
	app := newApprover(turnCtx, conn, s.id, clientCapabilities)
	cc.setThreadHandlers(threadID, &threadHandlers{
		onNotification: func(method string, params json.RawMessage) {
			app.handleNotification(method)
			disp.handle(method, params)
		},
		onExecApproval:        app.handleExec,
		onFileApproval:        app.handleFile,
		onPermissionsApproval: app.handlePermissions,
		onElicitation:         app.handleElicitation,
	})
	defer cc.setThreadHandlers(threadID, nil)

	run := func(input []any) (turnCompleted, error) {
		params := turnStartParams{
			ThreadID:       threadID,
			Input:          input,
			ApprovalPolicy: mode.approvalPolicy,
			SandboxPolicy:  sandboxPolicyWithRoots(mode.sandboxPolicy, additionalDirectories),
		}
		if model != "" && model != "default" {
			params.Model = model
		}
		if effort != "" && effort != "default" {
			params.Effort = effort
		}

		startCtx, cancelStart := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		started := make(chan turnStartResult, 1)
		go func() {
			resp, err := cc.turnStart(startCtx, params)
			started <- turnStartResult{response: resp, err: err}
		}()

		var result turnStartResult
		select {
		case result = <-started:
			cancelStart()
		case <-turnCtx.Done():
			go interruptLateStartedTurn(cc, threadID, started, cancelStart)
			return turnCompleted{}, turnCtx.Err()
		}
		if result.err != nil {
			return turnCompleted{}, fmt.Errorf("turn/start: %w", result.err)
		}
		resp := result.response
		s.mu.Lock()
		interruptPending := s.interruptPending
		if !interruptPending {
			s.currentTurnID = resp.Turn.ID
		}
		s.mu.Unlock()
		if interruptPending || turnCtx.Err() != nil {
			go interruptTurnSoon(cc, threadID, resp.Turn.ID)
			return turnCompleted{}, context.Canceled
		}

		select {
		case <-turnCtx.Done():
			s.mu.Lock()
			interruptIssued := s.interruptIssued
			s.mu.Unlock()
			if !interruptIssued {
				go interruptTurnSoon(cc, threadID, resp.Turn.ID)
			}
			return turnCompleted{}, turnCtx.Err()
		case tc := <-disp.done:
			s.mu.Lock()
			s.currentTurnID = ""
			s.mu.Unlock()
			return tc, nil
		}
	}

	tc, err := run(promptToInput(prompt))
	if err != nil {
		if turnCtx.Err() != nil {
			return acp.StopReasonCancelled, disp.getUsage(), nil
		}
		return "", nil, err
	}
	if err := disp.getFailure(); err != nil {
		return "", nil, err
	}
	if stopReasonFor(tc.Turn.Status) != acp.StopReasonEndTurn || collaborationMode != planCollaborationMode {
		return stopReasonFor(tc.Turn.Status), disp.getUsage(), nil
	}

	plan := disp.takeCompletedPlan()
	if plan == nil || !requestPlanImplementation(turnCtx, conn, s.id, plan) {
		return acp.StopReasonEndTurn, disp.getUsage(), nil
	}
	if err := cc.threadSettingsUpdate(turnCtx, newThreadSettingsUpdate(threadID, model, effort, defaultCollaborationMode)); err != nil {
		return "", nil, fmt.Errorf("thread/settings/update: %w", err)
	}
	s.mu.Lock()
	s.collaborationMode = defaultCollaborationMode
	s.mu.Unlock()
	disp.update(acp.SessionUpdate{ConfigOptionUpdate: &acp.SessionConfigOptionUpdate{
		SessionUpdate: "config_option_update",
		ConfigOptions: buildConfigOptions(models, model, effort, defaultCollaborationMode),
	}})

	tc, err = run(promptToInput([]acp.ContentBlock{acp.TextBlock("Implement the approved plan.")}))
	if err != nil {
		if turnCtx.Err() != nil {
			return acp.StopReasonCancelled, disp.getUsage(), nil
		}
		return "", nil, err
	}
	if err := disp.getFailure(); err != nil {
		return "", nil, err
	}
	return stopReasonFor(tc.Turn.Status), disp.getUsage(), nil
}

func interruptLateStartedTurn(cc *codexClient, threadID string, started <-chan turnStartResult, cancelStart context.CancelFunc) {
	defer cancelStart()
	result := <-started
	if result.err == nil {
		interruptTurnSoon(cc, threadID, result.response.Turn.ID)
	}
}

func interruptTurnSoon(cc *codexClient, threadID, turnID string) {
	if turnID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = cc.turnInterrupt(ctx, turnInterruptParams{ThreadID: threadID, TurnID: turnID})
}

func flushPendingPlans(ctx context.Context, disp *eventDispatcher) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	disp.flushPendingPlanUpdates(cleanup)
}

func requestPlanImplementation(ctx context.Context, conn *acp.AgentSideConnection, sid acp.SessionId, plan *completedPlan) bool {
	const (
		implement = acp.PermissionOptionId("implement_plan")
		revise    = acp.PermissionOptionId("revise_plan")
	)
	id := acp.ToolCallId("plan-review:" + plan.itemID)
	start := acp.StartToolCall(
		id,
		"Implement this plan?",
		acp.WithStartKind(acp.ToolKindSwitchMode),
		acp.WithStartStatus(acp.ToolCallStatusPending),
		acp.WithStartRawInput(map[string]any{"plan": plan.text}),
	)
	if err := conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: sid, Update: start}); err != nil {
		return false
	}
	response, err := conn.RequestPermission(ctx, acp.RequestPermissionRequest{
		SessionId: sid,
		ToolCall: acp.ToolCallUpdate{
			ToolCallId: id,
			Title:      acp.Ptr("Implement this plan?"),
			Kind:       acp.Ptr(acp.ToolKindSwitchMode),
			Status:     acp.Ptr(acp.ToolCallStatusPending),
			RawInput:   map[string]any{"plan": plan.text},
		},
		Options: []acp.PermissionOption{
			{OptionId: implement, Name: "Yes, implement this plan", Kind: acp.PermissionOptionKindAllowOnce},
			{OptionId: revise, Name: "No, and tell Codex what to do differently", Kind: acp.PermissionOptionKindRejectOnce},
		},
	})
	approved := err == nil && response.Outcome.Selected != nil && response.Outcome.Selected.OptionId == implement
	output := "User kept the session in plan mode."
	if approved {
		output = "User approved the plan."
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_ = conn.SessionUpdate(finishCtx, acp.SessionNotification{
		SessionId: sid,
		Update: acp.UpdateToolCall(
			id,
			acp.WithUpdateStatus(acp.ToolCallStatusCompleted),
			acp.WithUpdateRawOutput(output),
		),
	})
	return approved
}

func sandboxPolicyWithRoots(policy any, roots []string) any {
	if len(roots) == 0 {
		return policy
	}
	original, ok := policy.(map[string]any)
	if !ok || original["type"] != "workspaceWrite" {
		return policy
	}
	copy := make(map[string]any, len(original))
	for key, value := range original {
		copy[key] = value
	}
	copy["writableRoots"] = append([]string(nil), roots...)
	return copy
}

func streamThreadHistory(ctx context.Context, conn *acp.AgentSideConnection, sid acp.SessionId, turns []rawTurn, toolOutputs map[string]string, planUpdates bool) {
	send := func(u acp.SessionUpdate) {
		if ctx.Err() != nil {
			return
		}
		_ = conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: sid, Update: u})
	}
	for _, turn := range turns {
		for _, raw := range turn.Items {
			replayItem(send, raw, toolOutputs, planUpdates)
		}
	}
}

func replayToolResult(send func(acp.SessionUpdate), id, status string, content []acp.ToolCallContent) {
	st := toolStatusFor(status)
	if st != acp.ToolCallStatusCompleted && st != acp.ToolCallStatusFailed {
		return
	}
	opts := []acp.ToolCallUpdateOpt{acp.WithUpdateStatus(st)}
	if len(content) > 0 {
		opts = append(opts, acp.WithUpdateContent(content))
	}
	send(acp.UpdateToolCall(acp.ToolCallId(id), opts...))
}

func replayToolText(send func(acp.SessionUpdate), id, status string, outputs map[string]string) {
	out := outputs[id]
	if out == "" {
		return
	}
	replayToolResult(send, id, status, []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(out))})
}

func replayItem(send func(acp.SessionUpdate), raw json.RawMessage, toolOutputs map[string]string, planUpdates bool) {
	var probe struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if json.Unmarshal(raw, &probe) != nil || probe.Type == "" {
		return
	}
	switch probe.Type {
	case "userMessage":
		var it struct {
			Content []json.RawMessage `json:"content"`
		}
		_ = json.Unmarshal(raw, &it)
		for _, input := range it.Content {
			if block, ok := userInputToBlock(input); ok {
				send(userMessageUpdate(block, probe.ID))
			}
		}

	case "agentMessage":
		var it struct {
			Text  string `json:"text"`
			Phase string `json:"phase"`
		}
		_ = json.Unmarshal(raw, &it)
		if it.Text != "" {
			send(agentMessageUpdate(it.Text, probe.ID, it.Phase))
		}

	case "reasoning":
		var it struct {
			Summary []string `json:"summary"`
			Content []string `json:"content"`
		}
		_ = json.Unmarshal(raw, &it)
		if text := joinReasoning(it.Summary, it.Content); text != "" {
			send(agentThoughtUpdate(text, probe.ID))
		}

	case "plan":
		var it struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(raw, &it)
		if it.Text != "" {
			if planUpdates {
				send(acp.SessionUpdate{PlanUpdate: &acp.SessionPlanUpdate{
					SessionUpdate: "plan_update",
					Plan:          acp.NewPlanUpdateContentMarkdown(acp.PlanId(probe.ID), it.Text),
				}})
			} else {
				send(acp.UpdateAgentMessageText("Plan:\n" + it.Text))
			}
		}

	case "commandExecution":
		var it struct {
			Command        string          `json:"command"`
			Cwd            string          `json:"cwd"`
			Status         string          `json:"status"`
			CommandActions []commandAction `json:"commandActions"`
		}
		_ = json.Unmarshal(raw, &it)
		if title, kind, locs, ok := commandActionToolCall(it.CommandActions); ok {
			opts := []acp.ToolCallStartOpt{
				acp.WithStartKind(kind),
				acp.WithStartStatus(toolStatusFor(it.Status)),
			}
			if len(locs) > 0 {
				opts = append(opts, acp.WithStartLocations(locs))
			}
			send(acp.StartToolCall(acp.ToolCallId(probe.ID), title, opts...))
			break
		}
		title := stripShellPrefix(it.Command)
		if title == "" {
			title = "Run command"
		}
		send(acp.StartToolCall(acp.ToolCallId(probe.ID), title,
			acp.WithStartKind(acp.ToolKindExecute),
			acp.WithStartStatus(toolStatusFor(it.Status)),
			acp.WithStartRawInput(map[string]any{"command": title, "cwd": it.Cwd}),
		))
		replayToolText(send, probe.ID, it.Status, toolOutputs)

	case "fileChange":
		var it struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(raw, &it)
		send(acp.StartToolCall(acp.ToolCallId(probe.ID), "Editing files",
			acp.WithStartKind(acp.ToolKindEdit),
			acp.WithStartStatus(toolStatusFor(it.Status)),
		))
		if content := fileChangeContent(raw); len(content) > 0 {
			replayToolResult(send, probe.ID, it.Status, content)
		}

	case "mcpToolCall":
		var it struct {
			Server string          `json:"server"`
			Tool   string          `json:"tool"`
			Args   json.RawMessage `json:"arguments"`
			Status string          `json:"status"`
		}
		_ = json.Unmarshal(raw, &it)
		var args map[string]any
		_ = json.Unmarshal(it.Args, &args)
		send(acp.StartToolCall(acp.ToolCallId(probe.ID), fmt.Sprintf("mcp.%s.%s", it.Server, it.Tool),
			acp.WithStartKind(acp.ToolKindExecute),
			acp.WithStartStatus(toolStatusFor(it.Status)),
			acp.WithStartRawInput(map[string]any{"server": it.Server, "tool": it.Tool, "arguments": args}),
		))
		replayToolText(send, probe.ID, it.Status, toolOutputs)

	case "dynamicToolCall":
		var it struct {
			Tool   string          `json:"tool"`
			Args   json.RawMessage `json:"arguments"`
			Status string          `json:"status"`
		}
		_ = json.Unmarshal(raw, &it)
		var args map[string]any
		_ = json.Unmarshal(it.Args, &args)
		send(acp.StartToolCall(acp.ToolCallId(probe.ID), it.Tool,
			acp.WithStartKind(acp.ToolKindExecute),
			acp.WithStartStatus(toolStatusFor(it.Status)),
			acp.WithStartRawInput(map[string]any{"arguments": args}),
		))
		replayToolText(send, probe.ID, it.Status, toolOutputs)

	case "webSearch":
		send(webSearchStartToolCall(raw, acp.ToolCallStatusCompleted))
		replayToolText(send, probe.ID, "completed", toolOutputs)

	case "imageView":
		if u, ok := imageViewToolCall(raw); ok {
			send(u)
		}

	case "imageGeneration":
		if u, ok := imageGenToolCall(raw); ok {
			send(u)
		}

	case "collabAgentToolCall":
		if u, ok := collabStartToolCall(raw); ok {
			send(u)
		}

	case "subAgentActivity":
		if u, ok := subAgentStartToolCall(raw, acp.ToolCallStatusCompleted); ok {
			send(u)
		}

	case "contextCompaction":
		send(acp.StartToolCall(acp.ToolCallId(probe.ID), "Context compacted",
			acp.WithStartKind(acp.ToolKindOther),
			acp.WithStartStatus(acp.ToolCallStatusCompleted),
		))

	case "exitedReviewMode":
		var it struct {
			Review string `json:"review"`
		}
		_ = json.Unmarshal(raw, &it)
		if text := strings.TrimSpace(it.Review); text != "" {
			send(acp.UpdateAgentMessageText(text + "\n\n"))
		}
	}
}

func userInputToBlock(raw json.RawMessage) (acp.ContentBlock, bool) {
	var probe struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &probe) != nil {
		return acp.ContentBlock{}, false
	}
	switch probe.Type {
	case "text":
		var it struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(raw, &it)
		if it.Text == "" {
			return acp.ContentBlock{}, false
		}
		return acp.TextBlock(it.Text), true
	case "image":
		var it struct {
			URL string `json:"url"`
		}
		_ = json.Unmarshal(raw, &it)
		return acp.TextBlock(formatURIAsLink("image", it.URL)), true
	case "localImage":
		var it struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(raw, &it)
		uri := it.Path
		if !strings.HasPrefix(uri, "file://") {
			uri = "file://" + uri
		}
		return acp.TextBlock(formatURIAsLink("", uri)), true
	case "skill":
		var it struct {
			Name string `json:"name"`
			Path string `json:"path"`
		}
		_ = json.Unmarshal(raw, &it)
		return acp.TextBlock(fmt.Sprintf("skill:%s (%s)", it.Name, it.Path)), true
	}
	return acp.ContentBlock{}, false
}

func stopReasonFor(status string) acp.StopReason {
	switch status {
	case "interrupted":
		return acp.StopReasonCancelled
	default:
		return acp.StopReasonEndTurn
	}
}

func promptToInput(blocks []acp.ContentBlock) []any {
	out := make([]any, 0, len(blocks))
	for _, b := range blocks {
		switch {
		case b.Text != nil:
			out = append(out, textInput(b.Text.Text))
		case b.Image != nil:
			img := b.Image
			switch {
			case img.Uri != nil && *img.Uri != "":
				out = append(out, map[string]any{"type": "image", "url": *img.Uri})
			case img.Data != "":
				out = append(out, map[string]any{
					"type": "image",
					"url":  "data:" + img.MimeType + ";base64," + img.Data,
				})
			}
		case b.ResourceLink != nil:
			out = append(out, textInput(formatURIAsLink(b.ResourceLink.Name, b.ResourceLink.Uri)))
		case b.Resource != nil:
			if text, uri := resourceContents(b.Resource.Resource); text != "" {
				link := formatURIAsLink("", uri)
				body := fmt.Sprintf("%s\n<context ref=%q>\n%s\n</context>", link, uri, text)
				out = append(out, textInput(body))
			}
		}
	}
	return out
}

func textInput(text string) map[string]any {
	return map[string]any{"type": "text", "text": text, "text_elements": []any{}}
}

func resourceContents(res any) (text, uri string) {
	b, err := json.Marshal(res)
	if err != nil {
		return "", ""
	}
	var v struct {
		Text string `json:"text"`
		URI  string `json:"uri"`
	}
	_ = json.Unmarshal(b, &v)
	return v.Text, v.URI
}

func formatURIAsLink(name, uri string) string {
	if name != "" {
		return fmt.Sprintf("[@%s](%s)", name, uri)
	}
	if p, ok := strings.CutPrefix(uri, "file://"); ok {
		return fmt.Sprintf("[@%s](%s)", path.Base(p), uri)
	}
	return uri
}
