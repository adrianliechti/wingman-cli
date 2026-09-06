package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/oauth2"

	"github.com/adrianliechti/wingman-agent/pkg/layout"
)

// Credential is the OAuth state kept for one remote server: the client the
// authorization server knows us as, and the token it issued.
type Credential struct {
	ClientID     string           `json:"client_id,omitempty"`
	ClientSecret string           `json:"client_secret,omitempty"`
	TokenURL     string           `json:"token_url,omitempty"`
	AuthStyle    oauth2.AuthStyle `json:"auth_style,omitempty"`
	RedirectURL  string           `json:"redirect_url,omitempty"`
	Token        *oauth2.Token    `json:"token,omitempty"`
}

func (c *Credential) config() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		RedirectURL:  c.RedirectURL,
		Endpoint: oauth2.Endpoint{
			TokenURL:  c.TokenURL,
			AuthStyle: c.AuthStyle,
		},
	}
}

// CredentialStore persists credentials keyed by server URL in a JSON file.
type CredentialStore struct {
	path string
	mu   sync.Mutex
}

func NewCredentialStore(path string) *CredentialStore {
	return &CredentialStore{path: path}
}

// DefaultCredentialStore uses the Wingman user directory.
func DefaultCredentialStore() *CredentialStore {
	path, _ := layout.WingmanPath("mcp-credentials.json")
	return NewCredentialStore(path)
}

func (s *CredentialStore) Path() string {
	return s.path
}

// Get returns the stored credential for url, or nil when there is none.
func (s *CredentialStore) Get(url string) (*Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.read()

	if err != nil {
		return nil, err
	}

	return entries[url], nil
}

func (s *CredentialStore) Set(url string, cred *Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.read()

	if err != nil {
		return err
	}

	entries[url] = cred

	return s.write(entries)
}

// Delete forgets the credential for url and reports whether one existed.
func (s *CredentialStore) Delete(url string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.read()

	if err != nil {
		return false, err
	}

	if _, ok := entries[url]; !ok {
		return false, nil
	}

	delete(entries, url)

	return true, s.write(entries)
}

func (s *CredentialStore) read() (map[string]*Credential, error) {
	entries := map[string]*Credential{}

	if s.path == "" {
		return entries, nil
	}

	data, err := os.ReadFile(s.path)

	if errors.Is(err, os.ErrNotExist) {
		return entries, nil
	}

	if err != nil {
		return nil, fmt.Errorf("read mcp credentials: %w", err)
	}

	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse mcp credentials: %w", err)
	}

	return entries, nil
}

func (s *CredentialStore) write(entries map[string]*Credential) error {
	if s.path == "" {
		return errors.New("mcp credential store has no path")
	}

	data, err := json.MarshalIndent(entries, "", "  ")

	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	tmp := s.path + ".tmp"

	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}

	return os.Rename(tmp, s.path)
}
