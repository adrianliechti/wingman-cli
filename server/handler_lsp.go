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

	"github.com/adrianliechti/wingman-agent/pkg/fileuri"
	"github.com/adrianliechti/wingman-agent/pkg/language"
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

type editorCapabilitiesResponse struct {
	WorkspaceURI                      string   `json:"workspace_uri"`
	LanguageServer                    bool     `json:"language_server"`
	Completion                        bool     `json:"completion"`
	CompletionResolve                 bool     `json:"completion_resolve"`
	CompletionTriggerCharacters       []string `json:"completion_trigger_characters"`
	SignatureHelp                     bool     `json:"signature_help"`
	SignatureHelpTriggerCharacters    []string `json:"signature_help_trigger_characters"`
	SignatureHelpRetriggerCharacters  []string `json:"signature_help_retrigger_characters"`
	Hover                             bool     `json:"hover"`
	Definition                        bool     `json:"definition"`
	TypeDefinition                    bool     `json:"type_definition"`
	References                        bool     `json:"references"`
	Implementation                    bool     `json:"implementation"`
	DocumentSymbols                   bool     `json:"document_symbols"`
	DocumentHighlights                bool     `json:"document_highlights"`
	FoldingRanges                     bool     `json:"folding_ranges"`
	Rename                            bool     `json:"rename"`
	RenamePrepare                     bool     `json:"rename_prepare"`
	CodeActions                       bool     `json:"code_actions"`
	CodeActionResolve                 bool     `json:"code_action_resolve"`
	DocumentFormatting                bool     `json:"document_formatting"`
	RangeFormatting                   bool     `json:"range_formatting"`
	OnTypeFormattingTriggerCharacters []string `json:"on_type_formatting_trigger_characters"`
	SemanticTokens                    bool     `json:"semantic_tokens"`
	InlayHints                        bool     `json:"inlay_hints"`
	WorkspaceSymbols                  bool     `json:"workspace_symbols"`
}

func (s *Server) handleLSPEditorCapabilities(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	filePath, ok := s.resolveLSPFile(w, p, false)
	if !ok {
		return
	}
	capabilities, hasServer, err := s.workspace.EditorLSPCapabilities(r.Context(), filePath)
	if err != nil {
		// A missing or failed server must not disable structural fallbacks.
		hasServer = false
	}
	response := editorCapabilitiesResponse{
		WorkspaceURI:       fileuri.FromPath(s.workspace.RootPath),
		LanguageServer:     hasServer,
		Completion:         true,
		Hover:              true,
		Definition:         true,
		References:         true,
		Implementation:     true,
		DocumentSymbols:    true,
		DocumentHighlights: true,
		FoldingRanges:      true,
		SemanticTokens:     true,
		WorkspaceSymbols:   true,
	}
	if hasServer {
		if provider := capabilities.CompletionProvider; provider != nil {
			response.CompletionResolve = provider.ResolveProvider != nil && *provider.ResolveProvider
			response.CompletionTriggerCharacters = nonNilStrings(provider.TriggerCharacters)
		}
		if provider := capabilities.SignatureHelpProvider; provider != nil {
			response.SignatureHelp = true
			response.SignatureHelpTriggerCharacters = nonNilStrings(provider.TriggerCharacters)
			response.SignatureHelpRetriggerCharacters = nonNilStrings(provider.RetriggerCharacters)
		}
		// Structural features remain available when the server omits them or
		// fails later; only type-aware features are strictly capability-gated.
		response.TypeDefinition = lspCapabilityEnabled(capabilities.TypeDefinitionProvider)
		response.Rename, response.RenamePrepare = renameCapabilities(capabilities.RenameProvider)
		response.CodeActions, response.CodeActionResolve = codeActionCapabilities(capabilities.CodeActionProvider)
		response.DocumentFormatting = lspCapabilityEnabled(capabilities.DocumentFormattingProvider)
		response.RangeFormatting = lspCapabilityEnabled(capabilities.DocumentRangeFormattingProvider)
		if provider := capabilities.DocumentOnTypeFormattingProvider; provider.FirstTriggerCharacter != "" {
			response.OnTypeFormattingTriggerCharacters = append(
				[]string{provider.FirstTriggerCharacter},
				provider.MoreTriggerCharacter...,
			)
		}
		response.SemanticTokens = true
		response.InlayHints = lspCapabilityEnabled(capabilities.InlayHintProvider)
	}
	writeJSON(w, response)
}

func lspCapabilityEnabled(value any) bool {
	if value == nil {
		return false
	}
	if enabled, ok := value.(lsp.Boolean); ok {
		return bool(enabled)
	}
	return true
}

func renameCapabilities(value any) (enabled, prepare bool) {
	switch provider := value.(type) {
	case lsp.Boolean:
		return bool(provider), false
	case *lsp.RenameOptions:
		return true, provider.PrepareProvider != nil && *provider.PrepareProvider
	default:
		return false, false
	}
}

func codeActionCapabilities(value any) (enabled, resolve bool) {
	switch provider := value.(type) {
	case lsp.Boolean:
		return bool(provider), false
	case *lsp.CodeActionOptions:
		return true, provider.ResolveProvider != nil && *provider.ResolveProvider
	default:
		return false, false
	}
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func (s *Server) handleLSPDocumentLifecycle(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Event   string `json:"event"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	var filePath string
	if body.Event == "close" {
		rel, ok := s.workspaceRel(body.Path)
		if !ok {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}
		filePath = filepath.Join(s.workspace.RootPath, rel)
	} else {
		var ok bool
		filePath, ok = s.resolveLSPFile(w, body.Path, false)
		if !ok {
			return
		}
	}
	var err error
	switch body.Event {
	case "open", "save":
		err = s.workspace.SyncEditorDocument(r.Context(), filePath, body.Content, true)
	case "change":
		err = s.workspace.SyncEditorDocument(r.Context(), filePath, body.Content, false)
	case "close":
		err = s.workspace.CloseEditorDocument(r.Context(), filePath)
	default:
		http.Error(w, "invalid document event", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

func (s *Server) handleLSPCompletions(w http.ResponseWriter, r *http.Request) {
	body, filePath, ok := s.decodeLSPTriggerRequest(w, r)
	if !ok {
		return
	}

	var completionContext *lsp.CompletionContext
	if body.TriggerKind > 0 {
		completionContext = &lsp.CompletionContext{
			TriggerKind: lsp.CompletionTriggerKind(body.TriggerKind),
		}
		if body.TriggerCharacter != "" {
			completionContext.TriggerCharacter = &body.TriggerCharacter
		}
	}
	list, err := s.workspace.CompletionItems(
		r.Context(),
		filePath,
		body.Content,
		body.Line-1,
		body.Column-1,
		completionContext,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if list.Items == nil {
		list.Items = []lsp.CompletionItem{}
	}
	writeLSPJSON(w, list)
}

func (s *Server) handleLSPCompletionResolve(w http.ResponseWriter, r *http.Request) {
	var body struct {
		lspDocumentRequest
		Item json.RawMessage `json:"item"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	filePath, ok := s.resolveLSPFile(w, body.Path, true)
	if !ok {
		return
	}
	var requested lsp.CompletionItem
	if err := lsp.Unmarshal(body.Item, &requested); err != nil {
		http.Error(w, "invalid completion item", http.StatusBadRequest)
		return
	}
	item, err := s.workspace.ResolveCompletionItem(r.Context(), filePath, body.Content, requested)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeLSPJSON(w, item)
}

func (s *Server) handleLSPSignatureHelp(w http.ResponseWriter, r *http.Request) {
	body, filePath, ok := s.decodeLSPTriggerRequest(w, r)
	if !ok {
		return
	}

	var signatureContext *lsp.SignatureHelpContext
	if body.TriggerKind > 0 {
		signatureContext = &lsp.SignatureHelpContext{
			TriggerKind: lsp.SignatureHelpTriggerKind(body.TriggerKind),
			IsRetrigger: body.IsRetrigger,
		}
		if body.TriggerCharacter != "" {
			signatureContext.TriggerCharacter = &body.TriggerCharacter
		}
	}
	help, err := s.workspace.SignatureHelp(
		r.Context(),
		filePath,
		body.Content,
		body.Line-1,
		body.Column-1,
		signatureContext,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if help == nil {
		help = &lsp.SignatureHelp{Signatures: []lsp.SignatureInformation{}}
	}
	writeLSPJSON(w, help)
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
		writeLSPJSON(w, documents)
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
	writeLSPJSON(w, converted)
}

func writeLSPJSON(w http.ResponseWriter, value any) {
	data, err := lsp.Marshal(value)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func (s *Server) handleLSPDocumentHighlights(w http.ResponseWriter, r *http.Request) {
	body, filePath, ok := s.decodeLSPPositionRequest(w, r, false)
	if !ok {
		return
	}
	highlights, err := s.workspace.DocumentHighlights(
		r.Context(), filePath, body.Content, body.Line-1, body.Column-1,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if highlights == nil {
		highlights = []lsp.DocumentHighlight{}
	}
	writeLSPJSON(w, highlights)
}

func (s *Server) handleLSPFoldingRanges(w http.ResponseWriter, r *http.Request) {
	body, filePath, ok := s.decodeLSPDocumentRequest(w, r, false)
	if !ok {
		return
	}
	ranges, err := s.workspace.FoldingRanges(r.Context(), filePath, body.Content)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if ranges == nil {
		ranges = []lsp.FoldingRange{}
	}
	writeLSPJSON(w, ranges)
}

func (s *Server) handleLSPSemanticTokens(w http.ResponseWriter, r *http.Request) {
	body, filePath, ok := s.decodeLSPDocumentRequest(w, r, false)
	if !ok {
		return
	}
	tokens, err := s.workspace.SemanticTokens(r.Context(), filePath, body.Content)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if tokens == nil {
		tokens = []language.SemanticToken{}
	}
	writeJSON(w, tokens)
}

type lspPositionRequest struct {
	lspDocumentRequest
	Line   int `json:"line"`
	Column int `json:"column"`
}

type lspTriggerRequest struct {
	lspPositionRequest
	TriggerKind      int    `json:"trigger_kind,omitempty"`
	TriggerCharacter string `json:"trigger_character,omitempty"`
	IsRetrigger      bool   `json:"is_retrigger,omitempty"`
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

func (s *Server) decodeLSPTriggerRequest(w http.ResponseWriter, r *http.Request) (lspTriggerRequest, string, bool) {
	var body lspTriggerRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return body, "", false
	}
	if body.Line < 1 || body.Column < 1 {
		http.Error(w, "line and column must be positive", http.StatusBadRequest)
		return body, "", false
	}
	filePath, ok := s.resolveLSPFile(w, body.Path, false)
	return body, filePath, ok
}

type locationRequest func(context.Context, string, *string, int, int) ([]lsp.Location, error)

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
	for _, location := range locations {
		path, ok := fileuri.Path(string(location.URI))
		if !ok {
			continue
		}
		if targetRel, err := filepath.Rel(s.workspace.RootPath, path); err == nil && targetRel != ".." && !filepath.IsAbs(targetRel) &&
			!strings.HasPrefix(targetRel, ".."+string(filepath.Separator)) {
			continue
		} else {
			abs := filepath.Clean(path)
			if info, err := os.Stat(abs); err != nil || info.IsDir() {
				continue
			}
			s.allowLSPExternalFile(filepath.ToSlash(abs))
		}
	}
	writeLSPJSON(w, locations)
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
		Revision: fileRevision(data),
		Size:     int64(len(data)),
	})
}

func (s *Server) diagnosticItems(filePath string, values []language.Diagnostic) []diagnosticItem {
	relPath := filePath
	if rel, err := filepath.Rel(s.workspace.RootPath, filePath); err == nil {
		relPath = filepath.ToSlash(rel)
	}
	result := make([]diagnosticItem, 0, len(values))
	for _, diagnostic := range values {
		severity := "info"
		switch diagnostic.Severity {
		case language.SeverityError:
			severity = "error"
		case language.SeverityWarning:
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
