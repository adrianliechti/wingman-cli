package server

import (
	"cmp"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/lsp"
)

type lspDocumentRequest struct {
	Path    string  `json:"path"`
	Content *string `json:"content,omitempty"`
}

type diagnosticItem struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	EndLine   int    `json:"end_line"`
	EndColumn int    `json:"end_column"`
	Severity  string `json:"severity"`
	Message   string `json:"message"`
	Source    string `json:"source,omitempty"`
}

type lspLocationItem struct {
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	External bool   `json:"external,omitempty"`
}

type workspaceDiagnosticsResponse struct {
	Diagnostics        []diagnosticItem `json:"diagnostics"`
	CheckedFiles       int              `json:"checked_files"`
	DiscoveredFiles    int              `json:"discovered_files"`
	DiscoveryTruncated bool             `json:"discovery_truncated"`
	UnknownFiles       int              `json:"unknown_files"`
	UnavailableServers []string         `json:"unavailable_servers"`
	Analyzing          bool             `json:"analyzing"`
}

func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	report := s.workspace.Diagnostics(r.Context())
	unavailableServers := report.UnavailableServers
	if unavailableServers == nil {
		unavailableServers = []string{}
	}
	result := make([]diagnosticItem, 0)
	for filePath, diagnostics := range report.Diagnostics {
		result = append(result, s.diagnosticItems(filePath, diagnostics)...)
	}

	severityOrder := map[string]int{"error": 0, "warning": 1, "info": 2}
	slices.SortFunc(result, func(a, b diagnosticItem) int {
		if order := cmp.Compare(severityOrder[a.Severity], severityOrder[b.Severity]); order != 0 {
			return order
		}
		if pathOrder := cmp.Compare(a.Path, b.Path); pathOrder != 0 {
			return pathOrder
		}
		return cmp.Compare(a.Line, b.Line)
	})
	writeJSON(w, workspaceDiagnosticsResponse{
		Diagnostics:        result,
		CheckedFiles:       report.CheckedFiles,
		DiscoveredFiles:    report.DiscoveredFiles,
		DiscoveryTruncated: report.DiscoveryTruncated,
		UnknownFiles:       report.UnknownFiles,
		UnavailableServers: unavailableServers,
		Analyzing:          report.Analyzing,
	})
}

// resolveLSPFile validates a workspace-relative request path, writing the
// error response on failure. requireLSP additionally demands an available
// language server; endpoints with a graph fallback pass false.
func (s *Server) resolveLSPFile(w http.ResponseWriter, p string, requireLSP bool) (string, bool) {
	rel, ok := s.resolveExistingRegularFile(w, p)
	if !ok {
		return "", false
	}
	if requireLSP && !s.workspace.HasLSP() {
		http.Error(w, "language server unavailable", http.StatusNotFound)
		return "", false
	}
	return filepath.Join(s.workspace.RootPath, rel), true
}

func (s *Server) decodeLSPDocumentRequest(w http.ResponseWriter, r *http.Request, requireLSP bool) (lspDocumentRequest, string, bool) {
	var body lspDocumentRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return body, "", false
	}
	filePath, ok := s.resolveLSPFile(w, body.Path, requireLSP)
	return body, filePath, ok
}

func (s *Server) handleLSPFileDiagnostics(w http.ResponseWriter, r *http.Request) {
	body, filePath, ok := s.decodeLSPDocumentRequest(w, r, true)
	if !ok {
		return
	}

	diagnostics, known, err := s.workspace.FileDiagnostics(r.Context(), filePath, body.Content)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if !known {
		http.Error(w, "diagnostics are not available yet", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, s.diagnosticItems(filePath, diagnostics))
}

func (s *Server) handleLSPDefinition(w http.ResponseWriter, r *http.Request) {
	s.handleLSPLocations(w, r, s.workspace.DefinitionLocations, false)
}

func (s *Server) handleLSPTypeDefinition(w http.ResponseWriter, r *http.Request) {
	s.handleLSPLocations(w, r, s.workspace.TypeDefinitionLocations, true)
}

func (s *Server) handleLSPImplementations(w http.ResponseWriter, r *http.Request) {
	s.handleLSPLocations(w, r, s.workspace.ImplementationLocations, false)
}

func (s *Server) handleLSPReferences(w http.ResponseWriter, r *http.Request) {
	s.handleLSPLocations(w, r, s.workspace.ReferenceLocations, false)
}

func (s *Server) handleLSPHover(w http.ResponseWriter, r *http.Request) {
	body, filePath, ok := s.decodeLSPPositionRequest(w, r, false)
	if !ok {
		return
	}
	contents, err := s.workspace.HoverInformation(r.Context(), filePath, body.Content, body.Line-1, body.Column-1)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]string{"contents": contents})
}

func (s *Server) handleLSPDocumentSymbols(w http.ResponseWriter, r *http.Request) {
	body, filePath, ok := s.decodeLSPDocumentRequest(w, r, false)
	if !ok {
		return
	}

	documents, flat, err := s.workspace.DocumentSymbolItems(r.Context(), filePath, body.Content)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if len(documents) > 0 {
		writeJSON(w, documents)
		return
	}
	converted := make([]lsp.DocumentSymbol, 0, len(flat))
	for _, symbol := range flat {
		converted = append(converted, lsp.DocumentSymbol{
			Name:           symbol.Name,
			Kind:           symbol.Kind,
			Range:          symbol.Location.Range,
			SelectionRange: symbol.Location.Range,
		})
	}
	writeJSON(w, converted)
}

type lspPositionRequest struct {
	lspDocumentRequest
	Line   int `json:"line"`
	Column int `json:"column"`
}

func (s *Server) decodeLSPPositionRequest(w http.ResponseWriter, r *http.Request, requireLSP bool) (lspPositionRequest, string, bool) {
	var body lspPositionRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return body, "", false
	}
	if body.Line < 1 || body.Column < 1 {
		http.Error(w, "line and column must be positive", http.StatusBadRequest)
		return body, "", false
	}
	filePath, ok := s.resolveLSPFile(w, body.Path, requireLSP)
	return body, filePath, ok
}

type locationRequest func(context.Context, string, *string, int, int) ([]lsp.DefLocation, error)

func (s *Server) handleLSPLocations(w http.ResponseWriter, r *http.Request, request locationRequest, requireLSP bool) {
	body, filePath, ok := s.decodeLSPPositionRequest(w, r, requireLSP)
	if !ok {
		return
	}
	locations, err := request(r.Context(), filePath, body.Content, body.Line-1, body.Column-1)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	result := make([]lspLocationItem, 0, len(locations))
	seen := make(map[lspLocationItem]bool)
	for _, location := range locations {
		if location.Path == "" || location.Line < 0 || location.Column < 0 {
			continue
		}
		item := lspLocationItem{
			Line:   location.Line + 1,
			Column: location.Column + 1,
		}
		if targetRel, err := filepath.Rel(s.workspace.RootPath, location.Path); err == nil && targetRel != ".." && !filepath.IsAbs(targetRel) &&
			!strings.HasPrefix(targetRel, ".."+string(filepath.Separator)) {
			item.Path = filepath.ToSlash(targetRel)
		} else {
			abs := filepath.Clean(location.Path)
			if info, err := os.Stat(abs); err != nil || info.IsDir() {
				continue
			}
			item.Path = filepath.ToSlash(abs)
			item.External = true
			s.allowLSPExternalFile(item.Path)
		}
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	writeJSON(w, result)
}

// allowLSPExternalFile records a language-server reported location outside the
// workspace so handleLSPExternalFile may serve exactly those files, read-only.
func (s *Server) allowLSPExternalFile(path string) {
	s.lspExternalMu.Lock()
	defer s.lspExternalMu.Unlock()
	if s.lspExternalPaths == nil {
		s.lspExternalPaths = make(map[string]bool)
	}
	s.lspExternalPaths[path] = true
}

func (s *Server) lspExternalFileAllowed(path string) bool {
	s.lspExternalMu.Lock()
	defer s.lspExternalMu.Unlock()
	return s.lspExternalPaths[path]
}

func (s *Server) handleLSPExternalFile(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if !s.lspExternalFileAllowed(p) {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	data, err := os.ReadFile(filepath.FromSlash(p))
	if err != nil || isBinary(data) {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	writeJSON(w, FileContent{
		Path:     p,
		Content:  string(data),
		Language: languageForPath(p),
		Size:     int64(len(data)),
	})
}

func (s *Server) diagnosticItems(filePath string, diagnostics []lsp.Diagnostic) []diagnosticItem {
	relPath := filePath
	if rel, err := filepath.Rel(s.workspace.RootPath, filePath); err == nil {
		relPath = filepath.ToSlash(rel)
	}
	result := make([]diagnosticItem, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		severity := "info"
		switch diagnostic.Severity {
		case lsp.DiagnosticSeverityError:
			severity = "error"
		case lsp.DiagnosticSeverityWarning:
			severity = "warning"
		}
		result = append(result, diagnosticItem{
			Path:      relPath,
			Line:      diagnostic.Range.Start.Line + 1,
			Column:    diagnostic.Range.Start.Character + 1,
			EndLine:   diagnostic.Range.End.Line + 1,
			EndColumn: diagnostic.Range.End.Character + 1,
			Severity:  severity,
			Message:   diagnostic.Message,
			Source:    diagnostic.Source,
		})
	}
	return result
}
