package language

import (
	"context"
	"fmt"

	"github.com/adrianliechti/wingman-agent/pkg/graph"
	"github.com/adrianliechti/wingman-agent/pkg/lsp"
)

func (s *Service) DefinitionLocations(ctx context.Context, filePath string, content *string, line, column int) ([]lsp.Location, error) {
	return s.structuralLocations(
		ctx, filePath, content, line, column,
		func(capabilities lsp.ServerCapabilities) any { return capabilities.DefinitionProvider },
		func(session *lsp.Session, uri string) ([]lsp.Location, error) {
			return session.DefinitionLocations(ctx, uri, line, column)
		},
		(*graph.Engine).Definitions,
	)
}

func (s *Service) TypeDefinitionLocations(ctx context.Context, filePath string, content *string, line, column int) ([]lsp.Location, error) {
	return s.locationRequest(ctx, filePath, content, func(session *lsp.Session, uri string) ([]lsp.Location, error) {
		return session.TypeDefinitionLocations(ctx, uri, line, column)
	})
}

func (s *Service) ImplementationLocations(ctx context.Context, filePath string, content *string, line, column int) ([]lsp.Location, error) {
	return s.structuralLocations(
		ctx, filePath, content, line, column,
		func(capabilities lsp.ServerCapabilities) any { return capabilities.ImplementationProvider },
		func(session *lsp.Session, uri string) ([]lsp.Location, error) {
			return session.ImplementationLocations(ctx, uri, line, column)
		},
		(*graph.Engine).Implementations,
	)
}

func (s *Service) ReferenceLocations(ctx context.Context, filePath string, content *string, line, column int) ([]lsp.Location, error) {
	return s.structuralLocations(
		ctx, filePath, content, line, column,
		func(capabilities lsp.ServerCapabilities) any { return capabilities.ReferencesProvider },
		func(session *lsp.Session, uri string) ([]lsp.Location, error) {
			return session.ReferenceLocations(ctx, uri, line, column)
		},
		(*graph.Engine).References,
	)
}

func (s *Service) structuralLocations(
	ctx context.Context,
	filePath string,
	content *string,
	line, column int,
	capability func(lsp.ServerCapabilities) any,
	request func(*lsp.Session, string) ([]lsp.Location, error),
	fallback func(*graph.Engine, context.Context, string, []byte, int, int) ([]graph.Location, error),
) ([]lsp.Location, error) {
	if s.hasLSPServerFor(filePath) {
		locations, supported, err := s.capabilityLocationRequest(ctx, filePath, content, capability, request)
		if supported && err == nil {
			return locations, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return s.graphLocations(ctx, filePath, content, line, column, fallback)
}

func (s *Service) capabilityLocationRequest(
	ctx context.Context,
	filePath string,
	content *string,
	capability func(lsp.ServerCapabilities) any,
	request func(*lsp.Session, string) ([]lsp.Location, error),
) (locations []lsp.Location, supported bool, err error) {
	err = s.withLSPDocument(ctx, filePath, content, func(session *lsp.Session, uri string) error {
		supported = lsp.CapabilityEnabled(capability(session.Capabilities()))
		if !supported {
			return nil
		}
		locations, err = request(session, uri)
		return err
	})
	return
}

func (s *Service) locationRequest(ctx context.Context, filePath string, content *string, request func(*lsp.Session, string) ([]lsp.Location, error)) ([]lsp.Location, error) {
	var locations []lsp.Location
	err := s.withLSPDocument(ctx, filePath, content, func(session *lsp.Session, uri string) error {
		var err error
		locations, err = request(session, uri)
		return err
	})
	if err != nil {
		return nil, err
	}
	return locations, nil
}

func (s *Service) Hover(ctx context.Context, filePath string, content *string, line, column int) (string, error) {
	if s.hasLSPServerFor(filePath) {
		var hover *lsp.Hover
		supported := false
		err := s.withLSPDocument(ctx, filePath, content, func(session *lsp.Session, uri string) error {
			supported = lsp.CapabilityEnabled(session.Capabilities().HoverProvider)
			if !supported {
				return nil
			}
			var err error
			hover, err = session.Hover(ctx, uri, line, column)
			return err
		})
		if supported && err == nil {
			return HoverText(hover), nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
	}
	return s.graphHover(ctx, filePath, content, line, column)
}

func (s *Service) CompletionItems(ctx context.Context, filePath string, content *string, line, column int, completionContext *lsp.CompletionContext) (lsp.CompletionList, error) {
	if s.hasLSPServerFor(filePath) {
		var list lsp.CompletionList
		supported := false
		err := s.withLSPDocument(ctx, filePath, content, func(session *lsp.Session, uri string) error {
			supported = session.Capabilities().CompletionProvider != nil
			if !supported {
				return nil
			}
			var err error
			list, err = session.CompletionItems(ctx, uri, line, column, completionContext)
			return err
		})
		if supported && err == nil {
			return list, nil
		}
		if ctx.Err() != nil {
			return lsp.CompletionList{}, ctx.Err()
		}
	}
	if completionContext != nil && completionContext.TriggerCharacter != nil {
		return lsp.CompletionList{Items: []lsp.CompletionItem{}}, nil
	}
	items, err := s.graphCompletionItems(ctx, filePath, content)
	return lsp.CompletionList{Items: items}, err
}

func (s *Service) ResolveCompletionItem(ctx context.Context, filePath string, content *string, item lsp.CompletionItem) (lsp.CompletionItem, error) {
	var result lsp.CompletionItem
	err := s.withLSPDocument(ctx, filePath, content, func(session *lsp.Session, _ string) error {
		var err error
		result, err = session.ResolveCompletionItem(ctx, item)
		return err
	})
	return result, err
}

func (s *Service) SignatureHelp(ctx context.Context, filePath string, content *string, line, column int, signatureContext *lsp.SignatureHelpContext) (*lsp.SignatureHelp, error) {
	if !s.hasLSPServerFor(filePath) {
		return nil, nil
	}
	var result *lsp.SignatureHelp
	err := s.withLSPDocument(ctx, filePath, content, func(session *lsp.Session, uri string) error {
		var err error
		result, err = session.SignatureHelp(ctx, uri, line, column, signatureContext)
		return err
	})
	return result, err
}

func (s *Service) PrepareRename(ctx context.Context, filePath string, content *string, line, column int) (lsp.PrepareRenameResult, error) {
	var result lsp.PrepareRenameResult
	err := s.withLSPDocument(ctx, filePath, content, func(session *lsp.Session, uri string) error {
		var err error
		result, err = session.PrepareRename(ctx, uri, line, column)
		return err
	})
	return result, err
}

func (s *Service) Rename(ctx context.Context, filePath string, content *string, line, column int, newName string) (*lsp.WorkspaceEdit, error) {
	var result *lsp.WorkspaceEdit
	err := s.withLSPDocument(ctx, filePath, content, func(session *lsp.Session, uri string) error {
		var err error
		result, err = session.Rename(ctx, uri, line, column, newName)
		return err
	})
	return result, err
}

func (s *Service) CodeActions(ctx context.Context, filePath string, content *string, selection lsp.Range, only []lsp.CodeActionKind, trigger lsp.CodeActionTriggerKind) ([]lsp.CommandOrCodeAction, error) {
	var result []lsp.CommandOrCodeAction
	err := s.withLSPDocument(ctx, filePath, content, func(session *lsp.Session, uri string) error {
		var err error
		result, err = session.CodeActions(ctx, uri, selection, only, trigger)
		return err
	})
	return result, err
}

func (s *Service) ResolveCodeAction(ctx context.Context, filePath string, content *string, action lsp.CodeAction) (*lsp.CodeAction, error) {
	var result *lsp.CodeAction
	err := s.withLSPDocument(ctx, filePath, content, func(session *lsp.Session, _ string) error {
		var err error
		result, err = session.ResolveCodeAction(ctx, action)
		return err
	})
	return result, err
}

func (s *Service) ExecuteCommand(ctx context.Context, filePath string, content *string, command lsp.Command) (any, error) {
	var result any
	err := s.withLSPDocument(ctx, filePath, content, func(session *lsp.Session, _ string) error {
		var err error
		result, err = session.ExecuteCommand(ctx, command)
		return err
	})
	return result, err
}

func (s *Service) ExecuteCommandWithEdits(ctx context.Context, filePath string, content *string, command lsp.Command) (any, []lsp.CommandWorkspaceEdit, error) {
	var result any
	var edits []lsp.CommandWorkspaceEdit
	err := s.withLSPDocument(ctx, filePath, content, func(session *lsp.Session, _ string) error {
		var err error
		result, edits, err = session.ExecuteCommandWithEdits(ctx, command)
		return err
	})
	return result, edits, err
}

func (s *Service) Formatting(ctx context.Context, filePath string, content *string, options lsp.FormattingOptions) ([]lsp.TextEdit, error) {
	var result []lsp.TextEdit
	err := s.withLSPDocument(ctx, filePath, content, func(session *lsp.Session, uri string) error {
		var err error
		result, err = session.Formatting(ctx, uri, options)
		return err
	})
	return result, err
}

func (s *Service) RangeFormatting(ctx context.Context, filePath string, content *string, selection lsp.Range, options lsp.FormattingOptions) ([]lsp.TextEdit, error) {
	var result []lsp.TextEdit
	err := s.withLSPDocument(ctx, filePath, content, func(session *lsp.Session, uri string) error {
		var err error
		result, err = session.RangeFormatting(ctx, uri, selection, options)
		return err
	})
	return result, err
}

func (s *Service) OnTypeFormatting(ctx context.Context, filePath string, content *string, line, column int, character string, options lsp.FormattingOptions) ([]lsp.TextEdit, error) {
	var result []lsp.TextEdit
	err := s.withLSPDocument(ctx, filePath, content, func(session *lsp.Session, uri string) error {
		var err error
		result, err = session.OnTypeFormatting(ctx, uri, line, column, character, options)
		return err
	})
	return result, err
}

func (s *Service) InlayHints(ctx context.Context, filePath string, content *string, selection lsp.Range) ([]lsp.InlayHint, error) {
	var result []lsp.InlayHint
	err := s.withLSPDocument(ctx, filePath, content, func(session *lsp.Session, uri string) error {
		var err error
		result, err = session.InlayHints(ctx, uri, selection)
		return err
	})
	return result, err
}

func (s *Service) DocumentSymbols(ctx context.Context, filePath string, content *string) ([]lsp.DocumentSymbol, []lsp.SymbolInformation, error) {
	if s.hasLSPServerFor(filePath) {
		var documents []lsp.DocumentSymbol
		var flat []lsp.SymbolInformation
		supported := false
		err := s.withLSPDocument(ctx, filePath, content, func(session *lsp.Session, uri string) error {
			supported = lsp.CapabilityEnabled(session.Capabilities().DocumentSymbolProvider)
			if !supported {
				return nil
			}
			result, err := session.DocumentSymbols(ctx, uri)
			documents, flat = DocumentSymbolsFromProtocol(result)
			return err
		})
		if supported && err == nil {
			return documents, flat, nil
		}
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
	}
	return s.graphDocumentSymbols(filePath, content)
}

func (s *Service) DocumentHighlights(ctx context.Context, filePath string, content *string, line, column int) ([]lsp.DocumentHighlight, error) {
	if s.hasLSPServerFor(filePath) {
		var result []lsp.DocumentHighlight
		supported := false
		err := s.withLSPDocument(ctx, filePath, content, func(session *lsp.Session, uri string) error {
			supported = lsp.CapabilityEnabled(session.Capabilities().DocumentHighlightProvider)
			if !supported {
				return nil
			}
			var err error
			result, err = session.DocumentHighlights(ctx, uri, line, column)
			return err
		})
		if supported && err == nil {
			return result, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return s.graphDocumentHighlights(filePath, content, line, column)
}

func (s *Service) FoldingRanges(ctx context.Context, filePath string, content *string) ([]lsp.FoldingRange, error) {
	if s.hasLSPServerFor(filePath) {
		var result []lsp.FoldingRange
		supported := false
		err := s.withLSPDocument(ctx, filePath, content, func(session *lsp.Session, uri string) error {
			supported = lsp.CapabilityEnabled(session.Capabilities().FoldingRangeProvider)
			if !supported {
				return nil
			}
			var err error
			result, err = session.FoldingRanges(ctx, uri)
			return err
		})
		if supported && err == nil {
			return result, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return s.graphFoldingRanges(filePath, content)
}

func (s *Service) SemanticTokens(ctx context.Context, filePath string, content *string) ([]SemanticToken, error) {
	if s.hasLSPServerFor(filePath) {
		var tokens *lsp.SemanticTokens
		var legend lsp.SemanticTokensLegend
		supported := false
		err := s.withLSPDocument(ctx, filePath, content, func(session *lsp.Session, uri string) error {
			supported = lsp.CapabilityEnabled(session.Capabilities().SemanticTokensProvider)
			if !supported {
				return nil
			}
			var err error
			legend = semanticTokenLegend(session.Capabilities())
			tokens, err = session.SemanticTokens(ctx, uri)
			return err
		})
		if supported && err == nil && tokens != nil {
			return decodeSemanticTokens(tokens.Data, legend)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return s.graphSemanticTokens(filePath, content)
}

func semanticTokenLegend(capabilities lsp.ServerCapabilities) lsp.SemanticTokensLegend {
	switch options := capabilities.SemanticTokensProvider.(type) {
	case *lsp.SemanticTokensOptions:
		return options.Legend
	case *lsp.SemanticTokensRegistrationOptions:
		return options.Legend
	default:
		return lsp.SemanticTokensLegend{}
	}
}

func decodeSemanticTokens(data []uint32, legend lsp.SemanticTokensLegend) ([]SemanticToken, error) {
	if len(data)%5 != 0 {
		return nil, fmt.Errorf("invalid semantic token data length %d", len(data))
	}

	tokens := make([]SemanticToken, 0, len(data)/5)
	line, character := 0, 0
	for index := 0; index < len(data); index += 5 {
		lineDelta := int(data[index])
		characterDelta := int(data[index+1])
		line += lineDelta
		if lineDelta == 0 {
			character += characterDelta
		} else {
			character = characterDelta
		}
		typeIndex := int(data[index+3])
		if typeIndex >= len(legend.TokenTypes) {
			continue
		}
		modifierBits := data[index+4]
		modifiers := make([]string, 0)
		for modifierIndex, modifier := range legend.TokenModifiers {
			if modifierIndex < 32 && modifierBits&(1<<modifierIndex) != 0 {
				modifiers = append(modifiers, modifier)
			}
		}
		tokens = append(tokens, SemanticToken{
			Line:      line,
			Character: character,
			Length:    int(data[index+2]),
			Type:      legend.TokenTypes[typeIndex],
			Modifiers: modifiers,
		})
	}
	return tokens, nil
}
