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
	mu          sync.Mutex
	queueLoaded bool
	queueErr    error
	done        chan struct{}

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
	workers  sync.WaitGroup
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
	s := m.sessions[id]
	if s == nil {
		s = &managedTurnSession{}
		m.sessions[id] = s
	}
	m.mu.Unlock()
	// Loading one session must never hold the workspace-wide registry lock.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.queueLoaded {
		return s
	}
	store, ok := m.agent.(TurnQueueStore)
	if !ok {
		s.queueLoaded = true
		return s
	}
	state, err := store.LoadTurnQueue(id)
	if err != nil {
		s.queueErr = fmt.Errorf("restore turn queue: %w", err)
		return s
	}
	s.queueLoaded, s.queueErr = true, nil
	s.ids = make(map[string]struct{}, len(state.Inputs))
	for _, input := range state.Inputs {
		if input.ID == "" {
			continue
		}
		if _, duplicate := s.ids[input.ID]; duplicate {
			continue
		}
		input = CloneTurnInput(input)
		if input.Intent == "" {
			input.Intent = TurnInputFollowUp
		}
		s.ids[input.ID] = struct{}{}
		s.queued = append(s.queued, &managedTurnInput{input: input})
	}
	// Reading a recovered queue never authorizes execution.
	s.paused = len(s.queued) > 0
	return s
}

// Admission and worker registration share the shutdown barrier. Close waits for
// accepted submissions, including their callbacks and native steering calls.
func (m *TurnManager) beginWork() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx.Err() != nil {
		return false
	}
	m.workers.Add(1)
	return true
}

func (m *TurnManager) persistLocked(sessionID string, s *managedTurnSession) error {
	store, ok := m.agent.(TurnQueueStore)
	if !ok {
		return nil
	}
	state := TurnQueueState{Paused: s.paused, Inputs: make([]TurnInput, 0, len(s.queued))}
	for _, item := range s.queued {
		input := item.input
		input = CloneTurnInput(input)
		state.Inputs = append(state.Inputs, input)
	}
	if err := store.SaveTurnQueue(sessionID, state); err != nil {
		s.queueErr = fmt.Errorf("persist turn queue: %w", err)
		return s.queueErr
	}
	s.queueErr = nil
	return nil
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

// Submit rejects duplicate live input IDs. Transport receipts own retry
// deduplication. A steer that loses a turn-boundary race becomes a FIFO follow-up.
func (m *TurnManager) Submit(ctx context.Context, sessionID string, input TurnInput) (TurnInputSnapshot, error) {
	if !m.beginWork() {
		return TurnInputSnapshot{}, m.ctx.Err()
	}
	defer m.workers.Done()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(m.ctx, cancel)
	defer func() { stop(); cancel() }()
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
	input = CloneTurnInput(input)

	s := m.session(sessionID)
	s.mu.Lock()
	if !s.queueLoaded || m.ctx.Err() != nil {
		err := s.queueErr
		if m.ctx.Err() != nil {
			err = m.ctx.Err()
		}
		s.mu.Unlock()
		return TurnInputSnapshot{}, err
	}
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
						m.emit(TurnEvent{SessionID: sessionID, Input: CloneTurnInput(input), InputID: input.ID, State: TurnInputSteered, Intent: input.Intent})
						return snap, nil
					}
					s.mu.Unlock()
					// The active turn completed after the backend accepted the steer.
					// Surface the accepted user input before completing it; re-queueing
					// here would duplicate an input the backend already owns.
					m.emit(TurnEvent{SessionID: sessionID, Input: CloneTurnInput(input), InputID: input.ID, State: TurnInputSteered, Intent: input.Intent})
					m.releaseID(s, input.ID)
					m.emit(TurnEvent{SessionID: sessionID, Input: CloneTurnInput(input), InputID: input.ID, State: TurnInputCompleted, Intent: input.Intent})
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
	if err := m.ctx.Err(); err != nil {
		delete(s.ids, input.ID)
		s.mu.Unlock()
		return TurnInputSnapshot{}, err
	}
	if s.active == nil && !s.running && !s.paused {
		s.active = item
		s.running = true
		s.done = make(chan struct{})
		s.cancelRequested = false
		s.mu.Unlock()
		m.emit(TurnEvent{SessionID: sessionID, Input: CloneTurnInput(input), InputID: input.ID, State: TurnInputActive, Intent: input.Intent})
		m.startSession(sessionID, s)
		return TurnInputSnapshot{ID: input.ID, State: TurnInputActive, Intent: input.Intent}, nil
	}
	s.queued = append(s.queued, item)
	position := len(s.queued)
	if err := m.persistLocked(sessionID, s); err != nil {
		s.queued = s.queued[:len(s.queued)-1]
		delete(s.ids, input.ID)
		s.mu.Unlock()
		return TurnInputSnapshot{}, err
	}
	s.mu.Unlock()
	m.emit(TurnEvent{SessionID: sessionID, Input: CloneTurnInput(input), InputID: input.ID, State: TurnInputQueued, Intent: input.Intent, Position: position})
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

func (m *TurnManager) startSession(sessionID string, s *managedTurnSession) {
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	// Submit/Resume hold a work reservation until after this registration. Even
	// a cancelled admission runs its terminal cleanup before Close returns.
	m.workers.Go(func() { defer close(done); m.runSession(sessionID, s) })
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
		cancelBeforeStart := s.cancelRequested || m.ctx.Err() != nil
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
		promote := func() error {
			if m.ctx.Err() == nil && !s.paused && len(s.queued) > 0 {
				next = s.queued[0]
				s.queued = s.queued[1:]
				s.active = next
			}
			if err := m.persistLocked(sessionID, s); err != nil {
				if next != nil {
					s.active = nil
					s.queued = append([]*managedTurnInput{next}, s.queued...)
					next = nil
				}
				s.paused = len(s.queued) > 0
				s.queueErr = err
				return err
			}
			return nil
		}
		persistErr := promote()
		// Keep running true until terminal callbacks have projected the finished
		// turn. New submits during finalization remain queued behind that boundary.
		s.mu.Unlock()
		m.emit(TurnEvent{SessionID: sessionID, Input: CloneTurnInput(item.input), InputID: item.input.ID, State: state, Intent: item.input.Intent, Err: runErr, Executed: true})
		for _, in := range steered {
			m.emit(TurnEvent{SessionID: sessionID, Input: CloneTurnInput(in.input), InputID: in.input.ID, State: state, Intent: in.input.Intent, Err: runErr})
		}
		s.mu.Lock()
		if next == nil && persistErr == nil {
			persistErr = promote()
		}
		s.running = next != nil
		s.mu.Unlock()
		if persistErr != nil {
			m.emit(TurnEvent{SessionID: sessionID, State: TurnInputFailed, Err: persistErr})
		}
		if next == nil {
			return
		}
		m.emit(TurnEvent{SessionID: sessionID, Input: CloneTurnInput(next.input), InputID: next.input.ID, State: TurnInputActive, Intent: next.input.Intent})
	}
}

func (m *TurnManager) executeInput(ctx context.Context, sessionID string, item *managedTurnInput) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("agent turn panicked: %v", recovered)
		}
	}()
	ctx = agent.WithInputID(ctx, item.input.ID)
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
	if !s.queueLoaded {
		err := s.queueErr
		s.mu.Unlock()
		return err
	}
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
	if err := m.persistLocked(sessionID, s); err != nil {
		s.queued = append(s.queued, nil)
		copy(s.queued[idx+1:], s.queued[idx:])
		s.queued[idx] = removed
		s.ids[inputID] = struct{}{}
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	m.emit(TurnEvent{SessionID: sessionID, Input: CloneTurnInput(removed.input), InputID: inputID, State: TurnInputCancelled, Intent: removed.input.Intent})
	m.emitQueuePositions(sessionID, s)
	return nil
}

func (m *TurnManager) ReplaceQueued(sessionID, inputID string, replacement TurnInput) error {
	if len(replacement.Content) == 0 {
		return errors.New("input content required")
	}
	s := m.session(sessionID)
	s.mu.Lock()
	if !s.queueLoaded {
		err := s.queueErr
		s.mu.Unlock()
		return err
	}
	position := 0
	var previous TurnInput
	for i, item := range s.queued {
		if item.input.ID != inputID {
			continue
		}
		replacement.ID = inputID
		if replacement.Intent == "" {
			replacement.Intent = item.input.Intent
		}
		replacement = CloneTurnInput(replacement)
		previous = item.input
		item.input = replacement
		position = i + 1
		break
	}
	if position == 0 {
		s.mu.Unlock()
		return ErrInputNotQueued
	}
	if err := m.persistLocked(sessionID, s); err != nil {
		s.queued[position-1].input = previous
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	m.emit(TurnEvent{SessionID: sessionID, Input: CloneTurnInput(replacement), InputID: inputID, State: TurnInputQueued, Intent: replacement.Intent, Position: position})
	return nil
}

// CancelCurrent interrupts the active turn and pauses queued follow-ups.
func (m *TurnManager) CancelCurrent(sessionID string) error {
	return m.cancelSession(sessionID, false)
}

// CancelAll interrupts the active turn and cancels every queued follow-up.
func (m *TurnManager) CancelAll(sessionID string) error {
	return m.cancelSession(sessionID, true)
}

func (m *TurnManager) cancelSession(sessionID string, clearQueue bool) error {
	s := m.session(sessionID)
	s.mu.Lock()
	if !s.queueLoaded {
		err := s.queueErr
		s.mu.Unlock()
		return err
	}
	previousQueue, previousPaused, previousCancelled := s.queued, s.paused, s.cancelRequested
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
	if err := m.persistLocked(sessionID, s); err != nil {
		s.queued = previousQueue
		s.paused = previousPaused
		s.cancelRequested = previousCancelled
		for _, item := range previousQueue {
			s.ids[item.input.ID] = struct{}{}
		}
		s.mu.Unlock()
		return err
	}
	cancel := s.cancel
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	m.agent.Cancel(sessionID)
	for _, item := range queued {
		m.emit(TurnEvent{SessionID: sessionID, Input: CloneTurnInput(item.input), InputID: item.input.ID, State: TurnInputCancelled, Intent: item.input.Intent})
	}
	return nil
}

func (m *TurnManager) ClearQueue(sessionID string) error {
	s := m.session(sessionID)
	s.mu.Lock()
	if !s.queueLoaded {
		err := s.queueErr
		s.mu.Unlock()
		return err
	}
	previousPaused := s.paused
	queued := append([]*managedTurnInput(nil), s.queued...)
	for _, item := range queued {
		delete(s.ids, item.input.ID)
	}
	s.queued = nil
	s.paused = false
	if err := m.persistLocked(sessionID, s); err != nil {
		s.queued = queued
		s.paused = previousPaused
		for _, item := range queued {
			s.ids[item.input.ID] = struct{}{}
		}
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	for _, item := range queued {
		m.emit(TurnEvent{SessionID: sessionID, Input: CloneTurnInput(item.input), InputID: item.input.ID, State: TurnInputCancelled, Intent: item.input.Intent})
	}
	return nil
}

// StopSession cancels execution and joins finalization before callers delete
// backend history. A late queue write must not recreate a deleted directory.
func (m *TurnManager) StopSession(ctx context.Context, sessionID string) error {
	if err := m.CancelAll(sessionID); err != nil {
		return err
	}
	s := m.session(sessionID)
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *TurnManager) Resume(sessionID string) bool {
	if !m.beginWork() {
		return false
	}
	defer m.workers.Done()
	s := m.session(sessionID)
	s.mu.Lock()
	if !s.queueLoaded || m.ctx.Err() != nil {
		s.mu.Unlock()
		return false
	}
	previousPaused := s.paused
	s.paused = false
	if s.active != nil || s.running || len(s.queued) == 0 {
		if err := m.persistLocked(sessionID, s); err != nil {
			s.paused = previousPaused
		}
		s.mu.Unlock()
		return false
	}
	next := s.queued[0]
	s.queued = s.queued[1:]
	s.active = next
	s.running = true
	s.cancelRequested = false
	if err := m.persistLocked(sessionID, s); err != nil {
		s.active = nil
		s.queued = append([]*managedTurnInput{next}, s.queued...)
		s.running = false
		s.paused = true
		s.mu.Unlock()
		return false
	}
	s.done = make(chan struct{})
	s.mu.Unlock()
	m.emit(TurnEvent{SessionID: sessionID, Input: CloneTurnInput(next.input), InputID: next.input.ID, State: TurnInputActive, Intent: next.input.Intent})
	m.startSession(sessionID, s)
	return true
}

func (m *TurnManager) Snapshot(sessionID string) TurnSnapshot {
	s := m.session(sessionID)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := TurnSnapshot{Paused: s.paused, Features: m.Features(sessionID), Error: s.queueErr}
	if s.active != nil {
		out.Inputs = append(out.Inputs, TurnInputSnapshot{
			Input: CloneTurnInput(s.active.input), ID: s.active.input.ID, State: TurnInputActive, Intent: s.active.input.Intent,
		})
	}
	for _, item := range s.steered {
		out.Inputs = append(out.Inputs, TurnInputSnapshot{
			Input: CloneTurnInput(item.input), ID: item.input.ID, State: TurnInputSteered, Intent: item.input.Intent,
		})
	}
	for i, item := range s.queued {
		out.Inputs = append(out.Inputs, TurnInputSnapshot{
			Input: CloneTurnInput(item.input), ID: item.input.ID, State: TurnInputQueued, Intent: item.input.Intent, Position: i + 1,
		})
	}
	return out
}

func (m *TurnManager) emitQueuePositions(sessionID string, s *managedTurnSession) {
	s.mu.Lock()
	items := make([]*managedTurnInput, 0, len(s.queued))
	for _, item := range s.queued {
		items = append(items, &managedTurnInput{input: CloneTurnInput(item.input)})
	}
	s.mu.Unlock()
	for i, item := range items {
		m.emit(TurnEvent{SessionID: sessionID, Input: CloneTurnInput(item.input), InputID: item.input.ID, State: TurnInputQueued, Intent: item.input.Intent, Position: i + 1})
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
	m.workers.Wait()
}
