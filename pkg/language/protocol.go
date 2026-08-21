package language

import (
	"context"
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/lsp"
)

func HoverText(hover *lsp.Hover) string {
	if hover == nil {
		return ""
	}
	switch contents := hover.Contents.(type) {
	case lsp.String:
		return string(contents)
	case *lsp.MarkupContent:
		return contents.Value
	case *lsp.MarkedStringWithLanguage:
		return contents.Value
	case lsp.MarkedStringSlice:
		parts := make([]string, 0, len(contents))
		for _, marked := range contents {
			switch marked := marked.(type) {
			case lsp.String:
				parts = append(parts, string(marked))
			case *lsp.MarkedStringWithLanguage:
				parts = append(parts, marked.Value)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func DocumentSymbolsFromProtocol(result lsp.DocumentSymbolResult) ([]lsp.DocumentSymbol, []lsp.SymbolInformation) {
	switch result := result.(type) {
	case lsp.DocumentSymbolSlice:
		return []lsp.DocumentSymbol(result), nil
	case lsp.SymbolInformationSlice:
		return nil, []lsp.SymbolInformation(result)
	default:
		return nil, nil
	}
}

func (s *Service) WorkspaceSymbols(ctx context.Context, query string) ([]lsp.SymbolInformation, []lsp.WorkspaceSymbol, error) {
	s.lifeMu.RLock()
	defer s.lifeMu.RUnlock()
	if s.closed {
		return nil, nil, fmt.Errorf("language service is closed")
	}
	projects := s.manager.Projects()
	if len(projects) == 0 {
		return nil, nil, fmt.Errorf("no LSP servers detected in workspace")
	}
	var symbols []lsp.SymbolInformation
	var workspaceSymbols []lsp.WorkspaceSymbol
	for _, project := range projects {
		session, err := s.manager.ProjectSession(ctx, project)
		if err != nil {
			continue
		}
		result, err := session.WorkspaceSymbols(ctx, query)
		if err != nil {
			continue
		}
		switch result := result.(type) {
		case lsp.SymbolInformationSlice:
			symbols = append(symbols, []lsp.SymbolInformation(result)...)
		case lsp.WorkspaceSymbolSlice:
			workspaceSymbols = append(workspaceSymbols, []lsp.WorkspaceSymbol(result)...)
		}
	}
	return symbols, workspaceSymbols, nil
}
