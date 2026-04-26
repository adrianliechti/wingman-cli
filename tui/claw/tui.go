package claw

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/rivo/tview"

	"github.com/adrianliechti/wingman-agent/pkg/claw"
	"github.com/adrianliechti/wingman-agent/pkg/claw/channel"
	"github.com/adrianliechti/wingman-agent/pkg/tui"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
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
		tui.FormatTokens(a.Usage.InputTokens),
		tui.FormatTokens(a.Usage.OutputTokens),
		th.BrBlack,
		th.Cyan,
		name,
	)
}

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
