package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/adrianliechti/wingman-agent/pkg/code"
)

type session struct {
	id acp.SessionId

	promptGate            chan struct{}
	mu                    sync.Mutex
	closed                bool
	closedCh              chan struct{}
	modelID               string
	effort                string
	mode                  string
	collaborationMode     string
	additionalDirectories []string
	currentTurnID         string
	cancelTurn            context.CancelFunc
	interruptPending      bool
	interruptDone         chan struct{}
	startCleanup          <-chan struct{}
}

func (s *session) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *session) markClosed() {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		close(s.closedCh)
	}
	cancel := s.cancelTurn
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

type turnStartResult struct {
	response turnStartResponse
	err      error
}

func newSession(id acp.SessionId, model, effort string, additionalDirectories []string) *session {
	return &session{
		id: id, modelID: model, effort: effort, mode: defaultModeID,
		promptGate: make(chan struct{}, 1), closedCh: make(chan struct{}),
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
	issue := turnID != "" && s.interruptDone == nil
	if issue {
		s.interruptDone = make(chan struct{})
	}
	done := s.interruptDone
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if issue {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		err := cc.turnInterrupt(ctx, turnInterruptParams{ThreadID: string(s.id), TurnID: turnID})
		retireTimedOutTurnControl(cc, "turn/interrupt", err)
		close(done)
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
	if s.closed {
		s.mu.Unlock()
		return acp.StopReasonCancelled, nil, nil
	}
	s.cancelTurn = cancel
	s.interruptPending = false
	s.interruptDone = nil
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
		s.interruptDone = nil
		s.mu.Unlock()
	}()

	disp := newEventDispatcher(turnCtx, conn, s.id)

	run := func(input []any) (_ turnCompleted, runErr error) {
		runCtx, cancelRun := context.WithCancel(turnCtx)
		defer cancelRun()
		// A cancelled prompt may have returned before turn/start identified its
		// backend turn. Finish that turn's bounded cleanup before starting another.
		s.mu.Lock()
		cleanup := s.startCleanup
		s.mu.Unlock()
		if cleanup != nil {
			select {
			case <-cleanup:
			case <-runCtx.Done():
				return turnCompleted{}, runCtx.Err()
			case <-cc.rpc.done:
				return turnCompleted{}, cc.rpc.closedError()
			}
		}
		stream := newTurnStream(runCtx)
		disp = newEventDispatcher(runCtx, conn, s.id)
		app := newApprover(runCtx, conn, s.id, clientCapabilities)
		handlers := &threadHandlers{
			onNotification: stream.enqueue,
			onExecApproval: func(ctx context.Context, p execApprovalParams) execApprovalResponse {
				approval, cancel := app.forRequest(ctx)
				defer cancel()
				if !stream.acceptsRequest(approval.ctx, p.TurnID) {
					return execApprovalResponse{Decision: "cancel"}
				}
				return approval.handleExec(p)
			},
			onFileApproval: func(ctx context.Context, p fileApprovalParams) fileApprovalResponse {
				approval, cancel := app.forRequest(ctx)
				defer cancel()
				if !stream.acceptsRequest(approval.ctx, p.TurnID) {
					return fileApprovalResponse{Decision: "cancel"}
				}
				return approval.handleFile(p)
			},
			onPermissionsApproval: func(ctx context.Context, p permissionsApprovalParams) permissionsApprovalResponse {
				approval, cancel := app.forRequest(ctx)
				defer cancel()
				if !stream.acceptsRequest(approval.ctx, p.TurnID) {
					return rejectPermissionsResponse()
				}
				return approval.handlePermissions(p)
			},
			onElicitation: func(ctx context.Context, p elicitationParams) elicitationResponse {
				approval, cancel := app.forRequest(ctx)
				defer cancel()
				if !stream.acceptsRequest(approval.ctx, p.TurnID) {
					return elicitationResponse{Action: "cancel"}
				}
				return approval.handleElicitation(p)
			},
		}
		cc.setThreadHandlers(threadID, handlers)
		defer cc.removeThreadHandlers(threadID, handlers)
		defer func() {
			cancelRun()
			s.mu.Lock()
			id, done := s.currentTurnID, s.interruptDone
			issue := runErr != nil && id != "" && done == nil
			if issue {
				done = make(chan struct{})
				s.interruptDone = done
				s.startCleanup = done
			}
			s.mu.Unlock()
			if issue {
				go func() {
					interruptTurnSoon(cc, threadID, id)
					close(done)
				}()
			}
			if done != nil && turnCtx.Err() != nil {
				<-done
			}
			s.mu.Lock()
			s.currentTurnID = ""
			s.interruptDone = nil
			s.mu.Unlock()
		}()
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
		cleanupLateStart := func() {
			done := make(chan struct{})
			s.mu.Lock()
			s.startCleanup = done
			s.mu.Unlock()
			go func() {
				defer close(done)
				interruptLateStartedTurn(cc, threadID, started, cancelStart)
			}()
		}
		select {
		case result = <-started:
			cancelStart()
		case <-turnCtx.Done():
			cleanupLateStart()
			return turnCompleted{}, turnCtx.Err()
		case err := <-stream.failed:
			cleanupLateStart()
			return turnCompleted{}, err
		}
		if result.err != nil {
			retireTimedOutTurnControl(cc, "turn/start", result.err)
			return turnCompleted{}, fmt.Errorf("turn/start: %w", result.err)
		}
		resp := result.response
		if resp.Turn.ID == "" {
			return turnCompleted{}, fmt.Errorf("turn/start returned an empty turn id")
		}
		stream.started(resp.Turn.ID)
		s.mu.Lock()
		interruptPending := s.interruptPending
		s.currentTurnID = resp.Turn.ID
		s.mu.Unlock()
		if interruptPending || turnCtx.Err() != nil {
			return turnCompleted{}, context.Canceled
		}

		for {
			event, err := stream.next(cc.rpc)
			if err != nil {
				return turnCompleted{}, err
			}
			if event.processed != nil {
				close(event.processed)
				continue
			}
			owned, err := stream.owns(event.rpcMessage)
			if err != nil {
				return turnCompleted{}, err
			}
			if !owned {
				continue
			}
			if event.Method == "thread/closed" {
				return turnCompleted{}, fmt.Errorf("codex thread %s closed during the turn", threadID)
			}
			disp.handle(event.Method, event.Params)
			if err := disp.getFailure(); err != nil {
				return turnCompleted{}, err
			}
			select {
			case tc := <-disp.done:
				if tc.Turn.Status != "completed" && tc.Turn.Status != "interrupted" {
					return turnCompleted{}, fmt.Errorf("turn/completed returned unexpected turn status %q", tc.Turn.Status)
				}
				return tc, nil
			default:
			}
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
	if plan == nil {
		return acp.StopReasonEndTurn, disp.getUsage(), nil
	}
	approved, err := requestPlanImplementation(turnCtx, conn, s.id, plan)
	if turnCtx.Err() != nil {
		return acp.StopReasonCancelled, disp.getUsage(), nil
	}
	if err != nil {
		return "", nil, err
	}
	if !approved {
		return acp.StopReasonEndTurn, disp.getUsage(), nil
	}
	if err := cc.threadSettingsUpdate(turnCtx, newThreadSettingsUpdate(threadID, model, effort, defaultCollaborationMode)); err != nil {
		return "", nil, fmt.Errorf("thread/settings/update: %w", err)
	}
	s.mu.Lock()
	s.collaborationMode = defaultCollaborationMode
	s.mu.Unlock()
	if err := notifyClient(turnCtx, conn, s.id, acp.SessionUpdate{ConfigOptionUpdate: &acp.SessionConfigOptionUpdate{
		SessionUpdate: "config_option_update",
		ConfigOptions: buildConfigOptions(models, model, effort, defaultCollaborationMode),
	}}); err != nil {
		return "", nil, err
	}

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
	} else {
		retireTimedOutTurnControl(cc, "turn/start", result.err)
	}
}

func retireTimedOutTurnControl(cc *codexClient, method string, err error) {
	if errors.Is(err, context.DeadlineExceeded) {
		// A timed-out start may still create an unidentified turn; a timed-out
		// interrupt may leave it running. Neither transport is safe to reuse.
		cc.rpc.close(fmt.Errorf("%s acknowledgement timed out: %w", method, err))
	}
}

func interruptTurnSoon(cc *codexClient, threadID, turnID string) {
	if turnID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := cc.turnInterrupt(ctx, turnInterruptParams{ThreadID: threadID, TurnID: turnID})
	retireTimedOutTurnControl(cc, "turn/interrupt", err)
}

func requestPlanImplementation(ctx context.Context, conn *acp.AgentSideConnection, sid acp.SessionId, plan *completedPlan) (bool, error) {
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
	if err := notifyClient(ctx, conn, sid, start); err != nil {
		return false, err
	}
	response, err := callClient(ctx, conn, func() (acp.RequestPermissionResponse, error) {
		return conn.RequestPermission(ctx, acp.RequestPermissionRequest{
			SessionId: sid,
			ToolCall: acp.ToolCallUpdate{
				ToolCallId: id,
				Title:      new("Implement this plan?"),
				Kind:       acp.Ptr(acp.ToolKindSwitchMode),
				Status:     acp.Ptr(acp.ToolCallStatusPending),
				RawInput:   map[string]any{"plan": plan.text},
			},
			Options: []acp.PermissionOption{
				{OptionId: implement, Name: "Yes, implement this plan", Kind: acp.PermissionOptionKindAllowOnce},
				{OptionId: revise, Name: "No, and tell Codex what to do differently", Kind: acp.PermissionOptionKindRejectOnce},
			},
		})
	})
	if err != nil {
		return false, err
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	approved := response.Outcome.Selected != nil && response.Outcome.Selected.OptionId == implement
	output := "User kept the session in plan mode."
	if approved {
		output = "User approved the plan."
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	err = notifyClient(finishCtx, conn, sid, acp.UpdateToolCall(
		id,
		acp.WithUpdateStatus(acp.ToolCallStatusCompleted),
		acp.WithUpdateRawOutput(output),
	))
	return approved, err
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
	maps.Copy(copy, original)
	copy["writableRoots"] = append([]string(nil), roots...)
	return copy
}

func streamThreadHistory(ctx context.Context, conn *acp.AgentSideConnection, sid acp.SessionId, turns []rawTurn, toolOutputs map[string]string) error {
	var sendErr error
	send := func(u acp.SessionUpdate) {
		if sendErr != nil {
			return
		}
		sendErr = notifyClient(ctx, conn, sid, u)
	}
	for _, turn := range turns {
		for _, raw := range turn.Items {
			replayItem(send, raw, toolOutputs)
			if sendErr != nil {
				return sendErr
			}
		}
	}
	return nil
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

func replayToolText(send func(acp.SessionUpdate), id, status string, outputs map[string]string, fallbacks ...string) {
	out := outputs[id]
	if out == "" {
		for _, fallback := range fallbacks {
			if strings.TrimSpace(fallback) != "" {
				out = fallback
				break
			}
		}
	}
	if out == "" {
		return
	}
	replayToolResult(send, id, status, []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(boundedToolOutput(out)))})
}

func replayItem(send func(acp.SessionUpdate), raw json.RawMessage, toolOutputs map[string]string) {
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
			send(acp.UpdateAgentMessageText("Plan:\n" + it.Text))
		}

	case "commandExecution":
		var it struct {
			Status           string `json:"status"`
			AggregatedOutput string `json:"aggregatedOutput"`
		}
		_ = json.Unmarshal(raw, &it)
		if u, ok := itemToolCallStart(raw, probe.ID, probe.Type, toolStatusFor(it.Status)); ok {
			send(u)
			replayToolText(send, probe.ID, it.Status, toolOutputs, it.AggregatedOutput)
		}

	case "fileChange":
		var it struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(raw, &it)
		opts := []acp.ToolCallStartOpt{
			acp.WithStartKind(acp.ToolKindEdit),
			acp.WithStartStatus(toolStatusFor(it.Status)),
		}
		opts = appendDisplayLocations(opts, fileChangeLocations(raw))
		send(acp.StartToolCall(acp.ToolCallId(probe.ID), "Edit files", opts...))
		if content := fileChangeContent(raw); len(content) > 0 {
			replayToolResult(send, probe.ID, it.Status, content)
		}

	case "mcpToolCall", "dynamicToolCall":
		var it struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(raw, &it)
		if u, ok := itemToolCallStart(raw, probe.ID, probe.Type, toolStatusFor(it.Status)); ok {
			send(u)
			replayToolText(send, probe.ID, it.Status, toolOutputs)
		}

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
		send(completedCompactionToolCall(probe.ID))

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
			resource := b.Resource.Resource
			switch {
			case resource.TextResourceContents != nil:
				r := resource.TextResourceContents
				link := formatURIAsLink("", r.Uri)
				body := fmt.Sprintf("%s\n<context ref=%q>\n%s\n</context>", link, r.Uri, r.Text)
				out = append(out, textInput(body))
			case resource.BlobResourceContents != nil:
				r := resource.BlobResourceContents
				mimeType := "application/octet-stream"
				if r.MimeType != nil && *r.MimeType != "" {
					mimeType = *r.MimeType
				}
				if strings.HasPrefix(strings.ToLower(mimeType), "image/") && r.Blob != "" {
					out = append(out, map[string]any{"type": "image", "url": "data:" + mimeType + ";base64," + r.Blob})
					break
				}
				link := formatURIAsLink("", r.Uri)
				body := fmt.Sprintf("%s\n<context ref=%q mimeType=%q encoding=%q>\n%s\n</context>", link, r.Uri, mimeType, "base64", r.Blob)
				out = append(out, textInput(body))
			}
		}
	}
	return out
}

func textInput(text string) map[string]any {
	return map[string]any{"type": "text", "text": text, "text_elements": []any{}}
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
