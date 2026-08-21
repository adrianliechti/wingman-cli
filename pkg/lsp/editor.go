package lsp

import (
	"context"

	"go.lsp.dev/protocol"
)

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
	return retryRPC(ctx, func() (any, error) {
		return s.rpc.ExecuteCommand(ctx, &protocol.ExecuteCommandParams{
			Command:   command.Command,
			Arguments: command.Arguments,
		})
	})
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
