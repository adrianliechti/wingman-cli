package code

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	corecode "github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

func (a *App) Confirm(ctx context.Context, message string) (bool, error) {
	if a.confirmAllForSession(ctx) {
		return true, nil
	}

	a.elicitMu.Lock()
	defer a.elicitMu.Unlock()

	items := []PopupItem{
		{ID: "yes", Label: "yes", Detail: "approve this command"},
		{ID: "no", Label: "no", Detail: "deny"},
		{ID: "always", Label: "always", Detail: "auto-approve for this session"},
	}

	hotkeys := map[rune]string{'y': "yes", 'n': "no", 'a': "always"}

	ids, err := a.askOptionsLocked(ctx, "Confirm command", message, items, false, hotkeys)
	if err != nil {
		return false, err
	}

	choice := ""
	if len(ids) > 0 {
		choice = ids[0]
	}

	switch choice {
	case "yes":
		a.recordPrompt("Confirm command", message, "Yes")
		return true, nil
	case "always":
		a.rememberConfirmAll(ctx)
		a.recordPrompt("Confirm command", message, "Always")
		a.post(func() {
			a.appendChat(cellNotice("Auto-approving commands for this session", theme.Default.BrBlack, a.width()))
		})
		return true, nil
	default:
		a.recordPrompt("Confirm command", message, "No")
		return false, nil
	}
}

func (a *App) confirmationSessionID(ctx context.Context) string {
	if id := corecode.SessionIDFromContext(ctx); id != "" {
		return id
	}
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	return a.sessionID
}

func (a *App) confirmAllForSession(ctx context.Context) bool {
	_, ok := a.confirmAll.Load(a.confirmationSessionID(ctx))
	return ok
}

func (a *App) rememberConfirmAll(ctx context.Context) {
	a.confirmAll.Store(a.confirmationSessionID(ctx), struct{}{})
}

// recordPrompt writes the resolved question and its answer into the chat —
// the question is only shown at the bottom while it is being asked.
func (a *App) recordPrompt(title, message, answer string) {
	a.post(func() {
		a.flushToolGap()
		a.appendChat(cellPrompt(title, message, "", a.width()))
		a.appendChat(cellUser(answer, a.width()))
		a.setPhase(PhaseThinking)
		a.invalidate()
	})
}

// askOptionsLocked presents an interactive selection popup and returns the
// chosen ids (nil when declined or dismissed). Expects a.elicitMu held.
func (a *App) askOptionsLocked(ctx context.Context, title, message string, items []PopupItem, multi bool, hotkeys map[rune]string) ([]string, error) {
	response := make(chan []string, 1)

	a.post(func() {
		a.promptActive = true

		popup := newPopup(popupList, popupHint("", multi), items, func(ids []string) {
			select {
			case response <- ids:
			default:
			}
		})
		popup.header = cellPrompt(title, message, "", a.width())
		popup.multi = multi
		popup.hotkeys = hotkeys
		popup.onCancel = func() {
			select {
			case response <- nil:
			default:
			}
		}
		a.popup = popup
		a.invalidate()
	})

	defer a.post(func() {
		a.promptActive = false
		if a.popup != nil && a.popup.kind == popupList {
			a.closePopup()
		}
		a.invalidate()
	})

	select {
	case ids := <-response:
		return ids, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-a.ctx.Done():
		return nil, a.ctx.Err()
	}
}

func popupHint(title string, multi bool) string {
	if !multi {
		return title
	}
	hint := "space to mark, enter to accept"
	if title == "" {
		return hint
	}
	return title + " (" + hint + ")"
}

func (a *App) Elicit(ctx context.Context, req tool.ElicitRequest) (tool.ElicitResult, error) {
	// One lock across the whole request: a concurrent elicitation (parallel
	// tool calls, an MCP server) must not splice its prompt between the
	// fields of this one.
	a.elicitMu.Lock()
	defer a.elicitMu.Unlock()

	fields := req.Fields

	// A bare message (MCP confirmation-style elicitation) still needs an
	// accept/decline decision; model it as a yes/no pseudo-field whose value
	// stays out of the returned content.
	bare := len(fields) == 0
	if bare {
		fields = []tool.ElicitField{{Name: "confirmed", Type: "boolean", Required: true}}
	}

	content := map[string]any{}

	for i := 0; i < len(fields); i++ {
		field := fields[i]
		if field.CustomAnswerFor != "" {
			continue
		}
		message := ""
		if i == 0 {
			message = req.Message
		}

		freeText := len(field.Enum) == 0 && field.Type != "boolean"
		answerField := field
		customField, hasCustom := customAnswerCompanion(fields, field.Name)

		if !freeText {
			choiceField := field
			if hasCustom && len(choiceField.Enum) > 0 {
				choiceField.Strict = false
			}
			value, action, err := a.elicitOptionsField(ctx, message, choiceField, len(fields) > 1 && !bare)
			if err != nil {
				return tool.ElicitResult{}, err
			}

			switch action {
			case "decline":
				return tool.ElicitResult{Action: tool.ElicitDecline}, nil
			case "other":
				freeText = true
				if hasCustom {
					answerField = customField
				}
			default:
				if !bare {
					content[field.Name] = value
				} else if accepted, _ := value.(bool); !accepted {
					return tool.ElicitResult{Action: tool.ElicitDecline}, nil
				}
			}
		}

		if !freeText {
			continue
		}

		prompt := formatElicitField(message, answerField, len(fields) > 1 && !bare)

		for {
			text, err := a.askLineLocked(ctx, prompt, elicitPlaceholder(answerField))
			if err != nil {
				return tool.ElicitResult{}, err
			}

			if strings.EqualFold(strings.TrimSpace(text), "/decline") {
				return tool.ElicitResult{Action: tool.ElicitDecline}, nil
			}

			value, ok := parseElicitValue(answerField, text)
			if !ok {
				prompt = fmt.Sprintf("Invalid %s — try again.", fieldKind(answerField))
				continue
			}

			if !bare {
				content[answerField.Name] = value
			} else if accepted, _ := value.(bool); !accepted {
				return tool.ElicitResult{Action: tool.ElicitDecline}, nil
			}
			break
		}
	}

	return tool.ElicitResult{Action: tool.ElicitAccept, Content: content}, nil
}

func customAnswerCompanion(fields []tool.ElicitField, questionID string) (tool.ElicitField, bool) {
	for _, field := range fields {
		if field.CustomAnswerFor == questionID {
			return field, true
		}
	}
	return tool.ElicitField{}, false
}

// elicitOptionsField collects an enum or boolean answer through a selection
// popup. action is "", "decline", or "other" (free-text fallback for
// advisory enums).
func (a *App) elicitOptionsField(ctx context.Context, message string, field tool.ElicitField, showLabel bool) (any, string, error) {
	label := field.Title
	if label == "" {
		label = field.Name
	}

	if showLabel || field.Description != "" {
		if message != "" {
			message += "\n\n"
		}
		message += label
		if field.Description != "" {
			message += " — " + field.Description
		}
	}

	if field.Type == "boolean" && len(field.Enum) == 0 {
		items := []PopupItem{
			{ID: "yes", Label: "yes"},
			{ID: "no", Label: "no"},
		}

		ids, err := a.askOptionsLocked(ctx, "", message, items, false, map[rune]string{'y': "yes", 'n': "no"})
		if err != nil {
			return nil, "", err
		}
		if len(ids) == 0 {
			a.recordPrompt("", message, "decline")
			return nil, "decline", nil
		}

		value := ids[0] == "yes"
		a.recordPrompt("", message, ids[0])
		return value, "", nil
	}

	items := make([]PopupItem, 0, len(field.Enum)+2)
	for i, option := range field.Enum {
		item := PopupItem{ID: option, Label: option}
		if i < len(field.EnumDescriptions) {
			item.Detail = field.EnumDescriptions[i]
		}
		items = append(items, item)
	}

	if !field.Strict {
		items = append(items, PopupItem{ID: "__other", Label: "other…", Detail: "type your own answer"})
	}
	items = append(items, PopupItem{ID: "__decline", Label: "decline"})

	ids, err := a.askOptionsLocked(ctx, "", message, items, field.Multiple, nil)
	if err != nil {
		return nil, "", err
	}

	var picks []string
	for _, id := range ids {
		switch id {
		case "__decline":
			picks = nil
		case "__other":
			return nil, "other", nil
		default:
			picks = append(picks, id)
			continue
		}
		break
	}

	if len(picks) == 0 {
		a.recordPrompt("", message, "decline")
		return nil, "decline", nil
	}

	a.recordPrompt("", message, strings.Join(picks, ", "))

	if field.Multiple {
		return picks, "", nil
	}
	return picks[0], "", nil
}

func fieldKind(field tool.ElicitField) string {
	if len(field.Enum) > 0 {
		return "choice — pick one of the listed options"
	}
	if field.Type == "" {
		return "value"
	}
	return field.Type
}

// askLineLocked expects a.elicitMu to be held by the caller.
func (a *App) askLineLocked(ctx context.Context, prompt, placeholder string) (string, error) {
	a.askResponse = make(chan string, 1)

	a.post(func() {
		a.askActive = true
		a.askMessage = prompt
		a.askHeader = cellPrompt("", prompt, "", a.width())
		a.editor.SetPlaceholder(placeholder)
		a.invalidate()
	})

	defer a.post(func() {
		a.askActive = false
		a.askMessage = ""
		a.askHeader = nil
		a.editor.SetPlaceholder("Ask anything...")
		a.invalidate()
	})

	select {
	case result := <-a.askResponse:
		return result, nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-a.ctx.Done():
		return "", a.ctx.Err()
	}
}

func formatElicitField(message string, field tool.ElicitField, showLabel bool) string {
	var b strings.Builder

	if message != "" {
		b.WriteString(message)
	}

	// The field label is only informative on multi-field forms or when a
	// description adds context; for a single question the message says it all.
	if showLabel || field.Description != "" {
		label := field.Title
		if label == "" {
			label = field.Name
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(label)
		if field.Description != "" {
			b.WriteString(" — " + field.Description)
		}
	}

	for i, option := range field.Enum {
		if i == 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "\n  [%d] %s", i+1, option)
		if i < len(field.EnumDescriptions) && field.EnumDescriptions[i] != "" {
			b.WriteString(" — " + field.EnumDescriptions[i])
		}
		if i < len(field.EnumPreviews) && field.EnumPreviews[i] != "" {
			for _, line := range previewLines(field.EnumPreviews[i]) {
				b.WriteString("\n      " + line)
			}
		}
	}

	if field.Default != nil {
		fmt.Fprintf(&b, "\n(default: %v)", field.Default)
	}

	return b.String()
}

const maxPreviewLines = 12

func previewLines(preview string) []string {
	lines := strings.Split(strings.TrimRight(preview, "\n"), "\n")
	if len(lines) > maxPreviewLines {
		lines = append(lines[:maxPreviewLines], "…")
	}
	return lines
}

func elicitPlaceholder(field tool.ElicitField) string {
	switch field.Type {
	case "number", "integer":
		return "number · /decline"
	default:
		return "answer · /decline"
	}
}

func parseElicitValue(field tool.ElicitField, text string) (any, bool) {
	text = strings.TrimSpace(text)

	if field.Multiple && len(field.Enum) > 0 {
		picks := make([]string, 0, 4)
		for part := range strings.SplitSeq(text, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			value, ok := resolveEnumToken(field, part)
			if !ok {
				return nil, false
			}
			picks = append(picks, value)
		}
		if len(picks) == 0 {
			return nil, false
		}
		return picks, true
	}

	if len(field.Enum) > 0 {
		return resolveEnumToken(field, text)
	}

	switch field.Type {
	case "boolean":
		switch strings.ToLower(text) {
		case "y", "yes", "true", "1":
			return true, true
		case "n", "no", "false", "0":
			return false, true
		}
		return nil, false
	case "integer":
		n, err := strconv.ParseInt(text, 10, 64)
		return n, err == nil
	case "number":
		f, err := strconv.ParseFloat(text, 64)
		return f, err == nil
	default:
		return text, true
	}
}

// resolveEnumToken maps a typed token to an enum value: an option number, an
// exact enum member, or — for advisory (non-strict) options — any free text.
func resolveEnumToken(field tool.ElicitField, token string) (string, bool) {
	if n, err := strconv.Atoi(token); err == nil && n >= 1 && n <= len(field.Enum) {
		return field.Enum[n-1], true
	}
	for _, value := range field.Enum {
		if strings.EqualFold(value, token) {
			return value, true
		}
	}
	if field.Strict {
		return "", false
	}
	return token, true
}
