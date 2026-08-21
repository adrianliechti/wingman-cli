package language

import (
	"context"
	"fmt"
	"sync"

	"github.com/adrianliechti/wingman-agent/pkg/graph"
	"github.com/adrianliechti/wingman-agent/pkg/lsp"
)

// Service owns the code-intelligence backends for one workspace. It is the
// only layer that chooses between language-server and structural results.
type Service struct {
	root    string
	manager *lsp.Manager
	graph   *graph.Engine

	lifeMu sync.RWMutex
	closed bool
}

func New(root, graphStorePath string, managerOptions ...lsp.ManagerOption) *Service {
	service := &Service{
		root:    root,
		manager: lsp.NewManager(root, managerOptions...),
	}
	service.graph = graph.New(root, graphStorePath, graph.WithResolver(&lspResolver{service: service}))
	return service
}

func (s *Service) Manager() *lsp.Manager { return s.manager }

func (s *Service) Graph() *graph.Engine { return s.graph }

func (s *Service) WarmUp() {
	s.lifeMu.RLock()
	defer s.lifeMu.RUnlock()
	if !s.closed {
		s.manager.WarmUpServers()
	}
}

func (s *Service) Close() {
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.manager.Close()
}

func (s *Service) HasLSP() bool {
	s.lifeMu.RLock()
	defer s.lifeMu.RUnlock()
	return !s.closed && len(s.manager.DetectServers()) > 0
}

func (s *Service) hasLSPServerFor(filePath string) bool {
	return s.manager.FindServer(filePath) != nil
}

func (s *Service) withLSPDocument(ctx context.Context, filePath string, content *string, fn func(*lsp.Session, string) error) error {
	s.lifeMu.RLock()
	defer s.lifeMu.RUnlock()
	if s.closed {
		return fmt.Errorf("code intelligence service is closed")
	}
	session, err := s.manager.GetSession(ctx, filePath)
	if err != nil {
		return err
	}
	uri, err := syncDocument(ctx, session, filePath, content)
	if err != nil {
		return err
	}
	return fn(session, uri)
}

func syncDocument(ctx context.Context, session *lsp.Session, filePath string, content *string) (string, error) {
	if content != nil {
		return session.SyncDocument(ctx, filePath, *content)
	}
	return session.OpenDocument(ctx, filePath)
}

func (s *Service) SyncDocument(ctx context.Context, filePath, content string, saved bool) error {
	s.lifeMu.RLock()
	defer s.lifeMu.RUnlock()
	if s.closed || !s.hasLSPServerFor(filePath) {
		return nil
	}
	session, err := s.manager.GetSession(ctx, filePath)
	if err != nil {
		return err
	}
	if saved {
		_, err = session.SaveDocument(ctx, filePath, content)
	} else {
		_, err = session.SyncDocument(ctx, filePath, content)
	}
	return err
}

func (s *Service) CloseDocument(ctx context.Context, filePath string) error {
	s.lifeMu.RLock()
	defer s.lifeMu.RUnlock()
	if s.closed {
		return nil
	}
	session, ok := s.manager.ActiveSession(filePath)
	if !ok {
		return nil
	}
	return session.CloseDocument(ctx, filePath)
}

func (s *Service) Capabilities(ctx context.Context, filePath string) (lsp.ServerCapabilities, bool, error) {
	s.lifeMu.RLock()
	defer s.lifeMu.RUnlock()
	if s.closed || !s.hasLSPServerFor(filePath) {
		return lsp.ServerCapabilities{}, false, nil
	}
	session, err := s.manager.GetSession(ctx, filePath)
	if err != nil {
		return lsp.ServerCapabilities{}, false, err
	}
	return session.Capabilities(), true, nil
}

func (s *Service) DocumentContent(filePath string) (string, bool) {
	s.lifeMu.RLock()
	defer s.lifeMu.RUnlock()
	if s.closed {
		return "", false
	}
	session, ok := s.manager.ActiveSession(filePath)
	if !ok {
		return "", false
	}
	return session.DocumentContent(filePath)
}
