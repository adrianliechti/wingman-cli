package tui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/claw"
	"github.com/adrianliechti/wingman-agent/pkg/claw/channel"
	"github.com/adrianliechti/wingman-agent/pkg/claw/tool/schedule"
	"github.com/adrianliechti/wingman-agent/pkg/ui/markdown"
	"github.com/adrianliechti/wingman-agent/pkg/ui/theme"
)

type TUI struct {
	claw    *claw.Claw
	app     *tview.Application
	handler channel.MessageHandler

	agentList *tview.List
	taskView  *tview.TextView
	chatView  *tview.TextView
	input     *tview.InputField
	statusBar *tview.TextView

	selectedAgent string
	chatWidth     int
	mu            sync.Mutex
}

func New(c *claw.Claw) *TUI {
	return &TUI{
		claw:          c,
		selectedAgent: "main",
	}
}

// Channel interface

func (t *TUI) Name() string { return "cli" }

func (t *TUI) Start(ctx context.Context, handler channel.MessageHandler) error {
	t.handler = handler

	theme.Auto()
	t.buildUI()
	t.refreshAgents()
	t.selectAgent("main")

	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				t.app.QueueUpdateDraw(func() {
					t.refreshTasks()
				})
			}
		}
	}()

	return t.app.Run()
}

func (t *TUI) Send(ctx context.Context, chatID string, text string) error {
	name := nameFromChatID(chatID)

	t.app.QueueUpdateDraw(func() {
		if name == t.selected() {
			t.writeFormatted(text, true)
			t.chatView.ScrollToEnd()
		}
	})

	return nil
}

func (t *TUI) SendStream(ctx context.Context, chatID string) (io.WriteCloser, error) {
	return &streamWriter{
		tui:  t,
		name: nameFromChatID(chatID),
	}, nil
}

// UI

func (t *TUI) buildUI() {
	th := theme.Default
	t.app = tview.NewApplication()

	// Agent list
	t.agentList = tview.NewList()
	t.agentList.SetBorder(false)
	t.agentList.SetHighlightFullLine(true)
	t.agentList.ShowSecondaryText(false)
	t.agentList.SetMainTextColor(th.Foreground)
	t.agentList.SetSelectedTextColor(th.Cyan)
	t.agentList.SetSelectedBackgroundColor(th.Selection)
	t.agentList.SetSelectedFunc(func(index int, name string, _ string, _ rune) {
		t.selectAgent(strings.TrimSpace(name))
		t.app.SetFocus(t.input)
	})

	// Task view
	t.taskView = tview.NewTextView()
	t.taskView.SetBorder(false)
	t.taskView.SetDynamicColors(true)
	t.taskView.SetWordWrap(true)

	// Chat view
	t.chatView = tview.NewTextView()
	t.chatView.SetBorder(false)
	t.chatView.SetDynamicColors(true)
	t.chatView.SetWordWrap(false)
	t.chatView.SetScrollable(true)
	t.chatView.SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		t.chatWidth = width
		return x, y, width, height
	})
	t.chatView.SetChangedFunc(func() {
		t.app.Draw()
	})

	// Input
	t.input = tview.NewInputField()
	t.input.SetLabel("  \u276f ")
	t.input.SetLabelColor(th.Cyan)
	t.input.SetFieldBackgroundColor(th.Selection)
	t.input.SetFieldTextColor(th.Foreground)
	t.input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			t.submitInput()
		}
	})

	// Status bar
	t.statusBar = tview.NewTextView()
	t.statusBar.SetDynamicColors(true)
	t.statusBar.SetTextAlign(tview.AlignRight)

	// Sidebar
	sidebarTitle := tview.NewTextView()
	sidebarTitle.SetDynamicColors(true)
	sidebarTitle.SetText(fmt.Sprintf("\n  [%s::b]Agents[-::-]\n", th.Cyan))

	taskTitle := tview.NewTextView()
	taskTitle.SetDynamicColors(true)
	taskTitle.SetText(fmt.Sprintf("\n  [%s::b]Tasks[-::-]\n", th.Yellow))

	// Sidebar: just agents
	sidebar := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(sidebarTitle, 3, 0, false).
		AddItem(t.agentList, 0, 1, true)

	// Vertical separator
	vSep := tview.NewBox().SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		for row := y; row < y+height; row++ {
			screen.SetContent(x, row, '\u2502', nil, tcell.StyleDefault.Foreground(th.BrBlack))
		}
		return x + 1, y, width - 1, height
	})

	// Horizontal separator
	hSep := tview.NewBox().SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		for col := x; col < x+width; col++ {
			screen.SetContent(col, y, '\u2500', nil, tcell.StyleDefault.Foreground(th.BrBlack))
		}
		return x, y + 1, width, height - 1
	})

	// Bottom bar
	bottom := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(t.input, 1, 0, false).
		AddItem(t.statusBar, 1, 0, false)

	// Task panel (title + scrollable tasks)
	taskPanel := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(taskTitle, 3, 0, false).
		AddItem(t.taskView, 0, 1, false)

	// Right side: tasks (1/3) + separator + chat (2/3) + bottom
	rightSide := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(taskPanel, 0, 1, false).
		AddItem(hSep, 1, 0, false).
		AddItem(t.chatView, 0, 2, false).
		AddItem(bottom, 2, 0, false)

	// Root
	root := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(sidebar, 24, 0, true).
		AddItem(vSep, 1, 0, false).
		AddItem(rightSide, 0, 1, false)

	t.app.SetRoot(root, true)
	t.app.SetFocus(t.input)
	t.app.EnableMouse(true)

	t.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyTab:
			t.cycleFocus()
			return nil
		case tcell.KeyCtrlC:
			t.app.Stop()
			return nil
		}

		return event
	})
}

func (t *TUI) cycleFocus() {
	switch t.app.GetFocus() {
	case t.input:
		t.app.SetFocus(t.agentList)
	case t.agentList:
		t.app.SetFocus(t.input)
	default:
		t.app.SetFocus(t.input)
	}
}

// Agent management

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

const (
	indent   = "    "
	barWidth = 4 // "┃ " + padding
)

func (t *TUI) contentWidth() int {
	w := t.chatWidth - len(indent) - barWidth - 4 // 4 for right margin
	if w < 40 {
		return 40
	}

	return w
}

func (t *TUI) renderMessages(messages []agent.Message) {
	for _, msg := range messages {
		for _, c := range msg.Content {
			switch {
			case c.Text != "":
				switch msg.Role {
				case agent.RoleUser:
					t.writeFormatted(c.Text, false)
				case agent.RoleAssistant:
					t.writeFormatted(c.Text, true)
				}
			case c.ToolCall != nil:
				t.writeToolCall(c.ToolCall)
			}
		}
	}

	t.chatView.ScrollToEnd()
}

func (t *TUI) writeFormatted(text string, isAssistant bool) {
	th := theme.Default

	barColor := th.Cyan
	content := strings.TrimRight(text, "\n")

	if isAssistant {
		barColor = th.Blue
		content = strings.TrimRight(markdown.Render(content), "\n")
	}

	for line := range strings.SplitSeq(content, "\n") {
		for _, wl := range markdown.WrapLine(line, t.contentWidth()) {
			fmt.Fprintf(t.chatView, "%s[%s]\u2503[-] %s\n", indent, barColor, wl)
		}
	}

	fmt.Fprintln(t.chatView)
}

func (t *TUI) writeToolCall(tc *agent.ToolCall) {
	th := theme.Default
	hint := extractToolHint(tc.Args)

	if hint != "" {
		hint = " " + hint
	}

	fmt.Fprintf(t.chatView, "%s[%s]\u2503[-] [%s::b]\u25c8 %s[-::-][%s]%s[-]\n",
		indent, th.BrBlack, th.Yellow, tc.Name, th.BrBlack, hint)
}

// Tasks

func (t *TUI) refreshTasks() {
	th := theme.Default
	name := t.selected()
	agentDir := t.claw.AgentDir(name)
	tasks := schedule.LoadTasks(agentDir)

	t.taskView.Clear()
	now := time.Now()

	if len(tasks) == 0 {
		fmt.Fprintf(t.taskView, "  [%s]no tasks[-]", th.BrBlack)
		return
	}

	for _, task := range tasks {
		icon := "[green]\u25cf[-]"
		if task.Status == "paused" {
			icon = fmt.Sprintf("[%s]\u25cb[-]", th.BrBlack)
		}

		next := schedule.NextRun(task, now)
		nextStr := ""

		if !next.IsZero() {
			dur := next.Sub(now)
			if dur < 0 {
				nextStr = fmt.Sprintf(" [%s]overdue[-]", th.Red)
			} else if dur < time.Hour {
				nextStr = fmt.Sprintf(" [%s]%dm[-]", th.Green, int(dur.Minutes())+1)
			} else {
				nextStr = fmt.Sprintf(" [%s]%s[-]", th.BrBlack, next.Format("15:04"))
			}
		}

		prompt := task.Prompt
		if len(prompt) > 80 {
			prompt = prompt[:77] + "..."
		}

		fmt.Fprintf(t.taskView, "  %s [%s]%s[-]%s  [%s]%s[-]\n", icon, th.Foreground, humanSchedule(task.Schedule), nextStr, th.BrBlack, prompt)
	}
}

// Input

func (t *TUI) submitInput() {
	text := strings.TrimSpace(t.input.GetText())
	t.input.SetText("")

	if text == "" {
		return
	}

	name := t.selected()

	t.writeFormatted(text, false)
	t.chatView.ScrollToEnd()

	msg := channel.Message{
		ChatID:    "cli:" + name,
		Sender:    "user",
		Content:   text,
		Timestamp: time.Now(),
	}

	go func() {
		ctx := context.Background()
		t.handler(ctx, msg)

		t.app.QueueUpdateDraw(func() {
			t.updateStatusBar()
		})
	}()
}

// Status bar

func (t *TUI) updateStatusBar() {
	th := theme.Default
	name := t.selected()
	a := t.claw.GetAgent(name)

	t.statusBar.Clear()

	if a == nil {
		fmt.Fprintf(t.statusBar, "  [%s]%s[-] ", th.Cyan, name)
		return
	}

	fmt.Fprintf(t.statusBar, "  [%s]\u2191%s \u2193%s[-] [%s]\u2503[-] [%s]%s[-] ",
		th.BrBlack,
		formatTokens(a.Usage.InputTokens),
		formatTokens(a.Usage.OutputTokens),
		th.BrBlack,
		th.Cyan,
		name,
	)
}

func formatTokens(tokens int64) string {
	if tokens >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(tokens)/1_000_000)
	}

	if tokens >= 1000 {
		return fmt.Sprintf("%.1fK", float64(tokens)/1000)
	}

	return fmt.Sprintf("%d", tokens)
}

// Stream writer

type streamWriter struct {
	tui     *TUI
	name    string
	started bool
}

func (w *streamWriter) Write(p []byte) (int, error) {
	text := string(p)
	th := theme.Default

	w.tui.app.QueueUpdateDraw(func() {
		if w.name != w.tui.selected() {
			return
		}

		if !w.started {
			fmt.Fprintf(w.tui.chatView, "%s[%s]\u2503[-] ", indent, th.Blue)
			w.started = true
		}

		fmt.Fprint(w.tui.chatView, text)
		w.tui.chatView.ScrollToEnd()
	})

	return len(p), nil
}

func (w *streamWriter) Close() error {
	w.tui.app.QueueUpdateDraw(func() {
		if w.name == w.tui.selected() {
			fmt.Fprintln(w.tui.chatView)
		}
	})

	return nil
}

// Helpers

func nameFromChatID(chatID string) string {
	if _, name, ok := strings.Cut(chatID, ":"); ok {
		return name
	}

	return chatID
}

func humanSchedule(sched string) string {
	// Interval: "every 15m" -> "every 15 min"
	if strings.HasPrefix(sched, "every ") {
		d, err := time.ParseDuration(strings.TrimPrefix(sched, "every "))
		if err != nil {
			return sched
		}

		if d < time.Minute {
			return fmt.Sprintf("every %ds", int(d.Seconds()))
		}

		if d < time.Hour {
			return fmt.Sprintf("every %d min", int(d.Minutes()))
		}

		if d == time.Hour {
			return "every hour"
		}

		if d%time.Hour == 0 {
			h := int(d.Hours())
			if h == 24 {
				return "daily"
			}

			return fmt.Sprintf("every %dh", h)
		}

		return fmt.Sprintf("every %s", d)
	}

	// One-time: ISO timestamp -> "Apr 15, 09:00"
	if ts, err := time.Parse(time.RFC3339, sched); err == nil {
		now := time.Now()

		if ts.Year() == now.Year() && ts.YearDay() == now.YearDay() {
			return "today " + ts.Format("15:04")
		}

		tomorrow := now.AddDate(0, 0, 1)

		if ts.Year() == tomorrow.Year() && ts.YearDay() == tomorrow.YearDay() {
			return "tomorrow " + ts.Format("15:04")
		}

		return ts.Format("Jan 2, 15:04")
	}

	// Cron: parse common patterns
	fields := strings.Fields(sched)

	if len(fields) >= 5 {
		min, hour, dom, _, dow := fields[0], fields[1], fields[2], fields[3], fields[4]

		// "0 9 * * *" -> "daily at 09:00"
		if dom == "*" && dow == "*" && min != "*" && hour != "*" {
			return fmt.Sprintf("daily at %s:%s", zeroPad(hour), zeroPad(min))
		}

		// "0 9 * * 1-5" -> "weekdays at 09:00"
		if dom == "*" && dow == "1-5" && min != "*" && hour != "*" {
			return fmt.Sprintf("weekdays at %s:%s", zeroPad(hour), zeroPad(min))
		}

		// "0 9 * * 1" -> "Mon at 09:00"
		dayNames := map[string]string{"0": "Sun", "1": "Mon", "2": "Tue", "3": "Wed", "4": "Thu", "5": "Fri", "6": "Sat", "7": "Sun"}

		if dom == "*" && min != "*" && hour != "*" {
			if name, ok := dayNames[dow]; ok {
				return fmt.Sprintf("%s at %s:%s", name, zeroPad(hour), zeroPad(min))
			}
		}

		// "*/15 * * * *" -> "every 15 min"
		if strings.HasPrefix(min, "*/") && hour == "*" && dom == "*" && dow == "*" {
			return fmt.Sprintf("every %s min", strings.TrimPrefix(min, "*/"))
		}
	}

	return sched
}

func zeroPad(s string) string {
	if len(s) == 1 {
		return "0" + s
	}

	return s
}

func extractToolHint(argsJSON string) string {
	// Quick extract of common hint keys from JSON args
	for _, key := range []string{"command", "path", "query", "url", "prompt", "pattern"} {
		needle := fmt.Sprintf(`"%s":"`, key)
		if idx := strings.Index(argsJSON, needle); idx >= 0 {
			start := idx + len(needle)
			end := strings.Index(argsJSON[start:], `"`)

			if end > 0 && end < 60 {
				return argsJSON[start : start+end]
			}
		}
	}

	return ""
}
