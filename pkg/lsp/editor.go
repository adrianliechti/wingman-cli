package lsp

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/fileuri"
	"go.lsp.dev/protocol"
	lspuri "go.lsp.dev/uri"
)

// CommandWorkspaceEdit is an edit a language server asks the client to apply
// while executing a workspace command. The caller remains responsible for
// validating and applying it.
type CommandWorkspaceEdit struct {
	Label string
	Edit  protocol.WorkspaceEdit
}

func (s *Session) PrepareRename(ctx context.Context, uri string, line, column int) (protocol.PrepareRenameResult, error) {
	return retryRPC(ctx, func() (protocol.PrepareRenameResult, error) {
		return s.rpc.PrepareRename(ctx, &protocol.PrepareRenameParams{
			TextDocumentPositionParams: protocol.TextDocumentPositionParams{
				TextDocument: wireDocument(uri),
				Position:     wirePosition(line, column),
			},
		})
	})
}

func (s *Session) Rename(ctx context.Context, uri string, line, column int, newName string) (*protocol.WorkspaceEdit, error) {
	return retryRPC(ctx, func() (*protocol.WorkspaceEdit, error) {
		return s.rpc.Rename(ctx, &protocol.RenameParams{
			TextDocumentPositionParams: protocol.TextDocumentPositionParams{
				TextDocument: wireDocument(uri),
				Position:     wirePosition(line, column),
			},
			NewName: newName,
		})
	})
}

func (s *Session) CodeActions(
	ctx context.Context,
	uri string,
	selection protocol.Range,
	only []protocol.CodeActionKind,
	trigger protocol.CodeActionTriggerKind,
) ([]protocol.CommandOrCodeAction, error) {
	diagnostics, _ := s.protocolDiagnostics(ctx, uri)
	return retryRPC(ctx, func() ([]protocol.CommandOrCodeAction, error) {
		return s.rpc.CodeAction(ctx, &protocol.CodeActionParams{
			TextDocument: wireDocument(uri),
			Range:        selection,
			Context: protocol.CodeActionContext{
				Diagnostics: diagnosticsOverlapping(diagnostics, selection),
				Only:        only,
				TriggerKind: trigger,
			},
		})
	})
}

func (s *Session) ResolveCodeAction(ctx context.Context, action protocol.CodeAction) (*protocol.CodeAction, error) {
	return retryRPC(ctx, func() (*protocol.CodeAction, error) {
		return s.rpc.CodeActionResolve(ctx, &action)
	})
}

func (s *Session) ExecuteCommand(ctx context.Context, command protocol.Command) (any, error) {
	s.commandMu.Lock()
	defer s.commandMu.Unlock()
	return s.rpc.ExecuteCommand(ctx, &protocol.ExecuteCommandParams{
		Command:   command.Command,
		Arguments: command.Arguments,
	})
}

// ExecuteCommandWithEdits accepts workspace/applyEdit callbacks for the
// duration of one serialized command and returns them to the editor client.
func (s *Session) ExecuteCommandWithEdits(ctx context.Context, command protocol.Command) (result any, edits []CommandWorkspaceEdit, err error) {
	s.commandMu.Lock()
	defer s.commandMu.Unlock()

	s.applyEditMu.Lock()
	s.captureEdits = true
	s.commandEdits = nil
	s.applyEditMu.Unlock()
	defer func() {
		s.applyEditMu.Lock()
		edits = s.commandEdits
		s.commandEdits = nil
		s.captureEdits = false
		s.applyEditMu.Unlock()
	}()

	result, err = s.rpc.ExecuteCommand(ctx, &protocol.ExecuteCommandParams{
		Command:   command.Command,
		Arguments: command.Arguments,
	})
	return result, edits, err
}

func (s *Session) captureCommandEdit(params *protocol.ApplyWorkspaceEditParams) error {
	s.applyEditMu.Lock()
	defer s.applyEditMu.Unlock()
	if !s.captureEdits {
		return fmt.Errorf("the client is not executing a command")
	}
	if err := s.validateCommandEdit(params.Edit); err != nil {
		return err
	}
	label := ""
	if params.Label != nil {
		label = *params.Label
	}
	s.commandEdits = append(s.commandEdits, CommandWorkspaceEdit{Label: label, Edit: params.Edit})
	return nil
}

func (s *Session) validateCommandEdit(edit protocol.WorkspaceEdit) error {
	root, ok := fileuri.Path(s.rootURI)
	if !ok {
		return fmt.Errorf("the workspace root is not a file URI")
	}
	validateURI := func(uri lspuri.URI) error {
		path, ok := fileuri.Path(uri.String())
		if !ok {
			return fmt.Errorf("the edit targets a non-file URI")
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("the edit targets a file outside the workspace")
		}
		return nil
	}

	if edit.DocumentChanges != nil {
		for _, change := range edit.DocumentChanges {
			document, ok := change.(*protocol.TextDocumentEdit)
			if !ok {
				return fmt.Errorf("the edit contains an unsupported file operation")
			}
			if err := validateURI(document.TextDocument.URI); err != nil {
				return err
			}
			for _, element := range document.Edits {
				switch element.(type) {
				case *protocol.TextEdit, *protocol.AnnotatedTextEdit:
				default:
					return fmt.Errorf("the edit contains an unsupported snippet edit")
				}
			}
		}
		return nil
	}
	for uri := range edit.Changes {
		if err := validateURI(uri); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) Formatting(ctx context.Context, uri string, options protocol.FormattingOptions) ([]protocol.TextEdit, error) {
	return retryRPC(ctx, func() ([]protocol.TextEdit, error) {
		return s.rpc.Formatting(ctx, &protocol.DocumentFormattingParams{
			TextDocument: wireDocument(uri),
			Options:      options,
		})
	})
}

func (s *Session) RangeFormatting(ctx context.Context, uri string, selection protocol.Range, options protocol.FormattingOptions) ([]protocol.TextEdit, error) {
	return retryRPC(ctx, func() ([]protocol.TextEdit, error) {
		return s.rpc.RangeFormatting(ctx, &protocol.DocumentRangeFormattingParams{
			TextDocument: wireDocument(uri),
			Range:        selection,
			Options:      options,
		})
	})
}

func (s *Session) OnTypeFormatting(ctx context.Context, uri string, line, column int, character string, options protocol.FormattingOptions) ([]protocol.TextEdit, error) {
	return retryRPC(ctx, func() ([]protocol.TextEdit, error) {
		return s.rpc.OnTypeFormatting(ctx, &protocol.DocumentOnTypeFormattingParams{
			TextDocument: wireDocument(uri),
			Position:     wirePosition(line, column),
			Ch:           character,
			Options:      options,
		})
	})
}

func (s *Session) InlayHints(ctx context.Context, uri string, selection protocol.Range) ([]protocol.InlayHint, error) {
	return retryRPC(ctx, func() ([]protocol.InlayHint, error) {
		return s.rpc.InlayHint(ctx, &protocol.InlayHintParams{
			TextDocument: wireDocument(uri),
			Range:        selection,
		})
	})
}

func diagnosticsOverlapping(diagnostics []protocol.Diagnostic, selection protocol.Range) []protocol.Diagnostic {
	result := make([]protocol.Diagnostic, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		if positionBefore(diagnostic.Range.End, selection.Start) || positionBefore(selection.End, diagnostic.Range.Start) {
			continue
		}
		result = append(result, diagnostic)
	}
	return result
}

func positionBefore(a, b protocol.Position) bool {
	return a.Line < b.Line || a.Line == b.Line && a.Character < b.Character
}
