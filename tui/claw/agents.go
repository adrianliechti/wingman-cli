package claw

func (t *TUI) selected() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.selectedAgent
}

func (t *TUI) refreshAgents() {
	agents, err := t.claw.ListAgents()
	if err != nil {
		return
	}

	t.agentList.Clear()

	for _, name := range agents {
		t.agentList.AddItem("  "+name, "", 0, nil)
	}
}

func (t *TUI) selectAgent(name string) {
	t.mu.Lock()
	t.selectedAgent = name
	t.mu.Unlock()

	t.chatView.Clear()

	a := t.claw.GetAgent(name)
	if a != nil {
		t.renderMessages(a.Messages)
	}

	t.refreshTasks()
	t.updateStatusBar()
}
