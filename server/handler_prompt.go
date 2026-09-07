package server

import (
	"context"
	"errors"
	"sort"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/google/uuid"
)

type pendingPrompt struct {
	view  PromptView
	reply chan Command
}

func (b *backendRuntime) Elicit(ctx context.Context, req tool.ElicitRequest) (tool.ElicitResult, error) {
	reply, err := b.prompt(ctx, PromptKindAsk, req.Message, req.Fields)
	if err != nil {
		return tool.ElicitResult{}, err
	}
	return tool.ElicitResult{Action: tool.ElicitAction(reply.Action), Content: reply.Content}, nil
}

func (b *backendRuntime) Confirm(ctx context.Context, message string) (bool, error) {
	sid := code.SessionIDFromContext(ctx)
	if sid == "" {
		return false, errors.New("prompt requires an explicit session")
	}
	c := b.session(sid)
	c.mu.Lock()
	all := c.confirmAll && c.state.Status != "deleted" && ctx.Err() == nil && b.ctx.Err() == nil
	c.mu.Unlock()
	if all {
		return true, nil
	}
	reply, err := b.prompt(ctx, PromptKindConfirm, message, nil)
	if err != nil {
		return false, err
	}
	return reply.Action == string(tool.ElicitAccept), nil
}

func (b *backendRuntime) prompt(ctx context.Context, kind, message string, fields []tool.ElicitField) (Command, error) {
	sid := code.SessionIDFromContext(ctx)
	if sid == "" {
		return Command{}, errors.New("prompt requires an explicit session")
	}
	c := b.session(sid)
	id := uuid.NewString()
	p := pendingPrompt{view: PromptView{ID: id, Kind: kind, Message: message, Fields: fields}, reply: make(chan Command, 1)}
	c.mu.Lock()
	if c.state.Status == "deleted" {
		c.mu.Unlock()
		return Command{}, errors.New("session deleted")
	}
	c.prompts[id] = p
	c.publishPromptsLocked()
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.prompts, id); c.publishPromptsLocked(); c.mu.Unlock() }()
	select {
	case reply := <-p.reply:
		return reply, nil
	case <-ctx.Done():
		return Command{}, ctx.Err()
	case <-b.ctx.Done():
		return Command{}, b.ctx.Err()
	}
}

func (c *sessionController) publishPromptsLocked() {
	c.state.Prompts = make([]PromptView, 0, len(c.prompts))
	for _, p := range c.prompts {
		c.state.Prompts = append(c.state.Prompts, p.view)
	}
	sort.Slice(c.state.Prompts, func(i, j int) bool { return c.state.Prompts[i].ID < c.state.Prompts[j].ID })
	c.publishStateLocked()
}

func (c *sessionController) resolvePrompt(command Command) error {
	switch tool.ElicitAction(command.Action) {
	case tool.ElicitAccept, tool.ElicitDecline, tool.ElicitCancel:
	default:
		return errors.New("invalid prompt action")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.resolvePromptLocked(command)
}

func (c *sessionController) resolvePromptLocked(command Command) error {
	if c.state.Status == "deleted" || c.backend.ctx.Err() != nil {
		return errors.New("session closed")
	}
	p, ok := c.prompts[command.PromptID]
	if !ok {
		return errors.New("prompt already resolved or expired")
	}
	// Removal is the atomic winner selection for responses from multiple clients.
	delete(c.prompts, command.PromptID)
	if p.view.Kind == PromptKindConfirm && command.Action == string(tool.ElicitAccept) && command.Scope == PromptScopeSession {
		c.confirmAll = true
	}
	p.reply <- command
	c.publishPromptsLocked()
	return nil
}
