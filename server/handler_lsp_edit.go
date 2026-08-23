package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/fileuri"
	"github.com/adrianliechti/wingman-agent/pkg/lsp"
)

type lspWorkspaceDocument struct {
	Path string `json:"path"`
	// Revision identifies the exact content the language server used. For an
	// open document that can intentionally differ from the disk revision.
	Revision string `json:"revision,omitempty"`
	Exists   bool   `json:"exists"`
}

type lspWorkspaceEditResponse struct {
	Edit      *lsp.WorkspaceEdit              `json:"edit"`
	Documents map[string]lspWorkspaceDocument `json:"documents"`
}

type lspCodeActionsResponse struct {
	Actions   []lsp.CommandOrCodeAction       `json:"actions"`
	Documents map[string]lspWorkspaceDocument `json:"documents"`
}

type lspCommandEditResponse struct {
	Label     string                          `json:"label,omitempty"`
	Edit      *lsp.WorkspaceEdit              `json:"edit"`
	Documents map[string]lspWorkspaceDocument `json:"documents"`
}

type lspCommandResponse struct {
	Edits []lspCommandEditResponse `json:"edits"`
}

func (s *Server) handleLSPPrepareRename(w http.ResponseWriter, r *http.Request) {
	body, filePath, ok := s.decodeLSPPositionRequest(w, r, true)
	if !ok {
		return
	}
	result, err := s.workspace.PrepareRename(r.Context(), filePath, body.Content, body.Line-1, body.Column-1)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeLSPJSON(w, result)
}

func (s *Server) handleLSPRename(w http.ResponseWriter, r *http.Request) {
	var body struct {
		lspPositionRequest
		NewName string `json:"new_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if body.Line < 1 || body.Column < 1 || strings.TrimSpace(body.NewName) == "" {
		http.Error(w, "line, column, and new_name are required", http.StatusBadRequest)
		return
	}
	filePath, ok := s.resolveLSPFile(w, body.Path, true)
	if !ok {
		return
	}
	edit, err := s.workspace.Rename(r.Context(), filePath, body.Content, body.Line-1, body.Column-1, body.NewName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	s.writeWorkspaceEditResponse(w, edit)
}

type lspRangeRequest struct {
	lspDocumentRequest
	Range lsp.Range `json:"range"`
}

func (s *Server) handleLSPCodeActions(w http.ResponseWriter, r *http.Request) {
	var body struct {
		lspRangeRequest
		Only        []lsp.CodeActionKind      `json:"only,omitempty"`
		TriggerKind lsp.CodeActionTriggerKind `json:"trigger_kind,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	filePath, ok := s.resolveLSPFile(w, body.Path, true)
	if !ok {
		return
	}
	actions, err := s.workspace.CodeActions(r.Context(), filePath, body.Content, body.Range, body.Only, body.TriggerKind)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if actions == nil {
		actions = []lsp.CommandOrCodeAction{}
	}
	edits := make([]*lsp.WorkspaceEdit, 0, len(actions))
	for _, action := range actions {
		if action, ok := action.(*lsp.CodeAction); ok && action.Edit != nil {
			edits = append(edits, action.Edit)
		}
	}
	documents, err := s.workspaceEditDocuments(edits...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeLSPJSON(w, lspCodeActionsResponse{Actions: actions, Documents: documents})
}

func (s *Server) handleLSPCodeActionResolve(w http.ResponseWriter, r *http.Request) {
	var body struct {
		lspDocumentRequest
		Action json.RawMessage `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	filePath, ok := s.resolveLSPFile(w, body.Path, true)
	if !ok {
		return
	}
	var action lsp.CodeAction
	if err := lsp.Unmarshal(body.Action, &action); err != nil {
		http.Error(w, "invalid code action", http.StatusBadRequest)
		return
	}
	resolved, err := s.workspace.ResolveCodeAction(r.Context(), filePath, body.Content, action)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if resolved == nil {
		resolved = &action
	}
	documents, err := s.workspaceEditDocuments(resolved.Edit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeLSPJSON(w, struct {
		Action    *lsp.CodeAction                 `json:"action"`
		Documents map[string]lspWorkspaceDocument `json:"documents"`
	}{Action: resolved, Documents: documents})
}

func (s *Server) handleLSPExecuteCommand(w http.ResponseWriter, r *http.Request) {
	var body struct {
		lspDocumentRequest
		Command json.RawMessage `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	filePath, ok := s.resolveLSPFile(w, body.Path, true)
	if !ok {
		return
	}
	var command lsp.Command
	if err := lsp.Unmarshal(body.Command, &command); err != nil || command.Command == "" {
		http.Error(w, "invalid command", http.StatusBadRequest)
		return
	}
	_, edits, err := s.workspace.ExecuteLSPCommandWithEdits(r.Context(), filePath, body.Content, command)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	response := lspCommandResponse{Edits: make([]lspCommandEditResponse, 0, len(edits))}
	for _, requested := range edits {
		edit := requested.Edit
		documents, err := s.workspaceEditDocuments(&edit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		response.Edits = append(response.Edits, lspCommandEditResponse{
			Label: requested.Label, Edit: &edit, Documents: documents,
		})
	}
	writeLSPJSON(w, response)
}

type lspFormattingRequest struct {
	lspDocumentRequest
	Options lsp.FormattingOptions `json:"options"`
}

func (s *Server) handleLSPFormatting(w http.ResponseWriter, r *http.Request) {
	var body lspFormattingRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	filePath, ok := s.resolveLSPFile(w, body.Path, true)
	if !ok {
		return
	}
	edits, err := s.workspace.Formatting(r.Context(), filePath, body.Content, body.Options)
	s.writeTextEdits(w, edits, err)
}

func (s *Server) handleLSPRangeFormatting(w http.ResponseWriter, r *http.Request) {
	var body struct {
		lspFormattingRequest
		Range lsp.Range `json:"range"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	filePath, ok := s.resolveLSPFile(w, body.Path, true)
	if !ok {
		return
	}
	edits, err := s.workspace.RangeFormatting(r.Context(), filePath, body.Content, body.Range, body.Options)
	s.writeTextEdits(w, edits, err)
}

func (s *Server) handleLSPOnTypeFormatting(w http.ResponseWriter, r *http.Request) {
	var body struct {
		lspFormattingRequest
		Line      int    `json:"line"`
		Column    int    `json:"column"`
		Character string `json:"character"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if body.Line < 1 || body.Column < 1 || body.Character == "" {
		http.Error(w, "line, column, and character are required", http.StatusBadRequest)
		return
	}
	filePath, ok := s.resolveLSPFile(w, body.Path, true)
	if !ok {
		return
	}
	edits, err := s.workspace.OnTypeFormatting(r.Context(), filePath, body.Content, body.Line-1, body.Column-1, body.Character, body.Options)
	s.writeTextEdits(w, edits, err)
}

func (s *Server) handleLSPInlayHints(w http.ResponseWriter, r *http.Request) {
	var body lspRangeRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	filePath, ok := s.resolveLSPFile(w, body.Path, true)
	if !ok {
		return
	}
	hints, err := s.workspace.InlayHints(r.Context(), filePath, body.Content, body.Range)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if hints == nil {
		hints = []lsp.InlayHint{}
	}
	writeLSPJSON(w, hints)
}

func (s *Server) writeTextEdits(w http.ResponseWriter, edits []lsp.TextEdit, err error) {
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if edits == nil {
		edits = []lsp.TextEdit{}
	}
	writeLSPJSON(w, edits)
}

func (s *Server) writeWorkspaceEditResponse(w http.ResponseWriter, edit *lsp.WorkspaceEdit) {
	documents, err := s.workspaceEditDocuments(edit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeLSPJSON(w, lspWorkspaceEditResponse{Edit: edit, Documents: documents})
}

func (s *Server) workspaceEditDocuments(edits ...*lsp.WorkspaceEdit) (map[string]lspWorkspaceDocument, error) {
	documents := make(map[string]lspWorkspaceDocument)
	for _, edit := range edits {
		for _, key := range workspaceEditURIs(edit) {
			if _, exists := documents[key]; exists {
				continue
			}
			filePath, ok := fileuri.Path(key)
			if !ok {
				return nil, fmt.Errorf("language server edit targets a non-file URI")
			}
			rel, err := filepath.Rel(s.workspace.RootPath, filePath)
			if err != nil || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return nil, fmt.Errorf("language server edit targets a file outside the workspace")
			}
			content, err := s.workspace.Root.ReadFile(rel)
			switch {
			case err == nil:
				document := lspWorkspaceDocument{Path: filepath.ToSlash(rel), Revision: fileRevision(content), Exists: true}
				if buffer, ok := s.workspace.EditorLSPDocumentContent(filePath); ok {
					document.Revision = fileRevision([]byte(buffer))
				}
				documents[key] = document
			case errors.Is(err, fs.ErrNotExist):
				documents[key] = lspWorkspaceDocument{Path: filepath.ToSlash(rel), Exists: false}
			default:
				return nil, err
			}
		}
	}
	return documents, nil
}

func workspaceEditURIs(edit *lsp.WorkspaceEdit) []string {
	if edit == nil {
		return nil
	}
	result := make([]string, 0, len(edit.Changes)+len(edit.DocumentChanges))
	if edit.DocumentChanges != nil {
		for _, change := range edit.DocumentChanges {
			switch change := change.(type) {
			case *lsp.TextDocumentEdit:
				result = append(result, change.TextDocument.URI.String())
			case *lsp.CreateFile:
				result = append(result, change.URI.String())
			case *lsp.RenameFile:
				result = append(result, change.OldURI.String(), change.NewURI.String())
			case *lsp.DeleteFile:
				result = append(result, change.URI.String())
			}
		}
		return result
	}
	for documentURI := range edit.Changes {
		result = append(result, documentURI.String())
	}
	return result
}
