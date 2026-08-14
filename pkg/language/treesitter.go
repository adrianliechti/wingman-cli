package language

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/fileuri"
	"github.com/adrianliechti/wingman-agent/pkg/graph"
	"github.com/adrianliechti/wingman-agent/pkg/lsp"
)

func (s *Service) graphLocations(ctx context.Context, filePath string, content *string, line, column int, lookup func(*graph.Engine, context.Context, string, []byte, int, int) ([]graph.Location, error)) ([]lsp.Location, error) {
	relative, err := filepath.Rel(s.root, filePath)
	if err != nil {
		return nil, err
	}
	var source []byte
	if content != nil {
		source = []byte(*content)
	}
	locations, err := lookup(s.graph, ctx, filepath.ToSlash(relative), source, line, column)
	if err != nil {
		return nil, err
	}
	result := make([]lsp.Location, 0, len(locations))
	for _, location := range locations {
		uri, err := lsp.ParseURI(fileuri.FromPath(filepath.Join(s.root, filepath.FromSlash(location.File))))
		if err != nil {
			return nil, err
		}
		position := lsp.Position{Line: uint32(location.Line), Character: uint32(location.Col)}
		result = append(result, lsp.Location{
			URI:   uri,
			Range: lsp.Range{Start: position, End: position},
		})
	}
	return result, nil
}

func (s *Service) graphCompletionItems(ctx context.Context, filePath string, content *string) ([]lsp.CompletionItem, error) {
	source, err := editorSource(filePath, content)
	if err != nil {
		return nil, err
	}
	result := make([]lsp.CompletionItem, 0, 128)
	seen := make(map[string]bool)
	var add func([]*graph.Symbol)
	add = func(symbols []*graph.Symbol) {
		for _, symbol := range symbols {
			if symbol.Name != "" && !seen[symbol.Name] {
				seen[symbol.Name] = true
				result = append(result, lsp.CompletionItem{
					Label:  symbol.Name,
					Kind:   lsp.CompletionItemKind(completionKind(symbol.Kind)),
					Detail: lsp.Optional(string(symbol.Kind) + " · tree-sitter"),
				})
			}
			add(symbol.Children)
		}
	}
	add(graph.FileSymbols(filepath.Base(filePath), source))

	nodes, err := s.graph.Search(ctx, graph.SearchOpts{Limit: 200})
	if err != nil {
		return result, nil
	}
	relative, _ := filepath.Rel(s.root, filePath)
	relative = filepath.ToSlash(relative)
	for _, node := range nodes {
		if node.Name == "" || seen[node.Name] {
			continue
		}
		seen[node.Name] = true
		detail := string(node.Kind) + " · " + node.File + " · tree-sitter"
		if node.File == relative {
			detail = string(node.Kind) + " · current file · tree-sitter"
		}
		result = append(result, lsp.CompletionItem{
			Label:  node.Name,
			Kind:   lsp.CompletionItemKind(completionKind(node.Kind)),
			Detail: lsp.Optional(detail),
		})
	}
	return result, nil
}

func (s *Service) graphHover(ctx context.Context, filePath string, content *string, line, column int) (string, error) {
	relative, err := filepath.Rel(s.root, filePath)
	if err != nil {
		return "", err
	}
	var source []byte
	if content != nil {
		source = []byte(*content)
	}
	info, err := s.graph.Hover(ctx, filepath.ToSlash(relative), source, line, column)
	if err != nil || info == nil {
		return "", err
	}
	code := info.Code
	if info.Truncated {
		code += "\n…"
	}
	var result strings.Builder
	fmt.Fprintf(&result, "```%s\n%s\n```\n\n%s · %s:%d", info.Node.Lang, code, info.Node.Kind, info.Node.File, info.Node.StartLine)
	if info.Others > 0 {
		fmt.Fprintf(&result, " · %d more candidates", info.Others)
	}
	return result.String(), nil
}

func (s *Service) graphDocumentSymbols(filePath string, content *string) ([]lsp.DocumentSymbol, []lsp.SymbolInformation, error) {
	source, err := editorSource(filePath, content)
	if err != nil {
		return nil, nil, err
	}
	return documentSymbols(graph.FileSymbols(filepath.Base(filePath), source)), nil, nil
}

func documentSymbols(symbols []*graph.Symbol) []lsp.DocumentSymbol {
	result := make([]lsp.DocumentSymbol, 0, len(symbols))
	for _, symbol := range symbols {
		result = append(result, lsp.DocumentSymbol{
			Name:           symbol.Name,
			Kind:           lsp.SymbolKind(symbolKind(symbol.Kind)),
			Range:          protocolRange(symbol.Range),
			SelectionRange: protocolRange(symbol.NameRange),
			Children:       documentSymbols(symbol.Children),
		})
	}
	return result
}

func (s *Service) graphDocumentHighlights(filePath string, content *string, line, column int) ([]lsp.DocumentHighlight, error) {
	source, err := editorSource(filePath, content)
	if err != nil {
		return nil, err
	}
	ranges := graph.DocumentHighlights(filepath.Base(filePath), source, line, column)
	result := make([]lsp.DocumentHighlight, 0, len(ranges))
	for _, value := range ranges {
		result = append(result, lsp.DocumentHighlight{Range: protocolRange(value), Kind: lsp.DocumentHighlightKindText})
	}
	return result, nil
}

func (s *Service) graphFoldingRanges(filePath string, content *string) ([]lsp.FoldingRange, error) {
	source, err := editorSource(filePath, content)
	if err != nil {
		return nil, err
	}
	ranges := graph.FoldingRanges(filepath.Base(filePath), source)
	result := make([]lsp.FoldingRange, 0, len(ranges))
	for _, value := range ranges {
		start, end := uint32(value.StartCol), uint32(value.EndCol)
		result = append(result, lsp.FoldingRange{
			StartLine:      uint32(value.StartLine),
			StartCharacter: &start,
			EndLine:        uint32(value.EndLine),
			EndCharacter:   &end,
		})
	}
	return result, nil
}

func (s *Service) graphSemanticTokens(filePath string, content *string) ([]SemanticToken, error) {
	source, err := editorSource(filePath, content)
	if err != nil {
		return nil, err
	}
	values := graph.SemanticTokens(filepath.Base(filePath), source)
	result := make([]SemanticToken, 0, len(values))
	for _, value := range values {
		result = append(result, SemanticToken{
			Line:      value.Range.StartLine,
			Character: value.Range.StartCol,
			Length:    value.Range.EndCol - value.Range.StartCol,
			Type:      value.Type,
			Modifiers: value.Modifiers,
		})
	}
	return result, nil
}

func editorSource(filePath string, content *string) ([]byte, error) {
	if content != nil {
		return []byte(*content), nil
	}
	return os.ReadFile(filePath)
}

func protocolRange(value graph.SymRange) lsp.Range {
	return lsp.Range{
		Start: lsp.Position{Line: uint32(value.StartLine), Character: uint32(value.StartCol)},
		End:   lsp.Position{Line: uint32(value.EndLine), Character: uint32(value.EndCol)},
	}
}

func completionKind(kind graph.Kind) int {
	switch kind {
	case graph.KindModule:
		return 9
	case graph.KindClass:
		return 7
	case graph.KindMethod:
		return 2
	case graph.KindConstructor:
		return 4
	case graph.KindInterface:
		return 8
	case graph.KindFunction:
		return 3
	case graph.KindConstant:
		return 21
	case graph.KindType:
		return 22
	default:
		return 6
	}
}

func symbolKind(kind graph.Kind) int {
	switch kind {
	case graph.KindModule:
		return 2
	case graph.KindClass:
		return 5
	case graph.KindMethod:
		return 6
	case graph.KindConstructor:
		return 9
	case graph.KindInterface:
		return 11
	case graph.KindFunction:
		return 12
	case graph.KindConstant:
		return 14
	case graph.KindType:
		return 23
	default:
		return 13
	}
}
