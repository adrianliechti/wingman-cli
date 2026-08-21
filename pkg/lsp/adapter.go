package lsp

import (
	"go.lsp.dev/protocol"
	lspuri "go.lsp.dev/uri"
)

func wireDocument(uri string) protocol.TextDocumentIdentifier {
	return protocol.TextDocumentIdentifier{URI: lspuri.MustParse(uri)}
}

func wirePosition(line, character int) protocol.Position {
	return protocol.Position{Line: uint32(max(line, 0)), Character: uint32(max(character, 0))}
}

func locationsFromDefinition(result protocol.DefinitionResult) []Location {
	switch result := result.(type) {
	case *protocol.Location:
		return []Location{*result}
	case protocol.LocationSlice:
		return []Location(result)
	case protocol.DefinitionLinkSlice:
		locations := make([]Location, 0, len(result))
		for _, link := range result {
			locations = append(locations, Location{
				URI:   link.TargetURI,
				Range: link.TargetSelectionRange,
			})
		}
		return locations
	default:
		return nil
	}
}
