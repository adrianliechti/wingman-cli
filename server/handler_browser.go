package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

type browserRequest struct {
	Provider string `json:"provider"`
	URL      string `json:"url,omitempty"`
}

func (s *Server) handleBrowserStatus(w http.ResponseWriter, _ *http.Request) {
	service := s.workspace.Browser
	if service == nil {
		writeJSON(w, map[string]any{"providers": []any{}, "selected": ""})
		return
	}
	writeJSON(w, map[string]any{
		"providers": service.Providers(),
		"selected":  service.Selected(),
	})
}

func (s *Server) handleBrowserConnect(w http.ResponseWriter, r *http.Request) {
	request, ok := decodeBrowserRequest(w, r)
	if !ok {
		return
	}
	service := s.workspace.Browser
	if service == nil {
		http.Error(w, "browser integrations are unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := service.Enable(r.Context(), request.Provider); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	s.broadcast(Frame{Type: EvtCapabilitiesChanged})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleBrowserSelect(w http.ResponseWriter, r *http.Request) {
	request, ok := decodeBrowserRequest(w, r)
	if !ok {
		return
	}
	service := s.workspace.Browser
	if service == nil {
		http.Error(w, "browser integrations are unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := service.Select(request.Provider); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleBrowserOpen(w http.ResponseWriter, r *http.Request) {
	request, ok := decodeBrowserRequest(w, r)
	if !ok {
		return
	}
	service := s.workspace.Browser
	if service == nil {
		http.Error(w, "browser integrations are unavailable", http.StatusServiceUnavailable)
		return
	}
	result, err := service.Open(r.Context(), request.Provider, request.URL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]string{"result": result})
}

func (s *Server) handleBrowserSnapshot(w http.ResponseWriter, r *http.Request) {
	service := s.workspace.Browser
	if service == nil {
		http.Error(w, "browser integrations are unavailable", http.StatusServiceUnavailable)
		return
	}
	result, err := service.Snapshot(r.Context(), r.URL.Query().Get("provider"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]string{"snapshot": result})
}

func (s *Server) handleBrowserPageInfo(w http.ResponseWriter, r *http.Request) {
	service := s.workspace.Browser
	if service == nil {
		http.Error(w, "browser integrations are unavailable", http.StatusServiceUnavailable)
		return
	}
	result, err := service.PageInfo(r.Context(), r.URL.Query().Get("provider"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]string{"info": result})
}

func (s *Server) handleBrowserScreenshot(w http.ResponseWriter, r *http.Request) {
	service := s.workspace.Browser
	if service == nil {
		http.Error(w, "browser integrations are unavailable", http.StatusServiceUnavailable)
		return
	}
	capture, err := service.Screenshot(r.Context(), r.URL.Query().Get("provider"), r.URL.Query().Get("uid"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", capture.MIMEType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(capture.Data)
}

func decodeBrowserRequest(w http.ResponseWriter, r *http.Request) (browserRequest, bool) {
	var request browserRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid browser request", http.StatusBadRequest)
		return browserRequest{}, false
	}
	request.Provider = strings.TrimSpace(request.Provider)
	return request, true
}
