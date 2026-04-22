package claw

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/claw/tool/schedule"
)

const (
	schedulerOK   = "SCHEDULER_OK"
	schedulerTick = 1 * time.Minute
)

func (c *Claw) startScheduler(ctx context.Context, name string, ma *managedAgent) {
	// Run overdue tasks immediately on startup
	c.tickScheduler(ctx, name, ma)

	ticker := time.NewTicker(schedulerTick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.tickScheduler(ctx, name, ma)
		}
	}
}

func (c *Claw) tickScheduler(ctx context.Context, name string, ma *managedAgent) {
	agentDir := c.config.Memory.AgentDir(name)
	tasks := schedule.LoadTasks(agentDir)

	if len(tasks) == 0 {
		return
	}

	now := time.Now()
	var duePrompts []string
	modified := false

	for i := range tasks {
		t := &tasks[i]

		if !schedule.IsDue(*t, now) {
			continue
		}

		duePrompts = append(duePrompts, t.Prompt)
		t.LastRun = &now
		modified = true

		// Mark completed one-time tasks
		if _, err := time.Parse(time.RFC3339, t.Schedule); err == nil {
			t.Status = "completed"
		}
	}

	if !modified {
		return
	}

	// Remove completed tasks and save
	var active []schedule.Task

	for _, t := range tasks {
		if t.Status != "completed" {
			active = append(active, t)
		}
	}

	schedule.SaveTasks(agentDir, active)

	if len(duePrompts) == 0 {
		return
	}

	var prompt strings.Builder
	prompt.WriteString("The following scheduled tasks are due:\n\n")

	for _, p := range duePrompts {
		fmt.Fprintf(&prompt, "- %s\n", p)
	}

	prompt.WriteString("\nExecute each task. If nothing needs attention, reply with exactly: SCHEDULER_OK")

	c.runScheduledTask(ctx, name, ma, prompt.String())
}

func (c *Claw) runScheduledTask(ctx context.Context, name string, ma *managedAgent, prompt string) {
	input := []agent.Content{{Text: prompt}}

	var result strings.Builder

	for msg, err := range ma.agent.Send(ctx, input) {
		if err != nil {
			log.Printf("scheduler %s: error: %v", name, err)
			return
		}

		for _, content := range msg.Content {
			if content.Text != "" {
				result.WriteString(content.Text)
			}
		}
	}

	text := strings.TrimSpace(result.String())

	if text == "" || strings.HasPrefix(text, schedulerOK) {
		return
	}

	c.saveSession(name, ma)

	chatID := "cli:" + name
	if ch := c.findChannel(chatID); ch != nil {
		ch.Send(ctx, chatID, text)
	}
}
