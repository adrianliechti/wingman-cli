package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/claw/channel"
)

type Channel struct {
	chatID string
}

func New() *Channel {
	return &Channel{
		chatID: "cli:main",
	}
}

func (c *Channel) Name() string { return "cli" }

func (c *Channel) Start(ctx context.Context, handler channel.MessageHandler) error {
	lines := make(chan string)

	go func() {
		defer close(lines)

		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()

	c.printPrompt()

	for {
		select {
		case <-ctx.Done():
			return nil

		case line, ok := <-lines:
			if !ok {
				return nil
			}

			text := strings.TrimSpace(line)
			if text == "" {
				c.printPrompt()
				continue
			}

			if text == "/exit" || text == "/quit" {
				return nil
			}

			// Switch agent: /agent <name>
			if strings.HasPrefix(text, "/agent ") {
				name := strings.TrimSpace(strings.TrimPrefix(text, "/agent "))
				if name != "" {
					c.chatID = "cli:" + name
					fmt.Printf("Switched to agent: %s\n", name)
				}

				c.printPrompt()
				continue
			}

			// Show current agent
			if text == "/agent" {
				fmt.Printf("Current agent: %s\n", c.agentName())
				c.printPrompt()
				continue
			}

			msg := channel.Message{
				ChatID:    c.chatID,
				Sender:    "user",
				Content:   text,
				Timestamp: time.Now(),
			}

			handler(ctx, msg)
			c.printPrompt()
		}
	}
}

func (c *Channel) Send(ctx context.Context, chatID string, text string) error {
	fmt.Println(text)
	return nil
}

func (c *Channel) SendStream(ctx context.Context, chatID string) (io.WriteCloser, error) {
	return &streamWriter{}, nil
}

func (c *Channel) agentName() string {
	if _, name, ok := strings.Cut(c.chatID, ":"); ok {
		return name
	}

	return c.chatID
}

func (c *Channel) printPrompt() {
	fmt.Printf("[%s] > ", c.agentName())
}

type streamWriter struct{}

func (w *streamWriter) Write(p []byte) (int, error) {
	return os.Stdout.Write(p)
}

func (w *streamWriter) Close() error {
	fmt.Println()
	return nil
}
