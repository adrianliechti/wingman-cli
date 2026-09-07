package server

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/google/uuid"
)

// TranscriptEntry is the visible Wingman model. Provider messages, retry
// bookkeeping, hidden instructions and journal events never cross this boundary.
type TranscriptEntry struct {
	ID            string               `json:"id"`
	Type          string               `json:"type"`
	Content       string               `json:"content"`
	MessageID     string               `json:"messageId,omitempty"`
	InputID       string               `json:"inputId,omitempty"`
	Files         []string             `json:"files,omitempty"`
	Images        []string             `json:"images,omitempty"`
	ToolID        string               `json:"toolId,omitempty"`
	ToolName      string               `json:"toolName,omitempty"`
	ToolKind      string               `json:"toolKind,omitempty"`
	ToolArgs      string               `json:"toolArgs,omitempty"`
	ToolLocations []agent.ToolLocation `json:"toolLocations,omitempty"`
	ToolHint      string               `json:"toolHint,omitempty"`
	ToolResult    *string              `json:"toolResult,omitempty"`
	ToolPartial   bool                 `json:"toolPartial,omitempty"`
	ReasoningID   string               `json:"reasoningId,omitempty"`
}

type PromptView struct {
	ID      string             `json:"id"`
	Kind    string             `json:"kind"`
	Message string             `json:"message"`
	Fields  []tool.ElicitField `json:"fields,omitempty"`
}

type SessionSettings struct {
	Models    []modelOption `json:"models"`
	Model     string        `json:"model"`
	Effort    string        `json:"effort"`
	Efforts   []string      `json:"efforts"`
	Mode      string        `json:"mode"`
	Modes     []modeOption  `json:"modes"`
	CanDelete bool          `json:"canDelete"`
}

type modelOption struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

type UsageView struct {
	InputTokens     int64 `json:"inputTokens"`
	CachedTokens    int64 `json:"cachedTokens"`
	OutputTokens    int64 `json:"outputTokens"`
	LastInputTokens int64 `json:"lastInputTokens"`
	ContextWindow   int64 `json:"contextWindow"`
}

type SessionState struct {
	Status        string            `json:"status"`
	Phase         string            `json:"phase"`
	Error         *string           `json:"error"`
	Usage         UsageView         `json:"usage"`
	Prompts       []PromptView      `json:"prompts"`
	PendingInputs []TurnQueueEntry  `json:"pendingInputs"`
	QueuePaused   bool              `json:"queuePaused"`
	CanSteer      bool              `json:"canSteer"`
	Settings      SessionSettings   `json:"settings"`
	ToolProgress  map[string]string `json:"toolProgress"`
}

type SessionChange struct {
	Type    string            `json:"type"`
	Entry   *TranscriptEntry  `json:"entry,omitempty"`
	Entries []TranscriptEntry `json:"entries,omitempty"`
	ID      string            `json:"id,omitempty"`
	Text    string            `json:"text,omitempty"`
	IDs     []string          `json:"ids,omitempty"`
	State   *SessionState     `json:"state,omitempty"`
}

type sessionController struct {
	metadataMu sync.Mutex
	receiptMu  sync.Mutex
	backend    *backendRuntime
	ref        SessionRef
	epoch      string
	// opMu serializes load and HTTP commands, never stream callbacks.
	opMu           sync.Mutex
	loaded         bool
	deleted        bool
	receipts       map[string]*commandReceipt
	deliveredTasks map[string]bool
	// mu protects the complete projection AND subscription registration/publication.
	mu          sync.Mutex
	revision    uint64
	entries     []TranscriptEntry
	state       SessionState
	subscribers map[sessionSubscription]struct{}
	prompts     map[string]pendingPrompt
	confirmAll  bool
	textID      string
	reasoningID string
	attempt     map[string]bool
}

func newSessionController(b *backendRuntime, id string) *sessionController {
	return &sessionController{backend: b, ref: SessionRef{b.scope.WorkspaceID, b.id, id}, epoch: uuid.NewString(), receipts: map[string]*commandReceipt{}, deliveredTasks: map[string]bool{}, entries: []TranscriptEntry{}, subscribers: map[sessionSubscription]struct{}{}, prompts: map[string]pendingPrompt{}, attempt: map[string]bool{}, state: SessionState{Status: "loading", Phase: "idle", Prompts: []PromptView{}, PendingInputs: []TurnQueueEntry{}, ToolProgress: map[string]string{}, Settings: SessionSettings{Models: []modelOption{}, Efforts: []string{}, Modes: []modeOption{}}}}
}

func (c *sessionController) load() {
	if !c.backend.beginOperation() {
		return
	}
	defer c.backend.operations.Done()
	c.opMu.Lock()
	defer c.opMu.Unlock()
	_ = c.loadLocked()
}

func (c *sessionController) loadLocked() error {
	if err := c.backend.ctx.Err(); err != nil {
		return err
	}
	if c.deleted {
		return errors.New("session deleted")
	}
	if c.loaded {
		return nil
	}
	err := c.backend.agent.LoadSession(code.WithSessionID(c.backend.ctx, c.ref.SessionID), c.ref.SessionID)
	if err != nil {
		c.mu.Lock()
		message := err.Error()
		c.state.Status = "error"
		c.state.Error = &message
		c.publishStateLocked()
		c.mu.Unlock()
		return err
	}
	c.loaded = true
	c.replaceHistory()
	c.refreshSettings()
	c.mu.Lock()
	c.state.Status = "ready"
	c.state.Error = nil
	c.publishStateLocked()
	c.mu.Unlock()
	c.backend.sendTurnSnapshot(c.ref.SessionID)
	c.backend.ensureTaskPump(c.ref.SessionID)
	return nil
}

func (b *backendRuntime) settings(sid string) SessionSettings {
	models, model := b.agent.Models(sid)
	effort, efforts := b.agent.Effort(sid)
	modes, mode := b.agent.Modes(sid)
	state := SessionSettings{Model: model, Effort: effort, Efforts: append([]string{}, efforts...), Mode: mode, Modes: toModeState(modes, mode).Modes, Models: []modelOption{}, CanDelete: supportsDelete(b.agent)}
	for _, m := range models {
		state.Models = append(state.Models, modelOption{m.ID, m.Name, m.Namespace})
	}
	return state
}

func (c *sessionController) refreshSettings() {
	if !c.backend.beginOperation() {
		return
	}
	defer c.backend.operations.Done()
	c.metadataMu.Lock()
	defer c.metadataMu.Unlock()
	settings := c.backend.settings(c.ref.SessionID)
	usage := c.backend.agent.Usage(c.ref.SessionID)
	if usage.ContextWindow <= 0 && usage.LastInputTokens > 0 {
		usage.ContextWindow = int64(agent.ContextWindowFor(settings.Model))
	}
	u := UsageView{usage.InputTokens, usage.CacheReadInputTokens, usage.OutputTokens, usage.LastInputTokens, usage.ContextWindow}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.Status == "deleted" {
		return
	}
	if reflect.DeepEqual(c.state.Settings, settings) && c.state.Usage == u {
		return
	}
	c.state.Settings = settings
	c.state.Usage = u
	c.publishStateLocked()
}

func (c *sessionController) publishStateLocked() {
	c.publishLocked(SessionChange{Type: "state.replace", State: &c.state})
}

func (c *sessionController) replaceHistory() {
	entries := transcriptEntries(c.backend.agent.Messages(c.ref.SessionID))
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.Status == "deleted" {
		return
	}
	c.entries = entries
	c.textID = ""
	c.reasoningID = ""
	c.attempt = map[string]bool{}
	// Explicit authoritative replacement at load/commit boundaries. No client
	// tries to match streamed text against history by content or timing.
	c.publishLocked(SessionChange{Type: "entries.replace", Entries: entries})
}

func (b *backendRuntime) sendSession(sid string, f Frame) {
	if sid == "" {
		return
	}
	if f.Type == EvtTasksChanged {
		f.Session = sid
		f.Backend = b.id
		b.Server.send(f)
		return
	}
	b.session(sid).apply(f)
}

func (b *backendRuntime) setSessionPhase(sid, phase string) {
	b.sendSession(sid, Frame{Type: EvtPhase, Phase: phase})
}

func (c *sessionController) index(id string) int {
	return slices.IndexFunc(c.entries, func(e TranscriptEntry) bool { return e.ID == id })
}

func (c *sessionController) upsertLocked(e TranscriptEntry) {
	if i := c.index(e.ID); i >= 0 {
		c.entries[i] = e
	} else {
		c.entries = append(c.entries, e)
	}
	c.publishLocked(SessionChange{Type: "entry.upsert", Entry: &e})
}

func (c *sessionController) apply(f Frame) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.Status == "deleted" {
		return
	}
	switch f.Type {
	case EvtPhase:
		if c.state.Phase == f.Phase {
			return
		}
		c.state.Phase = f.Phase
	case EvtError:
		c.state.Error = &f.Message
	case EvtToolProgress:
		c.state.ToolProgress[f.ID] = f.Text
	case EvtTurnInput:
		if f.Input != nil && (f.Input.State == string(code.TurnInputActive) || f.Input.State == string(code.TurnInputSteered)) {
			c.state.Error = nil
			if f.Input.Origin != "task" && (f.Input.Text != "" || len(f.Input.Files)+len(f.Input.Images) > 0) {
				c.upsertLocked(TranscriptEntry{ID: "input:" + f.Input.ID, InputID: f.Input.ID, Type: "user", Content: f.Input.Text, Files: f.Input.Files, Images: f.Input.Images})
			}
			c.textID = ""
			c.reasoningID = ""
		}
	case EvtTextDelta, EvtReasoningDelta:
		typ, current := "assistant", &c.textID
		if f.Type == EvtReasoningDelta {
			typ = "reasoning"
			current = &c.reasoningID
		}
		i := c.index(*current)
		if i < 0 || (f.ID != "" && c.entries[i].MessageID != f.ID) {
			id := uuid.NewString()
			if f.ID != "" {
				id = typ + ":" + f.ID
			}
			e := TranscriptEntry{ID: id, Type: typ, Content: f.Text, MessageID: f.ID}
			if typ == "reasoning" {
				e.ReasoningID = f.ID
			}
			*current = e.ID
			c.attempt[e.ID] = true
			c.upsertLocked(e)
		} else {
			c.entries[i].Content += f.Text
			c.publishLocked(SessionChange{Type: "entry.append", ID: c.entries[i].ID, Text: f.Text})
		}
		return
	case EvtToolCall, EvtToolResult:
		c.textID = ""
		c.reasoningID = ""
		id := "tool:" + f.ID
		if f.ID == "" {
			id = uuid.NewString()
		} // adapters should supply IDs; never match by text/name
		e := TranscriptEntry{ID: id, Type: "tool", ToolID: f.ID, ToolName: f.Name, ToolKind: f.Kind, ToolArgs: f.Args, ToolLocations: f.Locations, ToolHint: f.Hint, ToolPartial: f.Partial}
		if i := c.index(id); i >= 0 {
			e.ToolResult = c.entries[i].ToolResult
		}
		if f.Type == EvtToolResult {
			e.ToolResult = &f.Content
			delete(c.state.ToolProgress, f.ID)
		}
		c.upsertLocked(e)
	case EvtStreamReset:
		ids := []string{}
		c.entries = slices.DeleteFunc(c.entries, func(e TranscriptEntry) bool {
			if c.attempt[e.ID] || (e.Type == "tool" && e.ToolPartial) {
				ids = append(ids, e.ID)
				return true
			}
			return false
		})
		c.attempt = map[string]bool{}
		c.textID = ""
		c.reasoningID = ""
		c.publishLocked(SessionChange{Type: "entries.remove", IDs: ids})
		return
	default:
		return
	}
	c.publishStateLocked()
}

func visibleInput(input code.TurnInput) code.TurnInputDisplay {
	if input.Display != nil {
		return *input.Display
	}
	// Legacy disk reader: infer the original visible envelope once, at the Go
	// boundary. Hidden task/skill content is never rendered as user input.
	var out code.TurnInputDisplay
	var texts []string
	for _, part := range input.Content {
		if part.Hidden {
			continue
		}
		if strings.HasPrefix(part.Text, "[File: ") && strings.HasSuffix(part.Text, "]") && !strings.Contains(part.Text, "\n") {
			out.Files = append(out.Files, strings.TrimSuffix(strings.TrimPrefix(part.Text, "[File: "), "]"))
		} else if part.Text != "" {
			texts = append(texts, part.Text)
		}
		if part.File != nil && part.File.Data != "" {
			out.Images = append(out.Images, part.File.Data)
		}
	}
	out.Text = strings.Join(texts, "\n")
	return out
}

func transcriptEntries(messages []agent.Message) []TranscriptEntry {
	entries := []TranscriptEntry{}
	for mi, m := range convertMessages(messages) {
		if m.Role == "user" {
			input := code.TurnInput{}
			for _, part := range m.Content {
				if part.Text != "" {
					input.Content = append(input.Content, agent.Content{Text: part.Text})
				}
				if part.Image != nil {
					input.Content = append(input.Content, agent.Content{File: &agent.File{Data: part.Image.Data}})
				}
			}
			v := visibleInput(input)
			if v.Text != "" || len(v.Files)+len(v.Images) > 0 {
				id := fmt.Sprintf("history:%d:user", mi)
				if m.InputID != "" {
					id = "input:" + m.InputID
				}
				entries = append(entries, TranscriptEntry{ID: id, InputID: m.InputID, Type: "user", Content: v.Text, Files: v.Files, Images: v.Images})
			}
			continue
		}
		for ci, part := range m.Content {
			id := fmt.Sprintf("history:%d:%d", mi, ci)
			if part.Text != "" {
				textID := id + ":text"
				if part.TextID != "" {
					textID = "assistant:" + part.TextID
				}
				entries = append(entries, TranscriptEntry{ID: textID, Type: "assistant", Content: part.Text, MessageID: part.TextID})
			}
			if part.Reasoning != nil {
				reasonID := id + ":reasoning"
				if part.Reasoning.ID != "" {
					reasonID = "reasoning:" + part.Reasoning.ID
				}
				entries = append(entries, TranscriptEntry{ID: reasonID, Type: "reasoning", Content: part.Reasoning.Summary, ReasoningID: part.Reasoning.ID})
			}
			if t := part.ToolCall; t != nil {
				tid := "tool:" + t.ID
				if t.ID == "" {
					tid = id + ":tool"
				}
				entries = append(entries, TranscriptEntry{ID: tid, Type: "tool", ToolID: t.ID, ToolName: t.Name, ToolKind: t.Kind, ToolArgs: t.Args, ToolLocations: t.Locations, ToolHint: t.Hint})
			}
			if t := part.ToolResult; t != nil {
				i := slices.IndexFunc(entries, func(e TranscriptEntry) bool { return t.ID != "" && e.ToolID == t.ID })
				if i >= 0 {
					entries[i].ToolResult = &t.Content
					continue
				}
				entries = append(entries, TranscriptEntry{ID: id + ":result", Type: "tool", ToolID: t.ID, ToolName: t.Name, ToolKind: t.Kind, ToolArgs: t.Args, ToolLocations: t.Locations, ToolResult: &t.Content})
			}
		}
	}
	return entries
}
