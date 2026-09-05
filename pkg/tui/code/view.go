package code

import (
	"fmt"
	"os"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/adrianliechti/wingman-agent/pkg/model"
	"github.com/adrianliechti/wingman-agent/pkg/tui"
	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
	"github.com/adrianliechti/wingman-agent/pkg/tui/inline"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

func (a *App) welcomeLines(width int) []string {
	t := theme.Default

	center := func(text string) string {
		pad := max((width-ansi.Width(text))/2, 0)
		return strings.Repeat(" ", pad) + text
	}

	var lines []string

	if width > 66 {
		colors := []string{
			fg(t.Blue), fg(t.Cyan), fg(t.Green), fg(t.Yellow), fg(t.Red), fg(t.Magenta),
		}
		for i, l := range tui.LogoLines {
			lines = append(lines, center(colors[i%len(colors)]+l+ansi.Reset))
		}
	} else {
		lines = append(lines, center(bold("wingman")))
	}

	lines = append(lines, "")

	cwd := a.agent.Workspace().RootPath
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(cwd, home) {
		cwd = "~" + strings.TrimPrefix(cwd, home)
	}
	lines = append(lines, center(dim(ansi.Truncate(cwd, max(10, width-4), "…"))))

	return lines
}

func (a *App) composerChrome(width int) EditorChrome {
	t := theme.Default
	color := t.BrBlack
	modeLabel := ""
	currentMode := a.currentMode()
	planMode := currentMode == code.PlanModeID
	unattendedMode := currentMode == code.UnattendedModeID
	if planMode {
		color = t.Yellow
		modeLabel = colored(t.Yellow, "PLAN")
	}
	if unattendedMode {
		color = t.Red
	}
	if a.promptActive || a.askActive {
		color = t.Red
		modeLabel = colored(t.Red, "ANSWER")
	}

	var identity []string
	availableModels, currentModel := a.agent.Models(a.sessionID)
	if label := modelIdentity(availableModels, currentModel); label != "" {
		identity = append(identity, label)
	}
	if effort, _ := a.agent.Effort(a.sessionID); effort != "" && effort != "auto" && effort != "default" {
		identity = append(identity, effort)
	}
	topLabel := ""
	bottomLeft := modeLabel
	if planMode && !a.promptActive && !a.askActive {
		topLabel = modeLabel
		bottomLeft = ""
	}

	identityLabel := strings.Join(identity, " · ")
	identityStyle := ""
	if identityLabel != "" {
		identityStyle = dim(identityLabel)
		if planMode {
			identityStyle = colored(t.Yellow, identityLabel)
		} else if unattendedMode {
			identityStyle = colored(t.Red, identityLabel)
		}
	}

	return EditorChrome{
		TopLabel:    topLabel,
		BottomLeft:  bottomLeft,
		BottomRight: identityStyle,
		TopColor:    color,
		Attachments: a.attachmentLines(width),
	}
}

func modelIdentity(available []model.Model, current string) string {
	if current == "" || current == "default" || current == "auto" {
		return ""
	}
	for _, candidate := range available {
		if candidate.ID != current {
			continue
		}
		if candidate.Name != "" {
			return candidate.Name
		}
	}
	return model.Name(current)
}

// streamCells renders the in-flight turn tail shown below the committed chat.
// Displaced snapshots keep earlier reasoning headings, messages, and tools
// visible while ACP waits until turn completion to commit the complete
// transcript. Reasoning bodies remain buffered for reconciliation and the
// transcript overlay; the live chat shows only their stable structured heading.
// Visible live cells follow the same cellFlow rules as committed cells (on a
// copy of the state).
func (a *App) streamCells(width int) []string {
	snapshots := a.snapshotStreamState()

	flow := a.flow
	var lines []string

	for _, snapshot := range snapshots {
		if snapshot.userText != "" {
			if flow.gap() {
				lines = append(lines, "")
			}
			if isCommandEcho(snapshot.userText) {
				lines = append(lines, cellCommand(snapshot.userText, width)...)
			} else {
				lines = append(lines, cellUser(snapshot.userText, width)...)
			}
		}

		if snapshot.toolName != "" && !a.isToolHidden(snapshot.toolName) {
			cell := snapshot.toolLines(width, false)
			if flow.beforeTool(len(cell) > 1) {
				lines = append(lines, "")
			}
			lines = append(lines, cell...)
		}

		if strings.TrimSpace(snapshot.text) != "" {
			if flow.gap() {
				lines = append(lines, "")
			}
			lines = append(lines, cellAssistant(snapshot.text, width, theme.Default.BrBlack)...)
		}

		if snapshot.reasoningHeadings != "" {
			cell := cellReasoningHeadings(snapshot.reasoningHeadings, width)
			if flow.beforeThought(len(cell) > 1) {
				lines = append(lines, "")
			}
			lines = append(lines, cell...)
		}
	}

	// While the spinner or a pinned prompt is visible the tail always ends
	// blank, so tool and reasoning cells never sit tight against it.
	if (a.isStreaming() || a.promptActive || a.askActive) && (a.flow.tool || len(lines) > 0) && (len(lines) == 0 || lines[len(lines)-1] != "") {
		lines = append(lines, "")
	}

	return lines
}

// render paints the full-screen frame: scrollable chat on top, then the
// status-rich composer and popup or footer pinned at the bottom. Queued input
// previews live at the tail of the scrollable chat until their turns start.
func (a *App) render() {
	termWidth, height := a.term.Size()
	if termWidth <= 0 || height <= 0 {
		return
	}

	if a.overlay != nil {
		a.term.RenderAlt(a.overlay.Render(termWidth, height), nil)
		return
	}

	// The chat rewraps whenever the diff pane appears or disappears.
	panelWidth := a.diffPanelWidth(termWidth)
	if showing := panelWidth > 0; showing != a.diffPanelShowing {
		a.diffPanelShowing = showing
		a.rebuildChat()
	}
	width := termWidth
	if panelWidth > 0 {
		width = termWidth - panelWidth - 2
	}

	// Selection mode: the popup is the only live element — the composer and
	// footer would just be noise around it.
	listPopup := a.popup != nil && (a.popup.kind == popupList || a.popup.kind == popupPalette)

	// Bottom section, built first so the chat viewport gets the remainder.
	var bottom []string
	editorStart := 0
	var cursor inline.Pos
	hasCursor := false

	if listPopup {
		if a.popup.kind == popupPalette {
			a.popup.maxRows = max(3, min(10, height/3))
		} else {
			a.popup.maxRows = max(5, min(14, height/2))
		}
		bottom = append(bottom, "")
		bottom = append(bottom, a.popup.Render(width)...)
		bottom = append(bottom, "")

		// A long question must not push the options off-screen: keep the
		// tail, which holds the items.
		if len(bottom) > height {
			bottom = append([]string{dim("…")}, bottom[len(bottom)-height+1:]...)
		}
	} else {
		if a.askActive && len(a.askHeader) > 0 {
			bottom = append(bottom, a.askHeader...)
		}

		maxEditorRows := max(height/3, 5)

		var editorLines []string
		editorLines, cursor = a.editor.Render(width, maxEditorRows, a.composerChrome(width))
		hasCursor = true
		editorStart = len(bottom)
		bottom = append(bottom, editorLines...)

		if a.popup != nil {
			bottom = append(bottom, a.popup.Render(width)...)
		} else {
			bottom = append(bottom, a.footerLine(width))
		}
	}

	// A too-short window drops rows from the top of the bottom section — the
	// editor and footer at its tail must stay visible.
	if len(bottom) > height {
		drop := len(bottom) - height
		bottom = bottom[drop:]
		editorStart -= drop
	}

	chatRows := max(height-len(bottom), 0)
	a.lastChatRows = chatRows

	view := a.chatViewLines(width)

	if a.showWelcome && len(view) == 0 {
		welcome := a.welcomeLines(width)
		pad := (chatRows - len(welcome)) / 2
		for range pad {
			view = append(view, "")
		}
		view = append(view, welcome...)
	}

	maxScroll := max(len(view)-chatRows, 0)
	a.lastMaxScroll = maxScroll

	if a.follow || a.chatScroll >= maxScroll {
		a.chatScroll = maxScroll
		a.follow = true
	}

	// Bottom-anchor short conversations so content hugs the composer.
	topPad := 0
	if !a.showWelcome && len(view) < chatRows {
		topPad = chatRows - len(view)
	}
	a.lastTopPad = topPad

	selStart, selEnd := a.orderedSelection()
	showSelection := a.selActive || a.selecting

	frame := make([]string, 0, height)

	for i := range chatRows {
		idx := a.chatScroll + i - topPad
		line := ""
		if idx >= 0 && idx < len(view) {
			line = view[idx]
		}

		if showSelection && idx >= selStart.Line && idx <= selEnd.Line {
			from, to := 0, ansi.Width(line)
			if idx == selStart.Line {
				from = selStart.Col
			}
			if idx == selEnd.Line && selEnd.Col+1 < to {
				to = selEnd.Col + 1
			}
			if to <= from {
				to = from + 1
			}
			line = ansi.Highlight(line, from, to, ansi.Reverse)
		}

		frame = append(frame, line)
	}

	frame = append(frame, bottom...)

	// Scroll indicator on the row above the composer when the newest content
	// is off-screen; full-width rows are truncated to keep it visible.
	if hidden := maxScroll - a.chatScroll; !listPopup && !a.follow && hidden > 0 {
		idx := chatRows + editorStart - 1
		if idx >= 0 && idx < len(frame) {
			indicator := dim(fmt.Sprintf("↓ %d more", hidden))
			keep := width - ansi.Width(indicator) - 1
			if keep > 0 {
				frame[idx] = ansi.Pad(ansi.Truncate(frame[idx], keep, "…"), keep) + " " + indicator
			}
		}
	}

	if panelWidth > 0 {
		divider := colored(theme.Default.BrBlack, "│")
		panel := a.diffPanel.render(panelWidth, height)
		for len(frame) < height {
			frame = append(frame, "")
		}
		for i := range height {
			frame[i] = ansi.Pad(ansi.Truncate(frame[i], width, "…"), width) + " " + divider + panel[i]
		}
	}

	var cursorPtr *inline.Pos
	if hasCursor {
		cursor.Row += chatRows + editorStart
		if cursor.Row >= 0 && cursor.Row < height {
			cursorPtr = &cursor
		}
	}

	a.term.RenderAlt(frame, cursorPtr)
}
