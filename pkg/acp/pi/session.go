package pi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

type session struct {
	id      acp.SessionId
	cwd     string
	proc    *process
	cleanup func()

	promptMu       sync.Mutex
	mu             sync.Mutex
	closed         bool
	models         []modelEntry
	currentModel   string
	thinkingLevels []string
	thinking       string

	cancelRequested atomic.Bool
	cancelTurn      context.CancelFunc
	closeOnce       sync.Once
}

func newSession(id acp.SessionId, cwd string, proc *process) *session {
	return &session{id: id, cwd: cwd, proc: proc, thinking: defaultThinkingLevel}
}

func (s *session) configOptions() []acp.SessionConfigOption {
	s.mu.Lock()
	defer s.mu.Unlock()
	return buildConfigOptions(s.models, s.currentModel, s.thinkingLevels, s.thinking)
}

func (s *session) refreshConfiguration(ctx context.Context) error {
	stateData, err := s.proc.getState(ctx)
	if err != nil {
		return err
	}
	state := parseState(stateData)

	levelsData, levelsErr := s.proc.getAvailableThinkingLevels(ctx)
	levels := parseAvailableThinkingLevels(levelsData)
	if levelsErr != nil || len(levels) == 0 {
		levels = append([]string(nil), fallbackThinkingLevels...)
	}

	s.mu.Lock()
	if current := state.currentModel(); current != "" {
		s.currentModel = current
	}
	s.thinkingLevels = levels
	s.thinking = state.thinking()
	s.mu.Unlock()
	return nil
}

func (s *session) close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		cancel := s.cancelTurn
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		s.proc.dispose()
		if s.cleanup != nil {
			s.cleanup()
		}
	})
}

func (s *session) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *session) cancel() {
	s.cancelRequested.Store(true)

	s.mu.Lock()
	cancel := s.cancelTurn
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

type turnResult struct {
	stop acp.StopReason
	err  error
}

func (s *session) runTurn(ctx context.Context, conn *acp.AgentSideConnection, prompt []acp.ContentBlock) (acp.StopReason, error) {
	s.cancelRequested.Store(false)

	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return acp.StopReasonCancelled, nil
	}
	s.cancelTurn = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.cancelTurn = nil
		s.mu.Unlock()
	}()

	message, images := promptToPi(prompt)

	t := &turn{
		ctx:       turnCtx,
		conn:      conn,
		sess:      s,
		done:      make(chan turnResult, 1),
		tools:     map[string]bool{},
		mutations: map[string]bool{},
		snapshots: map[string]fileSnapshot{},
	}

	s.proc.setHandler(t.handle)
	defer s.proc.setHandler(nil)

	go func() {
		err := s.proc.prompt(turnCtx, message, images)
		t.onPromptResult(err)
	}()

	select {
	case <-turnCtx.Done():
		abortCtx, abortCancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = s.proc.abort(abortCtx)
		abortCancel()
		return acp.StopReasonCancelled, nil
	case <-s.proc.done:
		return "", errors.New("pi process exited unexpectedly")
	case res := <-t.done:
		return res.stop, res.err
	}
}

type turn struct {
	ctx  context.Context
	conn *acp.AgentSideConnection
	sess *session

	done chan turnResult

	tools     map[string]bool
	mutations map[string]bool
	snapshots map[string]fileSnapshot
}

// fileSnapshot captures a file's contents before pi's edit/write tools mutate
// it, so tool completion can render a structured ACP diff — pi itself only
// reports diffs as plain strings. oldText nil means the file didn't exist yet.
type fileSnapshot struct {
	path    string
	oldText *string
}

func (t *turn) emit(u acp.SessionUpdate) {
	if t.ctx.Err() != nil {
		return
	}
	_ = t.conn.SessionUpdate(t.ctx, acp.SessionNotification{SessionId: t.sess.id, Update: u})
}

func (t *turn) resolve(stop acp.StopReason, err error) {
	select {
	case t.done <- turnResult{stop: stop, err: err}:
	default:
	}
}

func (t *turn) onPromptResult(err error) {
	if err == nil {
		return
	}
	if t.sess.cancelRequested.Load() {
		t.resolve(acp.StopReasonCancelled, nil)
		return
	}
	if isAuthError(err) {
		t.resolve("", acp.NewAuthRequired(nil))
		return
	}
	t.resolve("", err)
}

type piToolCall struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Arguments   json.RawMessage `json:"arguments"`
	PartialArgs string          `json:"partialArgs"`
}

func (t *turn) handle(raw json.RawMessage) {
	var probe struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &probe) != nil {
		return
	}

	switch probe.Type {
	case "message_update":
		t.handleMessageUpdate(raw)

	case "tool_execution_start":
		t.handleToolStart(raw)

	case "tool_execution_update":
		t.handleToolUpdate(raw)

	case "tool_execution_end":
		t.handleToolEnd(raw)

	case "extension_ui_request":
		t.handleExtensionUI(raw)

	case "auto_retry_start":
		t.emit(acp.UpdateAgentMessageText("Retrying...\n\n"))

	case "auto_retry_end":
		t.emit(acp.UpdateAgentMessageText("Retry finished, resuming.\n\n"))

	case "compaction_start":
		t.emit(acp.UpdateAgentMessageText("Context nearing limit, running automatic compaction...\n\n"))

	case "compaction_end":
		t.emit(acp.UpdateAgentMessageText("Automatic compaction finished.\n\n"))

	case "agent_settled":
		reason := acp.StopReasonEndTurn
		if t.sess.cancelRequested.Load() {
			reason = acp.StopReasonCancelled
		}
		t.resolve(reason, nil)
	}
}

func (t *turn) handleMessageUpdate(raw json.RawMessage) {
	var p struct {
		AssistantMessageEvent struct {
			Type         string      `json:"type"`
			Delta        string      `json:"delta"`
			ToolCall     *piToolCall `json:"toolCall"`
			ContentIndex int         `json:"contentIndex"`
			Partial      struct {
				Content []piToolCall `json:"content"`
			} `json:"partial"`
		} `json:"assistantMessageEvent"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return
	}

	ame := p.AssistantMessageEvent
	switch ame.Type {
	case "text_delta":
		if ame.Delta != "" {
			t.emit(acp.UpdateAgentMessageText(ame.Delta))
		}
	case "thinking_delta":
		if ame.Delta != "" {
			t.emit(acp.UpdateAgentThoughtText(ame.Delta))
		}
	case "toolcall_start", "toolcall_delta", "toolcall_end":
		tc := ame.ToolCall
		if tc == nil && ame.ContentIndex >= 0 && ame.ContentIndex < len(ame.Partial.Content) {
			tc = &ame.Partial.Content[ame.ContentIndex]
		}
		if tc == nil || tc.ID == "" {
			return
		}
		seen := t.tools[tc.ID]
		if !seen {
			t.tools[tc.ID] = true
		}
		if seen {
			return
		}
		args := toolArgs(tc)
		presentation := presentTool(tc.Name, args, t.sess.cwd)
		t.emit(acp.StartToolCall(
			acp.ToolCallId(tc.ID), presentation.title,
			startToolOptions(presentation, acp.ToolCallStatusPending)...,
		))
	}
}

func (t *turn) handleToolStart(raw json.RawMessage) {
	var p struct {
		ToolCallID string          `json:"toolCallId"`
		ToolName   string          `json:"toolName"`
		Args       json.RawMessage `json:"args"`
	}
	if json.Unmarshal(raw, &p) != nil || p.ToolCallID == "" {
		return
	}

	var args map[string]any
	_ = json.Unmarshal(p.Args, &args)
	presentation := presentTool(p.ToolName, args, t.sess.cwd)
	t.snapshotFileMutation(p.ToolName, p.ToolCallID, args, presentation.locations)

	seen := t.tools[p.ToolCallID]
	t.tools[p.ToolCallID] = true

	if !seen {
		t.emit(acp.StartToolCall(
			acp.ToolCallId(p.ToolCallID), presentation.title,
			startToolOptions(presentation, acp.ToolCallStatusInProgress)...,
		))
		return
	}

	opts := []acp.ToolCallUpdateOpt{
		acp.WithUpdateStatus(acp.ToolCallStatusInProgress),
		acp.WithUpdateTitle(presentation.title),
		acp.WithUpdateKind(presentation.kind),
	}
	if presentation.rawInput != nil {
		opts = append(opts, acp.WithUpdateRawInput(presentation.rawInput))
	} else if args != nil || len(presentation.locations) > 0 {
		// A streamed partial call may have exposed partialArgs. Replace it with
		// an empty object once the complete call is represented solely by a chip.
		opts = append(opts, acp.WithUpdateRawInput(map[string]any{}))
	}
	if len(presentation.locations) > 0 {
		opts = append(opts, acp.WithUpdateLocations(presentation.locations))
	}
	t.emit(acp.UpdateToolCall(acp.ToolCallId(p.ToolCallID), opts...))
}

func (t *turn) snapshotFileMutation(toolName, toolCallID string, args map[string]any, locs []acp.ToolCallLocation) {
	if toolName != "edit" && toolName != "write" {
		return
	}
	t.mutations[toolCallID] = true
	path := toolPath(args)
	if path == "" {
		return
	}
	snap := fileSnapshot{path: path}
	if data, err := os.ReadFile(absPath(path, t.sess.cwd)); err == nil {
		old := string(data)
		snap.oldText = &old
		if toolName == "edit" && len(locs) > 0 {
			for _, needle := range editOldTexts(args) {
				if line := findUniqueLine(old, needle); line != nil {
					locs[0].Line = line
					break
				}
			}
		}
	}
	t.snapshots[toolCallID] = snap
}

func (t *turn) handleToolUpdate(raw json.RawMessage) {
	var p struct {
		ToolCallID    string          `json:"toolCallId"`
		PartialResult json.RawMessage `json:"partialResult"`
	}
	if json.Unmarshal(raw, &p) != nil || p.ToolCallID == "" {
		return
	}
	t.ensureToolStarted(p.ToolCallID)

	opts := []acp.ToolCallUpdateOpt{acp.WithUpdateStatus(acp.ToolCallStatusInProgress)}
	if !t.mutations[p.ToolCallID] {
		if text := toolResultToText(p.PartialResult); text != "" {
			opts = append(opts, acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock(text))}))
		}
	}
	t.emit(acp.UpdateToolCall(acp.ToolCallId(p.ToolCallID), opts...))
}

func (t *turn) handleToolEnd(raw json.RawMessage) {
	var p struct {
		ToolCallID string          `json:"toolCallId"`
		Result     json.RawMessage `json:"result"`
		IsError    bool            `json:"isError"`
	}
	if json.Unmarshal(raw, &p) != nil || p.ToolCallID == "" {
		return
	}
	t.ensureToolStarted(p.ToolCallID)

	status := acp.ToolCallStatusCompleted
	if p.IsError {
		status = acp.ToolCallStatusFailed
	}

	var content []acp.ToolCallContent
	if snap, ok := t.snapshots[p.ToolCallID]; ok && !p.IsError {
		if data, err := os.ReadFile(absPath(snap.path, t.sess.cwd)); err == nil {
			newText := string(data)
			if snap.oldText == nil || newText != *snap.oldText {
				content = []acp.ToolCallContent{{Diff: &acp.ToolCallContentDiff{
					Type:    "diff",
					Path:    snap.path,
					OldText: snap.oldText,
					NewText: newText,
				}}}
			}
		}
	}
	if content == nil {
		if text := toolResultToText(p.Result); text != "" {
			content = []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(text))}
		}
	}

	opts := []acp.ToolCallUpdateOpt{acp.WithUpdateStatus(status)}
	if len(content) > 0 {
		opts = append(opts, acp.WithUpdateContent(content))
	}
	t.emit(acp.UpdateToolCall(acp.ToolCallId(p.ToolCallID), opts...))

	delete(t.tools, p.ToolCallID)
	delete(t.mutations, p.ToolCallID)
	delete(t.snapshots, p.ToolCallID)
}

func (t *turn) ensureToolStarted(id string) {
	seen := t.tools[id]
	if !seen {
		t.tools[id] = true
	}
	if !seen {
		t.emit(acp.StartToolCall(acp.ToolCallId(id), "Tool call",
			acp.WithStartKind(acp.ToolKindOther),
			acp.WithStartStatus(acp.ToolCallStatusInProgress),
		))
	}
}

func (t *turn) handleExtensionUI(raw json.RawMessage) {
	var p struct {
		ID         string   `json:"id"`
		Method     string   `json:"method"`
		Title      string   `json:"title"`
		Message    string   `json:"message"`
		NotifyType string   `json:"notifyType"`
		Options    []string `json:"options"`
	}
	if json.Unmarshal(raw, &p) != nil || p.ID == "" {
		return
	}

	switch p.Method {
	case "confirm":
		ok := t.askConfirm(p.ID, p.Title, p.Message)
		t.sess.proc.sendExtensionResponse(map[string]any{"id": p.ID, "confirmed": ok})

	case "select":
		if value, ok := t.askSelect(p.ID, p.Title, p.Options); ok {
			t.sess.proc.sendExtensionResponse(map[string]any{"id": p.ID, "value": value})
		} else {
			t.sess.proc.sendExtensionResponse(map[string]any{"id": p.ID, "cancelled": true})
		}

	case "notify":
		if p.Message != "" {
			u := acp.UpdateAgentMessageText(p.Message + "\n\n")
			level := p.NotifyType
			if level == "" {
				level = "info"
			}
			u.AgentMessageChunk.Meta = map[string]any{"piAcp": map[string]any{"notify": map[string]any{"level": level}}}
			t.emit(u)
		}

	case "input", "editor":
		// ACP has no free-form input primitive. Cancel dialog-style requests so
		// the extension can continue instead of waiting forever.
		t.sess.proc.sendExtensionResponse(map[string]any{"id": p.ID, "cancelled": true})

	default:
		// setStatus, setWidget, setTitle, and set_editor_text are fire-and-forget
		// in Pi RPC mode. Sending a response for them is a protocol violation.
	}
}

func (t *turn) askConfirm(id, title, message string) bool {
	tcTitle := title
	if tcTitle == "" {
		tcTitle = "Confirm"
	}
	status := acp.ToolCallStatusPending
	kind := acp.ToolKindOther
	tc := acp.ToolCallUpdate{
		ToolCallId: acp.ToolCallId("pi-ui-" + id),
		Title:      &tcTitle,
		Kind:       &kind,
		Status:     &status,
	}
	if message != "" {
		tc.Content = []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(message))}
	}

	resp, err := t.conn.RequestPermission(t.ctx, acp.RequestPermissionRequest{
		SessionId: t.sess.id,
		ToolCall:  tc,
		Options: []acp.PermissionOption{
			{OptionId: "yes", Name: "Yes", Kind: acp.PermissionOptionKindAllowOnce},
			{OptionId: "no", Name: "No", Kind: acp.PermissionOptionKindRejectOnce},
		},
	})
	if err != nil || resp.Outcome.Selected == nil {
		return false
	}
	return resp.Outcome.Selected.OptionId == "yes"
}

func (t *turn) askSelect(id, title string, options []string) (string, bool) {
	if len(options) == 0 {
		return "", false
	}
	tcTitle := title
	if tcTitle == "" {
		tcTitle = "Select"
	}
	status := acp.ToolCallStatusPending
	kind := acp.ToolKindOther
	tc := acp.ToolCallUpdate{
		ToolCallId: acp.ToolCallId("pi-ui-" + id),
		Title:      &tcTitle,
		Kind:       &kind,
		Status:     &status,
	}

	opts := make([]acp.PermissionOption, 0, len(options))
	for i, name := range options {
		opts = append(opts, acp.PermissionOption{
			OptionId: acp.PermissionOptionId(choiceID(i)),
			Name:     name,
			Kind:     acp.PermissionOptionKindAllowOnce,
		})
	}

	resp, err := t.conn.RequestPermission(t.ctx, acp.RequestPermissionRequest{
		SessionId: t.sess.id,
		ToolCall:  tc,
		Options:   opts,
	})
	if err != nil || resp.Outcome.Selected == nil {
		return "", false
	}
	for i := range options {
		if string(resp.Outcome.Selected.OptionId) == choiceID(i) {
			return options[i], true
		}
	}
	return "", false
}

func choiceID(i int) string {
	return "choice-" + strconv.Itoa(i)
}

func toolArgs(tc *piToolCall) map[string]any {
	if len(tc.Arguments) > 0 {
		var m map[string]any
		if json.Unmarshal(tc.Arguments, &m) == nil {
			return m
		}
	}
	if tc.PartialArgs != "" {
		var m map[string]any
		if json.Unmarshal([]byte(tc.PartialArgs), &m) == nil {
			return m
		}
		return map[string]any{"partialArgs": tc.PartialArgs}
	}
	return nil
}

func toolPath(args map[string]any) string {
	if args == nil {
		return ""
	}
	if p, ok := args["path"].(string); ok && p != "" {
		return p
	}
	if p, ok := args["file_path"].(string); ok && p != "" {
		return p
	}
	return ""
}

func toolLocations(args map[string]any, cwd string) []acp.ToolCallLocation {
	path := toolPath(args)
	if path == "" {
		return nil
	}
	if !filepath.IsAbs(path) && cwd != "" {
		path = filepath.Join(cwd, path)
	}
	location := acp.ToolCallLocation{Path: path}
	if offset, ok := tool.IntArg(args, "offset"); ok && offset > 0 {
		location.Line = new(offset)
	}
	return []acp.ToolCallLocation{location}
}

type toolPresentation struct {
	title     string
	kind      acp.ToolKind
	rawInput  any
	locations []acp.ToolCallLocation
}

func presentTool(name string, args map[string]any, cwd string) toolPresentation {
	kind := toolKind(name)
	locations := toolLocations(args, cwd)
	title := name
	omit := []string{}

	switch strings.ToLower(name) {
	case "read":
		title = "Read file"
		omit = append(omit, "path", "file_path", "offset")
	case "write":
		title = "Write file"
		omit = append(omit, "path", "file_path", "content")
	case "edit":
		title = "Edit file"
		omit = append(omit,
			"path", "file_path", "oldText", "newText", "old_string", "new_string",
			"replaceAll", "replace_all", "edits",
		)
	case "bash", "shell", "exec_command":
		title = "Run command"
	case "grep":
		title = "Search files"
		omit = append(omit, "path", "file_path")
	case "glob":
		title = "Find files"
		omit = append(omit, "path", "file_path")
	case "view_image":
		title = "View image"
		omit = append(omit, "path", "file_path")
	}

	if title == "" {
		title = "Tool call"
	}
	if len(locations) > 0 && len(omit) == 0 {
		omit = append(omit, "path", "file_path")
	}
	return toolPresentation{
		title: title, kind: kind,
		rawInput: compactToolArgs(args, omit...), locations: locations,
	}
}

func compactToolArgs(args map[string]any, omit ...string) any {
	if len(args) == 0 {
		return nil
	}
	display := make(map[string]any, len(args))
	for key, value := range args {
		display[key] = value
	}
	for _, key := range omit {
		delete(display, key)
	}
	if len(display) == 0 {
		return nil
	}
	return display
}

func startToolOptions(p toolPresentation, status acp.ToolCallStatus) []acp.ToolCallStartOpt {
	opts := []acp.ToolCallStartOpt{
		acp.WithStartKind(p.kind),
		acp.WithStartStatus(status),
	}
	if len(p.locations) > 0 {
		opts = append(opts, acp.WithStartLocations(p.locations))
	}
	// Apply rawInput after locations to undo the SDK's synthetic path mirror.
	if p.rawInput != nil || len(p.locations) > 0 {
		opts = append(opts, acp.WithStartRawInput(p.rawInput))
	}
	return opts
}

func absPath(path, cwd string) string {
	if filepath.IsAbs(path) || cwd == "" {
		return path
	}
	return filepath.Join(cwd, path)
}

func editOldTexts(args map[string]any) []string {
	var out []string
	if s, ok := args["oldText"].(string); ok && s != "" {
		out = append(out, s)
	}
	edits := args["edits"]
	if s, ok := edits.(string); ok {
		var arr []any
		if json.Unmarshal([]byte(s), &arr) == nil {
			edits = arr
		}
	}
	if arr, ok := edits.([]any); ok {
		for _, e := range arr {
			if m, ok := e.(map[string]any); ok {
				if s, ok := m["oldText"].(string); ok && s != "" {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

func findUniqueLine(text, needle string) *int {
	if needle == "" {
		return nil
	}
	first := strings.Index(text, needle)
	if first < 0 || strings.Contains(text[first+1:], needle) {
		return nil
	}
	line := 1 + strings.Count(text[:first], "\n")
	return &line
}

func toolKind(name string) acp.ToolKind {
	switch strings.ToLower(name) {
	case "read":
		return acp.ToolKindRead
	case "write", "edit":
		return acp.ToolKindEdit
	case "bash", "shell", "exec_command":
		return acp.ToolKindExecute
	case "grep", "glob":
		return acp.ToolKindSearch
	case "view_image":
		return acp.ToolKindRead
	default:
		return acp.ToolKindOther
	}
}

func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "auth") && strings.Contains(msg, "required") ||
		strings.Contains(msg, "api key") ||
		strings.Contains(msg, "no models")
}

type piImage struct {
	Type     string `json:"type"`
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

func promptToPi(blocks []acp.ContentBlock) (string, []piImage) {
	var b strings.Builder
	var images []piImage

	for _, block := range blocks {
		switch {
		case block.Text != nil:
			b.WriteString(block.Text.Text)

		case block.Image != nil:
			img := block.Image
			data := img.Data
			mime := img.MimeType
			if data == "" && img.Uri != nil {
				if m, d, ok := splitDataURL(*img.Uri); ok {
					mime, data = m, d
				}
			}
			if data != "" {
				images = append(images, piImage{Type: "image", MimeType: mime, Data: data})
			}

		case block.ResourceLink != nil:
			b.WriteString("\n[Context] " + block.ResourceLink.Uri)

		case block.Resource != nil:
			resource := block.Resource.Resource
			switch {
			case resource.TextResourceContents != nil:
				r := resource.TextResourceContents
				b.WriteString("\n[Embedded Context] " + r.Uri + "\n" + r.Text)
			case resource.BlobResourceContents != nil:
				r := resource.BlobResourceContents
				mimeType := "application/octet-stream"
				if r.MimeType != nil && *r.MimeType != "" {
					mimeType = *r.MimeType
				}
				if strings.HasPrefix(strings.ToLower(mimeType), "image/") && r.Blob != "" {
					images = append(images, piImage{Type: "image", MimeType: mimeType, Data: r.Blob})
					break
				}
				b.WriteString("\n[Embedded Context] " + r.Uri + " (" + mimeType + ", base64)\n" + r.Blob)
			}
		}
	}

	return b.String(), images
}

func splitDataURL(s string) (mime, data string, ok bool) {
	rest, found := strings.CutPrefix(s, "data:")
	if !found {
		return "", "", false
	}
	mime, data, ok = strings.Cut(rest, ";base64,")
	return mime, data, ok
}

func toolResultToText(result json.RawMessage) string {
	if len(result) == 0 {
		return ""
	}

	var r struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		Output   string `json:"output"`
		ExitCode *int   `json:"exitCode"`
		Details  struct {
			Diff     string `json:"diff"`
			Stdout   string `json:"stdout"`
			Stderr   string `json:"stderr"`
			Output   string `json:"output"`
			ExitCode *int   `json:"exitCode"`
		} `json:"details"`
	}
	if json.Unmarshal(result, &r) != nil {
		return string(result)
	}

	if strings.TrimSpace(r.Details.Diff) != "" {
		return r.Details.Diff
	}

	var texts []string
	for _, c := range r.Content {
		if c.Type == "text" && c.Text != "" {
			texts = append(texts, c.Text)
		}
	}
	if len(texts) > 0 {
		return strings.Join(texts, "")
	}

	stdout := firstNonEmpty(r.Details.Stdout, r.Stdout, r.Details.Output, r.Output)
	stderr := firstNonEmpty(r.Details.Stderr, r.Stderr)
	exit := r.ExitCode
	if exit == nil {
		exit = r.Details.ExitCode
	}

	if strings.TrimSpace(stdout) != "" || strings.TrimSpace(stderr) != "" {
		var parts []string
		if strings.TrimSpace(stdout) != "" {
			parts = append(parts, stdout)
		}
		if strings.TrimSpace(stderr) != "" {
			parts = append(parts, "stderr:\n"+stderr)
		}
		if exit != nil {
			parts = append(parts, "exit code: "+strconv.Itoa(*exit))
		}
		return strings.TrimRight(strings.Join(parts, "\n\n"), "\n")
	}

	return string(result)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
