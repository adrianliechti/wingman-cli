package lsp

import (
	"encoding/json"
	"strings"

	protocol "go.lsp.dev/protocol"
)

type InitializeParams struct {
	ProcessID    int                `json:"processId"`
	RootURI      string             `json:"rootUri"`
	Capabilities ClientCapabilities `json:"capabilities"`
}

type InitializeResult struct {
	Capabilities protocol.ServerCapabilities `json:"capabilities"`
}

type ClientCapabilities struct {
	TextDocument TextDocumentClientCapabilities `json:"textDocument"`
	Window       WindowClientCapabilities       `json:"window"`
}

type WindowClientCapabilities struct {
	WorkDoneProgress bool `json:"workDoneProgress,omitempty"`
}

type TextDocumentClientCapabilities struct {
	Synchronization   TextDocumentSyncClientCapabilities  `json:"synchronization"`
	Completion        CompletionClientCapabilities        `json:"completion"`
	SignatureHelp     SignatureHelpClientCapabilities     `json:"signatureHelp"`
	Hover             HoverClientCapabilities             `json:"hover"`
	Definition        DefinitionClientCapabilities        `json:"definition"`
	TypeDefinition    TypeDefinitionClientCapabilities    `json:"typeDefinition"`
	References        ReferencesClientCapabilities        `json:"references"`
	Implementation    ImplementationClientCapabilities    `json:"implementation"`
	DocumentSymbol    DocumentSymbolClientCapabilities    `json:"documentSymbol"`
	DocumentHighlight DocumentHighlightClientCapabilities `json:"documentHighlight"`
	FoldingRange      FoldingRangeClientCapabilities      `json:"foldingRange"`
	Rename            RenameClientCapabilities            `json:"rename"`
	CodeAction        CodeActionClientCapabilities        `json:"codeAction"`
	Formatting        FormattingClientCapabilities        `json:"formatting"`
	RangeFormatting   FormattingClientCapabilities        `json:"rangeFormatting"`
	OnTypeFormatting  FormattingClientCapabilities        `json:"onTypeFormatting"`
	SemanticTokens    SemanticTokensClientCapabilities    `json:"semanticTokens"`
	InlayHint         InlayHintClientCapabilities         `json:"inlayHint"`
	Diagnostic        DiagnosticClientCapabilities        `json:"diagnostic"`
	CallHierarchy     CallHierarchyClientCapabilities     `json:"callHierarchy"`
}

type TextDocumentSyncClientCapabilities struct {
	DidSave bool `json:"didSave,omitempty"`
}

type CompletionClientCapabilities struct {
	CompletionItem CompletionItemClientCapabilities `json:"completionItem"`
}

type CompletionItemClientCapabilities struct {
	SnippetSupport      bool     `json:"snippetSupport,omitempty"`
	DocumentationFormat []string `json:"documentationFormat,omitempty"`
}

type SignatureHelpClientCapabilities struct {
	SignatureInformation SignatureInformationClientCapabilities `json:"signatureInformation"`
}

type SignatureInformationClientCapabilities struct {
	DocumentationFormat    []string                         `json:"documentationFormat,omitempty"`
	ParameterInformation   ParameterInformationCapabilities `json:"parameterInformation"`
	ActiveParameterSupport bool                             `json:"activeParameterSupport,omitempty"`
}

type ParameterInformationCapabilities struct {
	LabelOffsetSupport bool `json:"labelOffsetSupport,omitempty"`
}

type HoverClientCapabilities struct {
	ContentFormat []string `json:"contentFormat,omitempty"`
}

type DefinitionClientCapabilities struct {
	LinkSupport bool `json:"linkSupport,omitempty"`
}

type TypeDefinitionClientCapabilities struct{}

type ReferencesClientCapabilities struct{}

type ImplementationClientCapabilities struct{}

type DocumentSymbolClientCapabilities struct{}

type DocumentHighlightClientCapabilities struct{}

type FoldingRangeClientCapabilities struct{}

type RenameClientCapabilities struct {
	PrepareSupport bool `json:"prepareSupport,omitempty"`
}

type CodeActionClientCapabilities struct{}

type FormattingClientCapabilities struct{}

type SemanticTokensClientCapabilities struct {
	Requests       SemanticTokensRequests `json:"requests"`
	TokenTypes     []string               `json:"tokenTypes"`
	TokenModifiers []string               `json:"tokenModifiers"`
	Formats        []string               `json:"formats"`
}

type SemanticTokensRequests struct {
	Full  bool `json:"full"`
	Range bool `json:"range"`
}

type InlayHintClientCapabilities struct{}

type DiagnosticClientCapabilities struct{}

type CallHierarchyClientCapabilities struct{}

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

type DidSaveTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type DidCloseTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type PublishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Version     int          `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

type ProgressParams struct {
	Token json.RawMessage `json:"token"`
	Value struct {
		Kind string `json:"kind"`
	} `json:"value"`
}

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

type LocationLink struct {
	TargetURI            string `json:"targetUri"`
	TargetRange          Range  `json:"targetRange"`
	TargetSelectionRange Range  `json:"targetSelectionRange"`
}

type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

type CompletionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	Context      *CompletionContext     `json:"context,omitempty"`
}

type CompletionContext struct {
	TriggerKind      int    `json:"triggerKind"`
	TriggerCharacter string `json:"triggerCharacter,omitempty"`
}

type CompletionItem struct {
	Label               string          `json:"label"`
	Kind                int             `json:"kind,omitempty"`
	Detail              string          `json:"detail,omitempty"`
	Documentation       json.RawMessage `json:"documentation,omitempty"`
	SortText            string          `json:"sortText,omitempty"`
	FilterText          string          `json:"filterText,omitempty"`
	InsertText          string          `json:"insertText,omitempty"`
	InsertTextFormat    int             `json:"insertTextFormat,omitempty"`
	TextEdit            json.RawMessage `json:"textEdit,omitempty"`
	AdditionalTextEdits []TextEdit      `json:"additionalTextEdits,omitempty"`
	Command             *Command        `json:"command,omitempty"`
	CommitCharacters    []string        `json:"commitCharacters,omitempty"`
	Preselect           bool            `json:"preselect,omitempty"`
	Data                json.RawMessage `json:"data,omitempty"`
}

type CompletionList struct {
	IsIncomplete bool             `json:"isIncomplete"`
	Items        []CompletionItem `json:"items"`
}

type Command struct {
	Title     string            `json:"title"`
	Command   string            `json:"command"`
	Arguments []json.RawMessage `json:"arguments,omitempty"`
}

type SignatureHelpParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	Context      *SignatureHelpContext  `json:"context,omitempty"`
}

type SignatureHelpContext struct {
	TriggerKind      int    `json:"triggerKind"`
	TriggerCharacter string `json:"triggerCharacter,omitempty"`
	IsRetrigger      bool   `json:"isRetrigger"`
}

type SignatureHelp struct {
	Signatures      []SignatureInformation `json:"signatures"`
	ActiveSignature int                    `json:"activeSignature,omitempty"`
	ActiveParameter int                    `json:"activeParameter,omitempty"`
}

type SignatureInformation struct {
	Label           string                 `json:"label"`
	Documentation   json.RawMessage        `json:"documentation,omitempty"`
	Parameters      []ParameterInformation `json:"parameters,omitempty"`
	ActiveParameter *int                   `json:"activeParameter,omitempty"`
}

type ParameterInformation struct {
	Label         json.RawMessage `json:"label"`
	Documentation json.RawMessage `json:"documentation,omitempty"`
}

type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

type ReferenceParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	Context      ReferenceContext       `json:"context"`
}

type ReferenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

type HoverResponse struct {
	Contents HoverContents `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

type HoverContents struct {
	Value string
}

func (h *HoverContents) UnmarshalJSON(data []byte) error {

	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		h.Value = s
		return nil
	}

	var obj struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(data, &obj); err == nil && obj.Value != "" {
		h.Value = obj.Value
		return nil
	}

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

	h.Value = string(data)
	return nil
}

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

type DocumentHighlightParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

type DocumentHighlight struct {
	Range Range `json:"range"`
	Kind  int   `json:"kind,omitempty"`
}

type FoldingRangeParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type FoldingRange struct {
	StartLine      int    `json:"startLine"`
	StartCharacter *int   `json:"startCharacter,omitempty"`
	EndLine        int    `json:"endLine"`
	EndCharacter   *int   `json:"endCharacter,omitempty"`
	Kind           string `json:"kind,omitempty"`
}

type SemanticTokensParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type SemanticTokens struct {
	Data []uint32 `json:"data"`
}

type SemanticTokensLegend struct {
	TokenTypes     []string `json:"tokenTypes"`
	TokenModifiers []string `json:"tokenModifiers"`
}

type SemanticTokensOptions struct {
	Legend SemanticTokensLegend `json:"legend"`
}

type SemanticToken struct {
	Line      int
	Character int
	Length    int
	Type      string
	Modifiers []string
}

type SymbolInformation struct {
	Name     string   `json:"name"`
	Kind     int      `json:"kind"`
	Location Location `json:"location"`
}

type WorkspaceSymbolParams struct {
	Query string `json:"query"`
}

type WorkspaceSymbol struct {
	Name     string `json:"name"`
	Kind     int    `json:"kind"`
	Location struct {
		URI   string `json:"uri"`
		Range *Range `json:"range,omitempty"`
	} `json:"location"`
}

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
