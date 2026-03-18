package lsp

import (
	"encoding/json"
	"strings"
)

// LSP Protocol Types

// Initialize

type InitializeParams struct {
	ProcessID    int                `json:"processId"`
	RootURI      string             `json:"rootUri"`
	Capabilities ClientCapabilities `json:"capabilities"`
}

type ClientCapabilities struct {
	TextDocument TextDocumentClientCapabilities `json:"textDocument"`
}

type TextDocumentClientCapabilities struct {
	Synchronization TextDocumentSyncClientCapabilities `json:"synchronization"`
	Hover           HoverClientCapabilities            `json:"hover"`
	Definition      DefinitionClientCapabilities       `json:"definition"`
	References      ReferencesClientCapabilities       `json:"references"`
	Implementation  ImplementationClientCapabilities   `json:"implementation"`
	DocumentSymbol  DocumentSymbolClientCapabilities   `json:"documentSymbol"`
	Diagnostic      DiagnosticClientCapabilities       `json:"diagnostic"`
	CallHierarchy   CallHierarchyClientCapabilities    `json:"callHierarchy"`
}

type TextDocumentSyncClientCapabilities struct {
	DidSave bool `json:"didSave,omitempty"`
}

type HoverClientCapabilities struct {
	ContentFormat []string `json:"contentFormat,omitempty"`
}

type DefinitionClientCapabilities struct {
	LinkSupport bool `json:"linkSupport,omitempty"`
}

type ReferencesClientCapabilities struct{}

type ImplementationClientCapabilities struct{}

type DocumentSymbolClientCapabilities struct{}

type DiagnosticClientCapabilities struct{}

type CallHierarchyClientCapabilities struct{}

// Text Document

type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

type VersionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

type DidChangeTextDocumentParams struct {
	TextDocument   VersionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

type TextDocumentContentChangeEvent struct {
	Text string `json:"text"`
}

// Primitives

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// Position-based Operations

type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

type ReferenceParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	Context      ReferenceContext       `json:"context"`
}

type ReferenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

// Hover

type Hover struct {
	Contents HoverContents `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

// HoverContents handles the multiple formats that LSP servers may return:
// MarkupContent { kind, value }, a plain string, or MarkedString { language, value }.
type HoverContents struct {
	Value string
}

func (h *HoverContents) UnmarshalJSON(data []byte) error {
	// Try plain string first (some servers like clangd)
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		h.Value = s
		return nil
	}

	// Try MarkupContent / MarkedString { kind/language, value }
	var obj struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(data, &obj); err == nil && obj.Value != "" {
		h.Value = obj.Value
		return nil
	}

	// Try MarkedString[] array
	var arr []json.RawMessage
	if err := json.Unmarshal(data, &arr); err == nil {
		var parts []string
		for _, item := range arr {
			var str string
			if err := json.Unmarshal(item, &str); err == nil {
				parts = append(parts, str)
				continue
			}
			var ms struct {
				Value string `json:"value"`
			}
			if err := json.Unmarshal(item, &ms); err == nil {
				parts = append(parts, ms.Value)
			}
		}
		h.Value = strings.Join(parts, "\n")
		return nil
	}

	// Fallback: use raw string
	h.Value = string(data)
	return nil
}

type MarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// Diagnostics

type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity,omitempty"`
	Code     any    `json:"code,omitempty"`
	Source   string `json:"source,omitempty"`
	Message  string `json:"message"`
}

const (
	DiagnosticSeverityError       = 1
	DiagnosticSeverityWarning     = 2
	DiagnosticSeverityInformation = 3
	DiagnosticSeverityHint        = 4
)

type DocumentDiagnosticParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type FullDocumentDiagnosticReport struct {
	Kind  string       `json:"kind"`
	Items []Diagnostic `json:"items"`
}

// Document Symbols

type DocumentSymbolParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           int              `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

type SymbolInformation struct {
	Name     string   `json:"name"`
	Kind     int      `json:"kind"`
	Location Location `json:"location"`
}

type WorkspaceSymbolParams struct {
	Query string `json:"query"`
}

// WorkspaceSymbol is the newer response type for workspace/symbol (since 3.17).
// Unlike SymbolInformation, its location range may be omitted.
type WorkspaceSymbol struct {
	Name     string `json:"name"`
	Kind     int    `json:"kind"`
	Location struct {
		URI   string `json:"uri"`
		Range *Range `json:"range,omitempty"`
	} `json:"location"`
}

// Call Hierarchy

type CallHierarchyItem struct {
	Name           string          `json:"name"`
	Kind           int             `json:"kind"`
	Detail         string          `json:"detail,omitempty"`
	URI            string          `json:"uri"`
	Range          Range           `json:"range"`
	SelectionRange Range           `json:"selectionRange"`
	Data           json.RawMessage `json:"data,omitempty"`
}

type CallHierarchyIncomingCallsParams struct {
	Item CallHierarchyItem `json:"item"`
}

type CallHierarchyOutgoingCallsParams struct {
	Item CallHierarchyItem `json:"item"`
}

type CallHierarchyIncomingCall struct {
	From       CallHierarchyItem `json:"from"`
	FromRanges []Range           `json:"fromRanges"`
}

type CallHierarchyOutgoingCall struct {
	To         CallHierarchyItem `json:"to"`
	FromRanges []Range           `json:"fromRanges"`
}
