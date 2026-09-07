// Package remote tunnels the web UI server through a self-hosted rendezvous
// relay so a phone or another machine can reach it without inbound ports.
package remote

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

const (
	connectPath = "/api/relay/connect"
	pairPath    = "/pair"
	unpairPath  = "/unpair"

	headerTunnel = "X-Wingman-Tunnel"
	headerKey    = "X-Wingman-Key"
	cookieName   = "wingman_remote"
)

// Credentials identify one workspace on a relay. The relay learns the key when
// the workspace registers; a phone presents the same key once when pairing.
type Credentials struct {
	ID  string
	Key string
}

func NewCredentials() Credentials {
	return Credentials{ID: randomToken(16), Key: randomToken(32)}
}

func randomToken(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf))
}

// PairURL keeps the secret in the fragment, outside HTTP paths and referrers.
// The link remains valid for this workspace server's lifetime.
func PairURL(relay string, c Credentials) (string, error) {
	u, err := parseRelayURL(relay)
	if err != nil {
		return "", err
	}
	if !c.valid() {
		return "", errors.New("invalid remote credentials")
	}
	switch u.Scheme {
	case "wss":
		u.Scheme = "https"
	case "ws":
		u.Scheme = "http"
	}
	u.Path = pairPath
	u.Fragment = c.ID + "." + c.Key
	return u.String(), nil
}

func parseRelayURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("relay URL is empty")
	}
	if !strings.Contains(raw, "://") {
		raw = "wss://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid relay URL: %w", err)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	case "wss", "ws":
	default:
		return nil, fmt.Errorf("unsupported relay scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("relay URL has no host")
	}
	if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errors.New("relay URL must be an origin without credentials, path, query, or fragment")
	}
	if u.Scheme == "ws" && !isLoopback(u.Hostname()) {
		return nil, errors.New("relay URL must use wss:// (plain ws:// is only allowed for localhost)")
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	u.RawQuery = ""
	u.Fragment = ""
	return u, nil
}

func (c Credentials) valid() bool {
	return validToken(c.ID) && validToken(c.Key) && len(c.Key) >= 32
}

func validToken(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
