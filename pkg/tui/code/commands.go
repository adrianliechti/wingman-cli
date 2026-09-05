package code

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/task"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/schedule"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/adrianliechti/wingman-agent/pkg/skill"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

type slashCommand struct {
	Name string
	Desc string
	Hint string

	// Busy marks commands that stay usable while a turn is running.
	Busy bool
	Run  func(a *App)
}

func (c slashCommand) Label() string {
	if c.Hint == "" {
		return c.Name
	}
	return c.Name + " " + c.Hint
}

func (a *App) builtinCommands() []slashCommand {
	cmds := []slashCommand{
		{Name: "/help", Desc: "Open command center", Busy: true, Run: (*App).showHelp},
		{Name: "/model", Desc: "Select AI model and effort", Run: (*App).showModelPicker},
	}
	modes, _ := a.agent.Modes(a.sessionID)
	for _, mode := range modes {
		cmds = append(cmds, slashCommand{
			Name: "/" + mode.ID,
			Desc: mode.Description,
			Busy: true,
			Run:  func(a *App) { a.setMode(mode.ID) },
		})
	}
	cmds = append(cmds,
		slashCommand{Name: "/problems", Desc: "Show problems", Busy: true, Run: (*App).showDiagnosticsView},
		slashCommand{Name: "/diff", Desc: "Show or hide working tree changes", Busy: true, Run: (*App).showDiffView},
	)
	if _, ok := a.agent.(contextStatsProvider); ok {
		cmds = append(cmds, slashCommand{Name: "/context", Desc: "Show context window usage", Busy: true, Run: (*App).showContextStats})
	}
	if _, ok := a.agent.(taskProvider); ok {
		cmds = append(cmds, slashCommand{Name: "/tasks", Desc: "Show background agents and scheduled tasks", Busy: true, Run: (*App).showTasks})
	}
	if _, ok := a.agent.(recapProvider); ok {
		cmds = append(cmds, slashCommand{Name: "/recap", Desc: "Summarize the session so far", Run: (*App).showRecap})
	}

	cmds = append(cmds,
		slashCommand{Name: "/resume", Desc: "Resume a previous session", Run: (*App).resumeSession},
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
	workspace := a.agent.Workspace()
	workspace.RefreshSkills()
	skills := workspace.Skills()
	for i := range skills {
		s := &skills[i]

		// A plugin skill whose bare name lost to another source is only
		// reachable qualified, so offer the name that actually works.
		name := s.Name
		if s.Plugin != "" && skill.FindSkill(s.Name, skills) != s {
			name = s.Qualified()
		}

		cmds = append(cmds, slashCommand{Name: "/" + name, Desc: s.Description, Hint: s.InvocationHint()})
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

func (a *App) agentCommands() []slashCommand {
	provider, ok := a.agent.(code.CommandProvider)
	if !ok {
		return nil
	}
	commands := provider.Commands(a.sessionID)
	result := make([]slashCommand, 0, len(commands))
	for _, command := range commands {
		name := "/" + strings.TrimPrefix(command.Name, "/")
		description := command.Description
		if description == "" {
			description = command.InputHint
		}
		result = append(result, slashCommand{Name: name, Desc: description})
	}
	return result
}

func (a *App) availableCommands() []slashCommand {
	groups := [][]slashCommand{a.builtinCommands(), a.skillCommands(), a.agentCommands()}
	seen := map[string]bool{}
	var result []slashCommand
	for _, group := range groups {
		for _, command := range group {
			if seen[command.Name] {
				continue
			}
			seen[command.Name] = true
			result = append(result, command)
		}
	}
	return result
}

func (a *App) commandHint(name string) string {
	for _, command := range a.availableCommands() {
		if command.Name == name {
			return command.Hint
		}
	}
	return ""
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
			items = append(items, PopupItem{ID: cmd.Name, Label: cmd.Label(), Detail: cmd.Desc})
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
	} else if a.commandHint(id) != "" {
		insert += " "
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

	workspace := a.agent.Workspace()
	workspace.RefreshSkills()
	skills := workspace.Skills()

	if name, ok := skill.ParseSlashCommand(query); ok && skill.FindSkill(name, skills) == nil && !a.hasAgentCommand(name) {
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

func (a *App) hasAgentCommand(name string) bool {
	for _, command := range a.agentCommands() {
		if strings.TrimPrefix(command.Name, "/") == name {
			return true
		}
	}
	return false
}

func (a *App) submitAgentInput(input []agent.Content, echo string) {
	id := uuid.NewString()

	snap, err := a.turns.Submit(a.ctx, a.sessionID, code.TurnInput{
		ID: id, Content: input, Intent: code.TurnInputSteer,
	})
	if err != nil {
		a.appendChat(cellNotice(fmt.Sprintf("Could not submit turn: %v", err), theme.Default.Red, a.width()))
		return
	}

	// A native steer is already part of the active turn. Put it into the live
	// transcript at the point where it was accepted so later model output
	// appears beneath it. Only inputs still waiting for a turn remain previews
	// at the bottom of the chat.
	if echo != "" {
		switch snap.State {
		case code.TurnInputSteered:
			a.appendLiveUserEcho(echo)
		case code.TurnInputQueued:
			a.pendingEchoMu.Lock()
			a.pendingEcho = append(a.pendingEcho, pendingEchoItem{ID: id, Text: echo, State: snap.State})
			a.pendingEchoMu.Unlock()
		}
	}

	a.syncMessages()
	a.invalidate()
}

func (a *App) showHelp() {
	a.showCommandCenter()
}

func (a *App) showTasks() {
	t := theme.Default

	provider, ok := a.agent.(taskProvider)
	if !ok {
		a.appendChat(cellNotice("Background agents are unavailable for this agent", t.Yellow, a.width()))
		return
	}

	reg := provider.Tasks(a.sessionID)

	var tasks []*task.Task
	if reg != nil {
		tasks = reg.List()
	}

	var jobs []schedule.Task
	if sp, ok := a.agent.(scheduleProvider); ok {
		if store := sp.Schedules(a.sessionID); store != nil {
			jobs, _ = store.List()
		}
	}

	if len(tasks) == 0 && len(jobs) == 0 {
		a.appendChat(cellNotice("No background agents or scheduled tasks in this session", t.Yellow, a.width()))
		return
	}

	items := make([]PopupItem, 0, len(tasks)+len(jobs))
	for _, tk := range tasks {
		detail := tk.Description
		if tk.Status() == task.StatusRunning {
			if activity := tk.Activity(); activity != "" {
				detail += " — " + activity
			}
		}
		items = append(items, PopupItem{
			ID:     tk.ID,
			Label:  fmt.Sprintf("%s  %-7s  %-14s  %s", tk.ID, tk.Status(), tk.AgentType, tk.Elapsed().Round(time.Second)),
			Detail: detail,
		})
	}

	now := time.Now()
	for _, job := range jobs {
		timing := schedule.Relative(schedule.NextAttempt(job, now), now)
		if job.Script != "" {
			timing += ", pre-check"
		}
		items = append(items, PopupItem{
			ID:     schedulePopupPrefix + job.ID,
			Label:  fmt.Sprintf("%s  %-7s  %-14s  %s", job.ID, job.Status, job.Schedule, timing),
			Detail: job.Prompt,
		})
	}

	a.popup = newPopup(popupList, "background agents & scheduled tasks (enter to watch)", items, func(ids []string) {
		if id, ok := strings.CutPrefix(ids[0], schedulePopupPrefix); ok {
			a.showSchedule(id)
			return
		}
		if reg == nil {
			return
		}
		if tk := reg.Get(ids[0]); tk != nil {
			a.showTaskPeek(tk)
		}
	})
}

const schedulePopupPrefix = "schedule:"

func (a *App) activeScheduleCount() int {
	sp, ok := a.agent.(scheduleProvider)
	if !ok {
		return 0
	}
	store := sp.Schedules(a.sessionID)
	if store == nil {
		return 0
	}

	jobs, err := store.List()
	if err != nil {
		return 0
	}

	count := 0
	for _, job := range jobs {
		if job.Status == schedule.StatusActive {
			count++
		}
	}
	return count
}

func (a *App) showSchedule(id string) {
	sp, ok := a.agent.(scheduleProvider)
	if !ok {
		return
	}
	store := sp.Schedules(a.sessionID)
	if store == nil {
		return
	}

	jobs, err := store.List()
	if err != nil {
		return
	}
	i, err := schedule.Find(jobs, id)
	if err != nil {
		a.appendChat(cellNotice(err.Error(), theme.Default.Yellow, a.width()))
		return
	}

	job := jobs[i]
	now := time.Now()

	lines := []string{
		fmt.Sprintf("Scheduled task %s (%s, %s)", job.ID, job.Schedule, job.Status),
		fmt.Sprintf("Next run: %s", schedule.Relative(schedule.NextAttempt(job, now), now)),
	}
	if job.LastRun != nil {
		lines = append(lines, fmt.Sprintf("Last run: %s", job.LastRun.Local().Format(time.RFC3339)))
	}
	if job.Script != "" {
		lines = append(lines, "Pre-check script: "+job.Script)
	}
	lines = append(lines, "", job.Prompt)

	a.appendChat(cellNotice(strings.Join(lines, "\n"), theme.Default.Cyan, a.width()))
}

func (a *App) showModelPicker() {
	a.showModelPickerLevel(false)
}

func (a *App) showModelPickerLevel(back bool) {
	available, current := a.agent.Models(a.sessionID)
	if len(available) == 0 {
		return
	}

	var items []PopupItem
	for _, m := range available {
		items = append(items, PopupItem{ID: m.ID, Label: m.Name, Detail: m.Description})
	}

	kind := popupList
	title := "model"
	if back {
		kind = popupPalette
		title = "commands › model"
	}
	popup := newPopup(kind, title, items, func(ids []string) {
		modelID := ids[0]
		if err := a.agent.SetModel(a.ctx, a.sessionID, modelID); err != nil {
			a.showToast("Could not change model: "+err.Error(), theme.Default.Red)
			return
		}
		a.invalidate()
		a.showEffortPickerLevel(back)
	})
	if back {
		popup.onCancel = a.showCommandCenter
	}
	for i := range popup.items {
		popup.items[i].Checked = popup.items[i].ID == current
	}
	popup.SelectID(current)
	a.popup = popup
}

func (a *App) showEffortPickerLevel(back bool) {
	current, options := a.agent.Effort(a.sessionID)
	if len(options) == 0 {
		return
	}
	selected := current
	if !slices.Contains(options, selected) {
		selected = options[0]
		for _, fallback := range []string{"default", "auto"} {
			if slices.Contains(options, fallback) {
				selected = fallback
				break
			}
		}
	}

	items := make([]PopupItem, 0, len(options))
	for _, v := range options {
		items = append(items, PopupItem{ID: v, Label: v})
	}

	kind := popupList
	title := "effort"
	if back {
		kind = popupPalette
		title = "commands › model › effort"
	}
	popup := newPopup(kind, title, items, func(ids []string) {
		if err := a.agent.SetEffort(a.ctx, a.sessionID, ids[0]); err != nil {
			a.showToast("Could not change effort: "+err.Error(), theme.Default.Red)
			return
		}
		a.invalidate()
	})
	popup.onCancel = func() { a.showModelPickerLevel(back) }
	for i := range popup.items {
		popup.items[i].Checked = popup.items[i].ID == selected
	}
	popup.SelectID(selected)
	a.popup = popup
}

func (a *App) showFilePicker() {
	go func() {
		files := a.collectFiles()

		a.post(func() {
			if a.popup != nil {
				return
			}

			var images []agent.Content
			for _, content := range a.pendingContent {
				if content.File != nil {
					images = append(images, content)
				}
			}

			items := make([]PopupItem, 0, len(images)+len(files))
			for i := range images {
				items = append(items, PopupItem{ID: fmt.Sprintf("image:%d", i), Label: fmt.Sprintf("image %d", i+1), Detail: "pasted image"})
			}
			for _, f := range files {
				items = append(items, PopupItem{ID: contextFileID(f.Path), Label: f.Path, Detail: "workspace file"})
			}

			popup := newPopup(popupList, "context (space to mark, enter to apply)", items, func(ids []string) {
				a.applyContextSelection(images, files, ids)
				count := a.countPendingImages() + len(a.pendingFiles)
				a.showToast(fmt.Sprintf("Context: %d attachment(s)", count), theme.Default.Cyan)
			})
			popup.multi = true
			popup.acceptEmpty = true
			popup.labelOnly = true
			for i := range images {
				popup.SetSelected(fmt.Sprintf("image:%d", i), true)
			}
			for _, path := range a.pendingFiles {
				popup.SetSelected(contextFileID(path), true)
			}
			a.popup = popup
			a.invalidate()
		})
	}()
}
