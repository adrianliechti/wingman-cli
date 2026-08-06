package code

import (
	"cmp"
	"slices"
	"strings"
)

const commandCenterTitle = "commands"

func builtinGroup(name string) string {
	switch name {
	case "/diff", "/problems", "/context":
		return "Workspace"
	case "/resume", "/recap", "/clear":
		return "Session"
	case "/model", "/plan", "/agent", "/tasks":
		return "Agent"
	default:
		return "Application"
	}
}

func builtinShortcut(name string) string {
	switch name {
	case "/plan", "/agent":
		return "tab"
	case "/clear":
		return "ctrl+l"
	case "/quit":
		return "ctrl+c ×2"
	default:
		return ""
	}
}

func (a *App) showCommandCenter() {
	runs := map[string]func(){}
	var items []PopupItem
	busy := a.isStreaming()

	add := func(item PopupItem, run func()) {
		items = append(items, item)
		runs[item.ID] = run
	}

	add(PopupItem{
		ID: "action:transcript", Label: "Open transcript", Detail: "Search and inspect the complete session",
		Group: "Workspace", Keywords: "history tools reasoning activity", Shortcut: "ctrl+o",
	}, a.showTranscript)
	add(PopupItem{
		ID: "action:attachments", Label: "Manage context", Detail: "Attach or remove images and workspace files",
		Group: "Workspace", Keywords: "files images context attach", Shortcut: "@", Disabled: busy,
		DisabledReason: "available when the current turn finishes",
	}, a.showFilePicker)

	if busy {
		add(PopupItem{
			ID: "action:interrupt", Label: "Interrupt current turn", Detail: "Stop active model and tool work",
			Group: "Agent", Keywords: "cancel stop escape",
		}, a.cancelStream)
	}

	for _, cmd := range a.builtinCommands() {
		if cmd.Name == "/help" {
			continue
		}
		command := cmd
		item := PopupItem{
			ID: "builtin:" + command.Name, Label: command.Name, Detail: command.Desc,
			Group: builtinGroup(command.Name), Keywords: strings.TrimPrefix(command.Name, "/"),
			Shortcut: builtinShortcut(command.Name), Disabled: busy && !command.Busy,
			DisabledReason: "available when the current turn finishes",
		}
		switch command.Name {
		case "/plan":
			item.Checked = a.currentMode == ModePlan
		case "/agent":
			item.Checked = a.currentMode == ModeAgent
		}

		run := func() { command.Run(a) }
		switch command.Name {
		case "/model":
			run = func() { a.showModelPickerLevel(true) }
		case "/resume":
			run = func() { a.showSessionPicker(true) }
		}
		add(item, run)
	}

	for _, cmd := range a.skillCommands() {
		command := cmd
		add(PopupItem{
			ID: "skill:" + command.Name, Label: command.Name, Detail: command.Desc,
			Group: "Skills", Keywords: "workflow skill", Disabled: busy,
			DisabledReason: "available when the current turn finishes",
		}, func() { a.insertPaletteCommand(command.Name) })
	}
	for _, cmd := range a.agentCommands() {
		command := cmd
		add(PopupItem{
			ID: "agent-command:" + command.Name, Label: command.Name, Detail: command.Desc,
			Group: "Agent commands", Keywords: "provider command", Disabled: busy,
			DisabledReason: "available when the current turn finishes",
		}, func() { a.insertPaletteCommand(command.Name) })
	}

	groupOrder := map[string]int{
		"Workspace": 0, "Session": 1, "Agent": 2, "Skills": 3,
		"Agent commands": 4, "Application": 5,
	}
	slices.SortStableFunc(items, func(left, right PopupItem) int {
		return cmp.Compare(groupOrder[left.Group], groupOrder[right.Group])
	})

	popup := newPopup(popupPalette, commandCenterTitle, items, func(ids []string) {
		if run := runs[ids[0]]; run != nil {
			run()
		}
	})
	a.popup = popup
	a.invalidate()
}

// refreshCommandCenter rebuilds an open command center when the busy state
// flips, preserving the query and, when still listed, the selection.
func (a *App) refreshCommandCenter() {
	previous := a.popup
	if previous == nil || previous.kind != popupPalette || previous.title != commandCenterTitle {
		return
	}

	a.showCommandCenter()
	a.popup.SetQuery(previous.query)
	if item, ok := previous.Current(); ok {
		a.popup.SelectID(item.ID)
	}
}

func (a *App) insertPaletteCommand(command string) {
	a.editor.Insert(command + " ")
	a.syncCommandPopup()
	a.invalidate()
}
