package claw

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool/schedule"
	"github.com/adrianliechti/wingman-agent/pkg/claw/channel"
)

const (
	schedulerOK   = "SCHEDULER_OK"
	schedulerTick = 1 * time.Minute

	runTimeout = 1 * time.Hour
)

func (c *Claw) startScheduler(name string, ma *managedAgent) {
	ctx, cancel := context.WithCancel(c.runCtx)
	ma.cancel = cancel

	go func() {
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
	}()
}

func (c *Claw) tickScheduler(ctx context.Context, name string, ma *managedAgent) {
	store := schedule.NewFileStore(c.config.Memory.AgentDir(name))

	tasks, err := store.List()
	if err != nil {
		log.Printf("scheduler %s: failed to load tasks: %v", name, err)
		return
	}

	now := time.Now()

	ma.pruneScratch(now)

	for _, t := range tasks {
		if ctx.Err() != nil {
			return
		}
		if schedule.IsDue(t, now) {
			c.runTask(ctx, name, ma, store, t)
		}
	}
}

func (ma *managedAgent) pruneScratch(now time.Time) {
	if ma.scratch == "" || now.Sub(ma.lastPrune) < time.Hour {
		return
	}
	ma.lastPrune = now

	entries, err := os.ReadDir(ma.scratch)
	if err != nil {
		return
	}

	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > 24*time.Hour {
			os.Remove(filepath.Join(ma.scratch, e.Name()))
		}
	}
}

func (c *Claw) runTask(ctx context.Context, name string, ma *managedAgent, store *schedule.FileStore, t schedule.Task) {
	ok := true
	wake := true

	var gateOutput string

	if t.Script != "" {
		wake, gateOutput = schedule.RunGate(ctx, c.agentWorkDir(name), t.Script)
	}

	if wake {
		prompt := fmt.Sprintf("Scheduled task %s is due:\n\n%s", t.ID, t.Prompt)

		if gateOutput != "" {
			prompt += fmt.Sprintf("\n\nPre-check script output:\n\n%s", gateOutput)
		}

		prompt += "\n\nExecute the task. If nothing needs the user's attention, reply with exactly: SCHEDULER_OK"

		ok = c.runScheduledTask(ctx, name, ma, prompt)
		if !ok {
			log.Printf("scheduler %s: task %s failed; retrying with backoff", name, t.ID)
		}
	}

	// a canceled scheduler means shutdown or agent deletion; writing
	// tasks.yaml now could resurrect a removed agent directory
	if ctx.Err() != nil {
		return
	}

	now := time.Now()

	err := store.Mutate(func(tasks []schedule.Task) ([]schedule.Task, error) {
		var kept []schedule.Task
		for i := range tasks {
			if tasks[i].ID == t.ID {
				if ok {
					if schedule.IsOneTime(tasks[i].Schedule) {
						continue
					}
					tasks[i].LastRun = &now
					tasks[i].Failures = 0
					tasks[i].LastAttempt = nil
				} else {
					tasks[i].Failures++
					tasks[i].LastAttempt = &now
				}
			}
			kept = append(kept, tasks[i])
		}
		return kept, nil
	})
	if err != nil {
		log.Printf("scheduler %s: failed to save tasks: %v", name, err)
	}
}

func (c *Claw) runScheduledTask(ctx context.Context, name string, ma *managedAgent, prompt string) bool {
	ma.mu.Lock()
	defer ma.mu.Unlock()

	checkpoint := len(ma.agent.Messages)
	revision := ma.agent.Revision

	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()

	var buf strings.Builder
	var tw *turnText
	resetOutput := func() {
		buf.Reset()
		tw = &turnText{sink: func(s string) { buf.WriteString(s) }}
	}
	resetOutput()
	ctx = agent.WithStreamEventHandlers(ctx, agent.StreamEventHandlers{
		Reset: resetOutput,
	})

	stream, err := ma.agent.Send(ctx, []agent.Content{{Text: prompt}})
	if err != nil {
		log.Printf("scheduler %s: could not start turn: %v", name, err)
		return false
	}

	rollback := func() {
		if ma.agent.Revision == revision && len(ma.agent.Messages) >= checkpoint {
			ma.agent.Messages = ma.agent.Messages[:checkpoint]
		}
	}

	for msg, err := range stream {
		if err != nil {
			log.Printf("scheduler %s: error: %v", name, err)
			rollback()
			return false
		}

		tw.add(msg)
	}

	text := strings.TrimSpace(buf.String())
	text = strings.TrimSpace(strings.TrimPrefix(text, schedulerOK))
	text = strings.TrimSpace(strings.TrimSuffix(text, schedulerOK))

	if text == "" {
		rollback()
		return true
	}

	c.saveSession(name, ma)
	ma.updateSnapshot()

	route := ma.notifyRoute
	if route == (channel.Route{}) {
		primary := c.config.Channels[len(c.config.Channels)-1]
		route = channel.Route{Channel: primary.Name(), Conversation: name}
	}

	if ch := c.findChannel(route.Channel); ch != nil {
		if err := ch.Send(ctx, route.Conversation, text); err != nil {
			log.Printf("scheduler %s: failed to deliver report to %s/%s: %v (kept in session history)", name, route.Channel, route.Conversation, err)
		}
	} else {
		log.Printf("scheduler %s: no channel %q; report kept in session history", name, route.Channel)
	}

	return true
}
