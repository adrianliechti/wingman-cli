package tool

import (
	"errors"
	"fmt"
	"sync"
)

type Set struct {
	tools []Tool

	mu      sync.Mutex
	closed  bool
	closers []ownedCloser
}

type ownedCloser struct {
	name  string
	close func() error
}

func NewSet(tools ...Tool) *Set {
	return &Set{tools: append([]Tool(nil), tools...)}
}

func (s *Set) Slice() []Tool {
	if s == nil {
		return nil
	}
	return append([]Tool(nil), s.tools...)
}

func (s *Set) Own(name string, close func() error) {
	if s == nil || close == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = closeOwned(ownedCloser{name: name, close: close})
		return
	}
	s.closers = append(s.closers, ownedCloser{name: name, close: close})
	s.mu.Unlock()
}

func (s *Set) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	closers := append([]ownedCloser(nil), s.closers...)
	s.closers = nil
	s.mu.Unlock()

	var errs []error
	for i := len(closers) - 1; i >= 0; i-- {
		if err := closeOwned(closers[i]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func closeOwned(closer ownedCloser) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("close %s: panic: %v", closer.name, recovered)
		}
	}()
	if err := closer.close(); err != nil {
		return fmt.Errorf("close %s: %w", closer.name, err)
	}
	return nil
}
