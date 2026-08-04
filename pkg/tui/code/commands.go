package code

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/task"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/adrianliechti/wingman-agent/pkg/skill"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

type slashCommand struct {
	Name string
	Desc string

	// Busy marks commands that stay usable while a turn is running.
	Busy bool
	Run  func(a *App)
}

func (a *App) builtinCommands() []slashCommand {
	cmds := []slashCommand{
		{Name: "/help", Desc: "Show help", Run: (*App).showHelp},
		{Name: "/model", Desc: "Select AI model", Run: (*App).showModelPicker},
		{Name: "/effort", Desc: "Set reasoning effort", Run: (*App).showEffortPicker},
		{Name: "/plan", Desc: "Enter planning mode", Run: (*App).enterPlanMode},
		{Name: "/agent", Desc: "Return to execution mode", Run: (*App).exitPlanMode},
		{Name: "/problems", Desc: "Show problems", Run: (*App).showDiagnosticsView},
	}
	if _, ok := a.agent.(contextStatsProvider); ok {
		cmds = append(cmds, slashCommand{Name: "/context", Desc: "Show context window usage", Run: (*App).showContextStats})
	}
	if _, ok := a.agent.(taskProvider); ok {
		cmds = append(cmds, slashCommand{Name: "/tasks", Desc: "Show background agents", Busy: true, Run: (*App).showTasks})
	}
	if _, ok := a.agent.(recapProvider); ok {
		cmds = append(cmds, slashCommand{Name: "/recap", Desc: "Summarize the session so far", Run: (*App).showRecap})
	}

	if a.agent.Workspace().HasRewind() {
		cmds = append(cmds,
			slashCommand{Name: "/diff", Desc: "Show changes from baseline", Run: (*App).showDiffView},
			slashCommand{Name: "/rewind", Desc: "Restore to previous checkpoint", Run: (*App).showRewindPicker},
		)
	}

	cmds = append(cmds,
		slashCommand{Name: "/resume", Desc: "Resume last session", Run: (*App).resumeSession},
		slashCommand{Name: "/clear", Desc: "Clear chat history", Run: (*App).clearChat},
		slashCommand{Name: "/quit", Desc: "Exit application", Busy: true, Run: func(a *App) {
			if a.confirmQuit() {
				a.stop()
			}
		}},
	)

	return cmds
}

func (a *App) findBuiltin(query string) *slashCommand {
	cmds := a.builtinCommands()
	for i := range cmds {
		if cmds[i].Name == query {
			return &cmds[i]
		}
	}
	return nil
}

func (a *App) skillCommands() []slashCommand {
	var cmds []slashCommand
	for _, s := range a.agent.Workspace().Skills {
		cmds = append(cmds, slashCommand{Name: "/" + s.Name, Desc: s.Description})
	}
	slices.SortStableFunc(cmds, func(a, b slashCommand) int {
		if a.Name == "/init" {
			return -1
		}
		if b.Name == "/init" {
			return 1
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return cmds
}

func (a *App) availableCommands() []slashCommand {
	return append(a.builtinCommands(), a.skillCommands()...)
}

// slashToken returns the /command token the cursor sits in: the rune index
// of its leading slash and the token text up to the cursor. A token starts at
// the beginning of the buffer or after whitespace and contains no whitespace,
// so paths and URLs never form one.
func slashToken(value []rune, cursor int) (int, string, bool) {
	for i := cursor; i > 0; i-- {
		r := value[i-1]
		if r == ' ' || r == '\t' || r == '\n' {
			return 0, "", false
		}
		if r != '/' {
			continue
		}
		if i >= 2 {
			prev := value[i-2]
			if prev != ' ' && prev != '\t' && prev != '\n' {
				return 0, "", false
			}
		}
		return i - 1, string(value[i-1 : cursor]), true
	}
	return 0, "", false
}

// syncCommandPopup opens/updates/closes the slash-command popup based on the
// token at the cursor. At the start of the buffer it completes commands and
// skills; mid-prompt it completes skills only.
func (a *App) syncCommandPopup() {
	start, token, ok := slashToken(a.editor.value, a.editor.cursor)

	if !ok || a.promptActive || a.askActive {
		if a.popup != nil && a.popup.kind == popupCommands {
			a.closePopup()
		}
		return
	}

	if a.popup != nil && a.popup.kind != popupCommands {
		return
	}

	inline := start > 0
	if a.popup == nil || a.cmdPopupInline != inline {
		a.closePopup()

		cmds := a.availableCommands()
		if inline {
			cmds = a.skillCommands()
		}

		var items []PopupItem
		for _, cmd := range cmds {
			items = append(items, PopupItem{ID: cmd.Name, Label: cmd.Name, Detail: cmd.Desc})
		}
		a.popup = newPopup(popupCommands, "", items, nil)
		a.cmdPopupInline = inline
	}

	a.cmdTokenStart = start
	a.popup.SetQuery(token)

	if a.popup.Empty() {
		a.closePopup()
	}
}

// completeCommand replaces the slash token at the cursor with the selected
// command and reports whether anything changed. The replacement spans the
// whole word so completing with the cursor mid-token leaves no tail behind;
// mid-prompt completions end past a trailing space so typing continues
// naturally.
func (a *App) completeCommand(id string) bool {
	start, end := a.cmdTokenStart, a.editor.cursor
	for end < len(a.editor.value) {
		r := a.editor.value[end]
		if r == ' ' || r == '\t' || r == '\n' {
			break
		}
		end++
	}

	insert := id
	advance := 0
	if start > 0 {
		if end < len(a.editor.value) && a.editor.value[end] == ' ' {
			advance = 1
		} else {
			insert += " "
		}
	}

	changed := advance > 0 || insert != string(a.editor.value[start:end])
	a.editor.ReplaceRange(start, end, insert)
	a.editor.cursor += advance
	a.syncCommandPopup()
	return changed
}

func (a *App) submitInput() {
	query := strings.TrimSpace(a.editor.Text())

	if query == "" {
		return
	}

	if query == "/models" {
		query = "/model"
	}

	cmd := a.findBuiltin(query)
	if cmd != nil && a.getPhase() != PhaseIdle && !cmd.Busy {
		return
	}

	if a.popup != nil && a.popup.kind == popupCommands {
		a.closePopup()
	}

	if cmd != nil {
		a.editor.SetText("")
		cmd.Run(a)
		return
	}

	skills := a.agent.Workspace().Skills

	if name, _, ok := skill.ParseCommand(query); ok && skill.FindSkill(name, skills) == nil {
		a.editor.SetText("")
		a.appendChat(cellNotice(fmt.Sprintf("Unknown command: /%s", name), theme.Default.Yellow, a.width()))
		return
	}

	a.editor.AddHistory(query)
	a.editor.SetText("")

	displayText := query

	imageCount := a.countPendingImages()
	if imageCount > 0 || len(a.pendingFiles) > 0 {
		var attachments []string
		if imageCount == 1 {
			attachments = append(attachments, "1 image")
		} else if imageCount > 1 {
			attachments = append(attachments, fmt.Sprintf("%d images", imageCount))
		}
		for _, f := range a.pendingFiles {
			attachments = append(attachments, f)
		}
		displayText = fmt.Sprintf("%s\n[%s]", query, strings.Join(attachments, ", "))
	}

	input := []agent.Content{{Text: displayText}}
	input = append(input, a.pendingContent...)

	if len(a.pendingFiles) > 0 {
		var sb strings.Builder
		fmt.Fprint(&sb, "\n[Attached files - use the read tool to access their content]\n")
		for _, f := range a.pendingFiles {
			fmt.Fprintf(&sb, "- %s\n", f)
		}
		input = append(input, agent.Content{Text: sb.String()})
	}

	for _, inv := range skill.Invocations(query, skills) {
		block, err := inv.Instructions(a.agent.Workspace().RootPath)
		if err != nil {
			a.appendChat(cellNotice(fmt.Sprintf("Failed to load skill %q: %v", inv.Skill.Name, err), theme.Default.Red, a.width()))
			continue
		}
		input = append(input, agent.Content{Text: block, Hidden: true})
	}

	a.clearPendingContent()
	a.showWelcome = false

	a.submitAgentInput(input, displayText)
}

func (a *App) submitAgentInput(input []agent.Content, echo string) {
	id := uuid.NewString()
	a.rememberTurn(id, input)

	snap, err := a.turns.Submit(a.ctx, a.sessionID, code.TurnInput{
		ID: id, Content: input, Intent: code.TurnInputSteer,
	})
	if err != nil {
		a.takeTurnCommit(id)
		a.appendChat(cellNotice(fmt.Sprintf("Could not submit turn: %v", err), theme.Default.Red, a.width()))
		return
	}

	// Only inputs waiting behind an active turn get a preview; an input that
	// starts immediately commits within a frame and a preview would flicker.
	if echo != "" && (snap.State == code.TurnInputQueued || snap.State == code.TurnInputSteered) {
		a.pendingEcho = append(a.pendingEcho, pendingEchoItem{ID: id, Text: echo})
	}

	a.syncMessages()
	a.invalidate()
}

func (a *App) showHelp() {
	t := theme.Default

	builtinCmds := a.builtinCommands()
	skillCmds := a.skillCommands()

	maxLen := 0
	for _, cmd := range append(append([]slashCommand{}, builtinCmds...), skillCmds...) {
		if len(cmd.Name) > maxLen {
			maxLen = len(cmd.Name)
		}
	}

	var lines []string
	lines = append(lines, cellIndent+bold("commands"))
	for _, cmd := range builtinCmds {
		pad := strings.Repeat(" ", maxLen-len(cmd.Name))
		lines = append(lines, cellIndent+"  "+colored(t.Cyan, cmd.Name)+pad+"  "+dim(cmd.Desc))
	}

	if len(skillCmds) > 0 {
		lines = append(lines, "")
		lines = append(lines, cellIndent+bold("skills"))
		for _, cmd := range skillCmds {
			pad := strings.Repeat(" ", maxLen-len(cmd.Name))
			lines = append(lines, cellIndent+"  "+colored(t.Cyan, cmd.Name)+pad+"  "+dim(cmd.Desc))
		}
	}

	lines = append(lines, "")

	a.appendChat(lines)
}

func (a *App) showTasks() {
	t := theme.Default

	provider, ok := a.agent.(taskProvider)
	if !ok {
		a.appendChat(cellNotice("Background agents are unavailable for this agent", t.Yellow, a.width()))
		return
	}
	reg := provider.Tasks(a.sessionID)
	if reg == nil {
		a.appendChat(cellNotice("No background agents in this session", t.Yellow, a.width()))
		return
	}

	tasks := reg.List()
	if len(tasks) == 0 {
		a.appendChat(cellNotice("No background agents in this session", t.Yellow, a.width()))
		return
	}

	items := make([]PopupItem, len(tasks))
	for i, tk := range tasks {
		detail := tk.Description
		if tk.Status() == task.StatusRunning {
			if activity := tk.Activity(); activity != "" {
				detail += " — " + activity
			}
		}
		items[i] = PopupItem{
			ID:     tk.ID,
			Label:  fmt.Sprintf("%s  %-7s  %-14s  %s", tk.ID, tk.Status(), tk.AgentType, tk.Elapsed().Round(time.Second)),
			Detail: detail,
		}
	}

	a.popup = newPopup(popupList, "background agents (enter to watch)", items, func(ids []string) {
		if tk := reg.Get(ids[0]); tk != nil {
			a.showTaskPeek(tk)
		}
	})
}

func (a *App) showModelPicker() {
	available, current := a.agent.Models(a.sessionID)
	if len(available) == 0 {
		return
	}

	var items []PopupItem
	for _, m := range available {
		items = append(items, PopupItem{ID: m.ID, Label: m.Name})
	}

	popup := newPopup(popupList, "model", items, func(ids []string) {
		_ = a.agent.SetModel(a.ctx, a.sessionID, ids[0])
		a.invalidate()
	})
	popup.SelectID(current)
	a.popup = popup
}

func (a *App) cycleModel() {
	sessionID := a.sessionID

	go func() {
		available, current := a.agent.Models(sessionID)
		if len(available) <= 1 {
			return
		}
		for i, m := range available {
			if m.ID == current {
				_ = a.agent.SetModel(context.Background(), sessionID, available[(i+1)%len(available)].ID)
				break
			}
		}
		a.post(a.invalidate)
	}()
}

func (a *App) showEffortPicker() {
	current, options := a.agent.Effort(a.sessionID)
	if len(options) == 0 {
		return
	}

	items := make([]PopupItem, 0, len(options))
	for _, v := range options {
		items = append(items, PopupItem{ID: v, Label: v})
	}

	popup := newPopup(popupList, "effort", items, func(ids []string) {
		_ = a.agent.SetEffort(a.ctx, a.sessionID, ids[0])
		a.invalidate()
	})
	popup.SelectID(current)
	a.popup = popup
}

func (a *App) showRewindPicker() {
	t := theme.Default

	checkpoints, err := a.agent.Workspace().Checkpoints()
	if err != nil {
		a.appendChat(cellNotice("Rewind unavailable in this workspace", t.Yellow, a.width()))
		return
	}
	if len(checkpoints) == 0 {
		a.appendChat(cellNotice("No checkpoints available", t.Yellow, a.width()))
		return
	}

	items := make([]PopupItem, len(checkpoints))
	for i, cp := range checkpoints {
		items[i] = PopupItem{
			ID:     cp.Hash,
			Label:  cp.Time.Format("15:04:05"),
			Detail: cp.Message,
		}
	}

	a.popup = newPopup(popupList, "rewind to", items, func(ids []string) {
		var label string
		for _, item := range items {
			if item.ID == ids[0] {
				label = item.Label + " - " + item.Detail
			}
		}
		if err := a.agent.Workspace().Restore(ids[0]); err != nil {
			a.appendChat(cellNotice(fmt.Sprintf("Failed to restore: %v", err), t.Red, a.width()))
			return
		}
		a.appendChat(cellNotice(fmt.Sprintf("Restored to: %s", label), t.Green, a.width()))
	})
}

func (a *App) showFilePicker() {
	go func() {
		files := a.collectFiles()

		a.post(func() {
			if a.popup != nil {
				return
			}

			items := make([]PopupItem, len(files))
			for i, f := range files {
				items[i] = PopupItem{ID: f.Path, Label: f.Path}
			}

			popup := newPopup(popupList, "@ add files (space to mark, enter to add)", items, func(ids []string) {
				for _, p := range ids {
					a.addFileToContext(p)
				}
			})
			popup.multi = true
			a.popup = popup
			a.invalidate()
		})
	}()
}
