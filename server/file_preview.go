package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const previewCookieName = "wingman_preview_root"

type filePreviewServer struct {
	root     *os.Root
	secret   []byte
	listener net.Listener
	server   *http.Server
	done     chan struct{}
	close    sync.Once
}

func newFilePreviewServer(root *os.Root) (*filePreviewServer, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		listener.Close()
		return nil, err
	}

	p := &filePreviewServer{
		root:     root,
		secret:   secret,
		listener: listener,
		done:     make(chan struct{}),
	}
	p.server = &http.Server{Handler: p}
	go func() {
		_ = p.server.Serve(listener)
		close(p.done)
	}()
	return p, nil
}

func (p *filePreviewServer) URL() string {
	return "http://" + p.listener.Addr().String()
}

func (p *filePreviewServer) startURL(file, requestHost string) string {
	query := url.Values{
		"token": {hex.EncodeToString(p.secret)},
		"path":  {filepath.ToSlash(file)},
	}
	host := requestHost
	if parsed, _, err := net.SplitHostPort(requestHost); err == nil {
		host = parsed
	}
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
	default:
		host = "127.0.0.1"
	}
	port := strconv.Itoa(p.listener.Addr().(*net.TCPAddr).Port)
	return "http://" + net.JoinHostPort(host, port) + "/__start?" + query.Encode()
}

func (p *filePreviewServer) Close() {
	p.close.Do(func() {
		_ = p.server.Close()
		<-p.done
	})
}

func (p *filePreviewServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/__start" {
		p.handleStart(w, r)
		return
	}

	rootDir, ok := p.previewRoot(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	requestPath := strings.ReplaceAll(r.URL.Path, `\`, "/")
	relURL := strings.TrimPrefix(path.Clean("/"+requestPath), "/")
	if relURL == "" || relURL == "." {
		relURL = "index.html"
	}
	rel := filepath.Join(rootDir, filepath.FromSlash(relURL))
	info, err := p.root.Lstat(rel)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	if info.IsDir() {
		if !strings.HasSuffix(r.URL.Path, "/") {
			http.Redirect(w, r, r.URL.Path+"/", http.StatusPermanentRedirect)
			return
		}
		rel = filepath.Join(rel, "index.html")
		info, err = p.root.Lstat(rel)
		if err != nil {
			http.Error(w, "index file not found", http.StatusNotFound)
			return
		}
	}
	if !info.Mode().IsRegular() {
		http.Error(w, "not a regular file", http.StatusBadRequest)
		return
	}

	f, err := p.root.Open(rel)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	defer f.Close()

	p.setPreviewHeaders(w)
	name := filepath.Base(rel)
	if contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	http.ServeContent(w, r, name, info.ModTime(), f)
}

func (p *filePreviewServer) handleStart(w http.ResponseWriter, r *http.Request) {
	token, err := hex.DecodeString(r.URL.Query().Get("token"))
	if err != nil || subtle.ConstantTimeCompare(token, p.secret) != 1 {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	file, ok := previewWorkspaceRel(r.URL.Query().Get("path"))
	if !ok {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	info, err := p.root.Lstat(file)
	if err != nil || !info.Mode().IsRegular() {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	rootDir := filepath.Dir(file)
	http.SetCookie(w, &http.Cookie{
		Name:     previewCookieName,
		Value:    p.signRoot(rootDir),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, "/"+url.PathEscape(filepath.Base(file)), http.StatusSeeOther)
}

func (p *filePreviewServer) previewRoot(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(previewCookieName)
	if err != nil {
		return "", false
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 {
		return "", false
	}
	encoded := parts[0]
	want, err := hex.DecodeString(parts[1])
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, p.secret)
	_, _ = mac.Write([]byte(encoded))
	if !hmac.Equal(want, mac.Sum(nil)) {
		return "", false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", false
	}
	rootDir, ok := previewWorkspaceDirRel(string(decoded))
	return rootDir, ok
}

func (p *filePreviewServer) signRoot(rootDir string) string {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(filepath.ToSlash(rootDir)))
	mac := hmac.New(sha256.New, p.secret)
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + hex.EncodeToString(mac.Sum(nil))
}

func (p *filePreviewServer) setPreviewHeaders(w http.ResponseWriter) {
	// The preview has its own loopback origin, so scripts and local web APIs work
	// normally without granting access to Wingman's separate API origin.
	w.Header().Set("Content-Security-Policy", "default-src 'self' data: blob: http: https:; script-src 'self' 'unsafe-inline' 'unsafe-eval' data: blob: http: https:; style-src 'self' 'unsafe-inline' data: blob: http: https:; connect-src 'self' http: https: ws: wss:; worker-src 'none'; object-src 'none'; form-action 'none'; base-uri 'self'")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func previewWorkspaceRel(value string) (string, bool) {
	if value == "" {
		return "", false
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || path.IsAbs(cleaned) {
		return "", false
	}
	return filepath.FromSlash(cleaned), true
}

func previewWorkspaceDirRel(value string) (string, bool) {
	if value == "." {
		return ".", true
	}
	return previewWorkspaceRel(value)
}
