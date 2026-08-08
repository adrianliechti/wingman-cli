package code

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
)

type managedTurnInput struct {
	input TurnInput
}

type managedTurnSession struct {
	mu sync.Mutex

	active  *managedTurnInput
	queued  []*managedTurnInput
	steered []*managedTurnInput

	cancel          context.CancelFunc
	cancelRequested bool
	paused          bool
	running         bool
	ids             map[string]struct{}
}

// TurnManager provides the session-level execution semantics shared by the
// web UI and other multi-turn clients. It guarantees one Agent.Send call per
// session at a time, supplies a FIFO follow-up queue for every backend, and
// uses native steering only when the backend explicitly supports it.
type TurnManager struct {
	ctx    context.Context
	cancel context.CancelFunc
	agent  Agent

	handlerMu sync.RWMutex
	handler   func(TurnEvent)

	mu       sync.Mutex
	sessions map[string]*managedTurnSession
}

func NewTurnManager(ctx context.Context, a Agent, handler func(TurnEvent)) *TurnManager {
	ctx, cancel := context.WithCancel(ctx)
	return &TurnManager{
		ctx: ctx, cancel: cancel, agent: a, handler: handler,
		sessions: make(map[string]*managedTurnSession),
	}
}

func (m *TurnManager) SetHandler(handler func(TurnEvent)) {
	m.handlerMu.Lock()
	m.handler = handler
	m.handlerMu.Unlock()
}

func (m *TurnManager) emit(ev TurnEvent) {
	m.handlerMu.RLock()
	h := m.handler
	m.handlerMu.RUnlock()
	if h != nil {
		h(ev)
	}
}

func (m *TurnManager) session(id string) *managedTurnSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		s = &managedTurnSession{}
		m.sessions[id] = s
	}
	return s
}

func (m *TurnManager) Features(sessionID string) TurnFeatures {
	f := TurnFeatures{}
	if p, ok := m.agent.(TurnFeatureProvider); ok {
		provided := p.TurnFeatures(sessionID)
		f.Steer = provided.Steer
	}
	if _, ok := m.agent.(TurnSteerer); !ok {
		f.Steer = false
	}
	return f
}

// Submit accepts an input exactly once. A steer that loses a turn-boundary
// race automatically becomes a FIFO follow-up instead of being discarded.
func (m *TurnManager) Submit(ctx context.Context, sessionID string, input TurnInput) (TurnInputSnapshot, error) {
	if sessionID == "" {
		return TurnInputSnapshot{}, errors.New("session id required")
	}
	if input.ID == "" {
		return TurnInputSnapshot{}, errors.New("input id required")
	}
	if len(input.Content) == 0 {
		return TurnInputSnapshot{}, errors.New("input content required")
	}
	switch input.Intent {
	case "":
		input.Intent = TurnInputFollowUp
	case TurnInputFollowUp, TurnInputSteer:
	default:
		return TurnInputSnapshot{}, fmt.Errorf("%w: %q", ErrInvalidIntent, input.Intent)
	}
	input.Content = agent.CloneContent(input.Content)

	s := m.session(sessionID)
	s.mu.Lock()
	if s.ids == nil {
		s.ids = make(map[string]struct{})
	}
	if _, exists := s.ids[input.ID]; exists {
		s.mu.Unlock()
		return TurnInputSnapshot{}, ErrDuplicateInput
	}
	s.ids[input.ID] = struct{}{}
	s.mu.Unlock()
	if input.Intent == TurnInputSteer && m.Features(sessionID).Steer {
		s.mu.Lock()
		target := s.active
		hasActive := target != nil && !s.cancelRequested
		s.mu.Unlock()
		if hasActive {
			if steerer, ok := m.agent.(TurnSteerer); ok {
				err := callSteer(ctx, steerer, sessionID, input)
				if err == nil {
					item := &managedTurnInput{input: input}
					s.mu.Lock()
					if s.active == target {
						s.steered = append(s.steered, item)
						s.mu.Unlock()
						snap := TurnInputSnapshot{ID: input.ID, State: TurnInputSteered, Intent: input.Intent}
						m.emit(TurnEvent{SessionID: sessionID, InputID: input.ID, State: TurnInputSteered, Intent: input.Intent})
						return snap, nil
					}
					s.mu.Unlock()
					// The active turn completed after the backend accepted the steer.
					// Surface the accepted user input before completing it; re-queueing
					// here would duplicate an input the backend already owns.
					m.emit(TurnEvent{SessionID: sessionID, InputID: input.ID, State: TurnInputSteered, Intent: input.Intent})
					m.releaseID(s, input.ID)
					m.emit(TurnEvent{SessionID: sessionID, InputID: input.ID, State: TurnInputCompleted, Intent: input.Intent})
					return TurnInputSnapshot{ID: input.ID, State: TurnInputCompleted, Intent: input.Intent}, nil
				}
				if errors.Is(err, ErrNoActiveTurn) || errors.Is(err, ErrTurnNotSteerable) {
					// Turn-boundary races and explicitly non-steerable turn kinds are
					// safe to preserve as FIFO follow-ups.
					input.Intent = TurnInputFollowUp
				} else {
					m.releaseID(s, input.ID)
					return TurnInputSnapshot{}, fmt.Errorf("steer active turn: %w", err)
				}
			}
		}
	}
	// A steer that could not target an active native turn is an ordinary
	// follow-up from this point onward, whether it starts now or waits in FIFO.
	if input.Intent == TurnInputSteer {
		input.Intent = TurnInputFollowUp
	}

	item := &managedTurnInput{input: input}
	s.mu.Lock()
	if s.active == nil && !s.running && !s.paused {
		s.active = item
		s.running = true
		s.cancelRequested = false
		s.mu.Unlock()
		m.emit(TurnEvent{SessionID: sessionID, InputID: input.ID, State: TurnInputActive, Intent: input.Intent})
		go m.runSession(sessionID, s)
		return TurnInputSnapshot{ID: input.ID, State: TurnInputActive, Intent: input.Intent}, nil
	}
	s.queued = append(s.queued, item)
	position := len(s.queued)
	s.mu.Unlock()
	m.emit(TurnEvent{SessionID: sessionID, InputID: input.ID, State: TurnInputQueued, Intent: input.Intent, Position: position})
	return TurnInputSnapshot{ID: input.ID, State: TurnInputQueued, Intent: input.Intent, Position: position}, nil
}

func callSteer(ctx context.Context, steerer TurnSteerer, sessionID string, input TurnInput) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("agent steer panicked: %v", recovered)
		}
	}()
	return steerer.Steer(ctx, sessionID, input)
}

func (m *TurnManager) runSession(sessionID string, s *managedTurnSession) {
	for {
		s.mu.Lock()
		item := s.active
		if item == nil {
			s.running = false
			s.cancel = nil
			s.mu.Unlock()
			return
		}
		runCtx, cancel := context.WithCancel(WithSessionID(m.ctx, sessionID))
		s.cancel = cancel
		cancelBeforeStart := s.cancelRequested
		s.mu.Unlock()

		var runErr error
		if cancelBeforeStart {
			cancel()
			runErr = context.Canceled
		} else {
			runErr = m.executeInput(runCtx, sessionID, item)
		}
		cancel()

		s.mu.Lock()
		cancelled := s.cancelRequested || errors.Is(runErr, context.Canceled)
		s.cancelRequested = false
		s.cancel = nil
		steered := append([]*managedTurnInput(nil), s.steered...)
		s.steered = nil
		s.active = nil
		delete(s.ids, item.input.ID)
		for _, in := range steered {
			delete(s.ids, in.input.ID)
		}

		state := TurnInputCompleted
		if cancelled {
			state = TurnInputCancelled
		} else if runErr != nil {
			state = TurnInputFailed
		}

		var next *managedTurnInput
		if !s.paused && len(s.queued) > 0 {
			next = s.queued[0]
			s.queued = s.queued[1:]
			s.active = next
		} else {
			s.running = false
		}
		s.mu.Unlock()

		m.emit(TurnEvent{SessionID: sessionID, InputID: item.input.ID, State: state, Intent: item.input.Intent, Err: runErr, Executed: true})
		for _, in := range steered {
			m.emit(TurnEvent{SessionID: sessionID, InputID: in.input.ID, State: state, Intent: in.input.Intent, Err: runErr})
		}
		if next == nil {
			return
		}
		m.emit(TurnEvent{SessionID: sessionID, InputID: next.input.ID, State: TurnInputActive, Intent: next.input.Intent})
	}
}

func (m *TurnManager) executeInput(ctx context.Context, sessionID string, item *managedTurnInput) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("agent turn panicked: %v", recovered)
		}
	}()
	ctx = agent.WithStreamEventHandlers(ctx, agent.StreamEventHandlers{
		Reset: func() {
			m.emit(TurnEvent{SessionID: sessionID, InputID: item.input.ID, StreamEvent: agent.StreamEventReset})
		},
		Commit: func() {
			m.emit(TurnEvent{SessionID: sessionID, InputID: item.input.ID, StreamEvent: agent.StreamEventCommit})
		},
	})
	stream, err := m.agent.Send(ctx, sessionID, item.input.Content)
	if err != nil {
		return err
	}
	if stream == nil {
		return errors.New("agent returned a nil turn stream")
	}
	for msg, streamErr := range stream {
		if streamErr != nil {
			return streamErr
		}
		copy := msg
		m.emit(TurnEvent{SessionID: sessionID, InputID: item.input.ID, Message: &copy})
	}
	return nil
}

func (m *TurnManager) RemoveQueued(sessionID, inputID string) error {
	s := m.session(sessionID)
	s.mu.Lock()
	idx := -1
	var removed *managedTurnInput
	for i, item := range s.queued {
		if item.input.ID == inputID {
			idx = i
			removed = item
			break
		}
	}
	if idx < 0 {
		s.mu.Unlock()
		return ErrInputNotQueued
	}
	s.queued = append(s.queued[:idx], s.queued[idx+1:]...)
	delete(s.ids, inputID)
	s.mu.Unlock()
	m.emit(TurnEvent{SessionID: sessionID, InputID: inputID, State: TurnInputCancelled, Intent: removed.input.Intent})
	m.emitQueuePositions(sessionID, s)
	return nil
}

func (m *TurnManager) ReplaceQueued(sessionID, inputID string, replacement TurnInput) error {
	if len(replacement.Content) == 0 {
		return errors.New("input content required")
	}
	s := m.session(sessionID)
	s.mu.Lock()
	position := 0
	for i, item := range s.queued {
		if item.input.ID != inputID {
			continue
		}
		replacement.ID = inputID
		if replacement.Intent == "" {
			replacement.Intent = item.input.Intent
		}
		replacement.Content = agent.CloneContent(replacement.Content)
		item.input = replacement
		position = i + 1
		break
	}
	s.mu.Unlock()
	if position == 0 {
		return ErrInputNotQueued
	}
	m.emit(TurnEvent{SessionID: sessionID, InputID: inputID, State: TurnInputQueued, Intent: replacement.Intent, Position: position})
	return nil
}

// CancelCurrent interrupts the active turn and pauses queued follow-ups.
func (m *TurnManager) CancelCurrent(sessionID string) {
	m.cancelSession(sessionID, false)
}

// CancelAll interrupts the active turn and cancels every queued follow-up.
func (m *TurnManager) CancelAll(sessionID string) {
	m.cancelSession(sessionID, true)
}

func (m *TurnManager) cancelSession(sessionID string, clearQueue bool) {
	s := m.session(sessionID)
	s.mu.Lock()
	s.cancelRequested = s.active != nil
	if !clearQueue && len(s.queued) > 0 {
		s.paused = true
	}
	queued := []*managedTurnInput(nil)
	if clearQueue {
		queued = append(queued, s.queued...)
		for _, item := range queued {
			delete(s.ids, item.input.ID)
		}
		s.queued = nil
		s.paused = false
	}
	cancel := s.cancel
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	m.agent.Cancel(sessionID)
	for _, item := range queued {
		m.emit(TurnEvent{SessionID: sessionID, InputID: item.input.ID, State: TurnInputCancelled, Intent: item.input.Intent})
	}
}

func (m *TurnManager) ClearQueue(sessionID string) {
	s := m.session(sessionID)
	s.mu.Lock()
	queued := append([]*managedTurnInput(nil), s.queued...)
	for _, item := range queued {
		delete(s.ids, item.input.ID)
	}
	s.queued = nil
	s.paused = false
	s.mu.Unlock()
	for _, item := range queued {
		m.emit(TurnEvent{SessionID: sessionID, InputID: item.input.ID, State: TurnInputCancelled, Intent: item.input.Intent})
	}
}

func (m *TurnManager) Resume(sessionID string) bool {
	s := m.session(sessionID)
	s.mu.Lock()
	s.paused = false
	if s.active != nil || s.running || len(s.queued) == 0 {
		s.mu.Unlock()
		return false
	}
	next := s.queued[0]
	s.queued = s.queued[1:]
	s.active = next
	s.running = true
	s.cancelRequested = false
	s.mu.Unlock()
	m.emit(TurnEvent{SessionID: sessionID, InputID: next.input.ID, State: TurnInputActive, Intent: next.input.Intent})
	go m.runSession(sessionID, s)
	return true
}

func (m *TurnManager) Snapshot(sessionID string) TurnSnapshot {
	s := m.session(sessionID)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := TurnSnapshot{Paused: s.paused, Features: m.Features(sessionID)}
	if s.active != nil {
		out.Inputs = append(out.Inputs, TurnInputSnapshot{
			ID: s.active.input.ID, State: TurnInputActive, Intent: s.active.input.Intent,
		})
	}
	for _, item := range s.steered {
		out.Inputs = append(out.Inputs, TurnInputSnapshot{
			ID: item.input.ID, State: TurnInputSteered, Intent: item.input.Intent,
		})
	}
	for i, item := range s.queued {
		out.Inputs = append(out.Inputs, TurnInputSnapshot{
			ID: item.input.ID, State: TurnInputQueued, Intent: item.input.Intent, Position: i + 1,
		})
	}
	return out
}

func (m *TurnManager) emitQueuePositions(sessionID string, s *managedTurnSession) {
	s.mu.Lock()
	items := append([]*managedTurnInput(nil), s.queued...)
	s.mu.Unlock()
	for i, item := range items {
		m.emit(TurnEvent{SessionID: sessionID, InputID: item.input.ID, State: TurnInputQueued, Intent: item.input.Intent, Position: i + 1})
	}
}

func (m *TurnManager) releaseID(s *managedTurnSession, inputID string) {
	s.mu.Lock()
	delete(s.ids, inputID)
	s.mu.Unlock()
}

func (m *TurnManager) Close() {
	m.cancel()
	m.mu.Lock()
	sessions := make(map[string]*managedTurnSession, len(m.sessions))
	maps.Copy(sessions, m.sessions)
	m.mu.Unlock()
	for id, s := range sessions {
		s.mu.Lock()
		cancel := s.cancel
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		m.agent.Cancel(id)
	}
}
