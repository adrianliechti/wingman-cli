package claw

import (
	"context"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/claw/channel"
)

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

func nameFromChatID(chatID string) string {
	if _, name, ok := strings.Cut(chatID, ":"); ok {
		return name
	}

	return chatID
}
