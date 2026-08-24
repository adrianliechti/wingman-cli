package lsp

import (
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

type Position = protocol.Position
type Range = protocol.Range
type Location = protocol.Location
type LocationURIOnly = protocol.LocationUriOnly
type DocumentURI = uri.URI

type CompletionContext = protocol.CompletionContext
type CompletionTriggerKind = protocol.CompletionTriggerKind
type CompletionItem = protocol.CompletionItem
type CompletionList = protocol.CompletionList
type CompletionItemKind = protocol.CompletionItemKind
type SignatureHelpContext = protocol.SignatureHelpContext
type SignatureHelpTriggerKind = protocol.SignatureHelpTriggerKind
type SignatureHelp = protocol.SignatureHelp
type SignatureInformation = protocol.SignatureInformation
type ServerCapabilities = protocol.ServerCapabilities

type PrepareRenameResult = protocol.PrepareRenameResult
type RenameOptions = protocol.RenameOptions
type WorkspaceEdit = protocol.WorkspaceEdit
type CodeActionKind = protocol.CodeActionKind
type CodeActionTriggerKind = protocol.CodeActionTriggerKind
type CommandOrCodeAction = protocol.CommandOrCodeAction
type CodeAction = protocol.CodeAction
type CodeActionOptions = protocol.CodeActionOptions
type Command = protocol.Command
type LSPAny = protocol.LSPAny
type FormattingOptions = protocol.FormattingOptions
type TextEdit = protocol.TextEdit
type InlayHint = protocol.InlayHint
type TextDocumentEdit = protocol.TextDocumentEdit
type CreateFile = protocol.CreateFile
type RenameFile = protocol.RenameFile
type DeleteFile = protocol.DeleteFile
type Boolean = protocol.Boolean
type OptionalValue[T any] = protocol.Optional[T]

type DocumentSymbol = protocol.DocumentSymbol
type SymbolInformation = protocol.SymbolInformation
type DocumentHighlight = protocol.DocumentHighlight
type FoldingRange = protocol.FoldingRange
type SemanticTokens = protocol.SemanticTokens
type SemanticTokensLegend = protocol.SemanticTokensLegend
type SemanticTokensOptions = protocol.SemanticTokensOptions
type SemanticTokensRegistrationOptions = protocol.SemanticTokensRegistrationOptions
type WorkspaceSymbol = protocol.WorkspaceSymbol
type CallHierarchyItem = protocol.CallHierarchyItem
type CallHierarchyIncomingCall = protocol.CallHierarchyIncomingCall
type CallHierarchyOutgoingCall = protocol.CallHierarchyOutgoingCall
type Diagnostic = protocol.Diagnostic
type DiagnosticSeverity = protocol.DiagnosticSeverity
type String = protocol.String
type MarkupContent = protocol.MarkupContent
type Hover = protocol.Hover
type MarkedStringWithLanguage = protocol.MarkedStringWithLanguage
type MarkedStringSlice = protocol.MarkedStringSlice
type DocumentSymbolResult = protocol.DocumentSymbolResult
type DocumentSymbolSlice = protocol.DocumentSymbolSlice
type SymbolInformationSlice = protocol.SymbolInformationSlice
type WorkspaceSymbolResult = protocol.WorkspaceSymbolResult
type WorkspaceSymbolSlice = protocol.WorkspaceSymbolSlice

const (
	DiagnosticSeverityError       = protocol.DiagnosticSeverityError
	DiagnosticSeverityWarning     = protocol.DiagnosticSeverityWarning
	DiagnosticSeverityInformation = protocol.DiagnosticSeverityInformation
	DiagnosticSeverityHint        = protocol.DiagnosticSeverityHint
)

const DocumentHighlightKindText = protocol.DocumentHighlightKindText

type SymbolKind = protocol.SymbolKind

const (
	SymbolKindFile          = protocol.SymbolKindFile
	SymbolKindModule        = protocol.SymbolKindModule
	SymbolKindNamespace     = protocol.SymbolKindNamespace
	SymbolKindPackage       = protocol.SymbolKindPackage
	SymbolKindClass         = protocol.SymbolKindClass
	SymbolKindMethod        = protocol.SymbolKindMethod
	SymbolKindProperty      = protocol.SymbolKindProperty
	SymbolKindField         = protocol.SymbolKindField
	SymbolKindConstructor   = protocol.SymbolKindConstructor
	SymbolKindEnum          = protocol.SymbolKindEnum
	SymbolKindInterface     = protocol.SymbolKindInterface
	SymbolKindFunction      = protocol.SymbolKindFunction
	SymbolKindVariable      = protocol.SymbolKindVariable
	SymbolKindConstant      = protocol.SymbolKindConstant
	SymbolKindString        = protocol.SymbolKindString
	SymbolKindNumber        = protocol.SymbolKindNumber
	SymbolKindBoolean       = protocol.SymbolKindBoolean
	SymbolKindArray         = protocol.SymbolKindArray
	SymbolKindObject        = protocol.SymbolKindObject
	SymbolKindKey           = protocol.SymbolKindKey
	SymbolKindNull          = protocol.SymbolKindNull
	SymbolKindEnumMember    = protocol.SymbolKindEnumMember
	SymbolKindStruct        = protocol.SymbolKindStruct
	SymbolKindEvent         = protocol.SymbolKindEvent
	SymbolKindOperator      = protocol.SymbolKindOperator
	SymbolKindTypeParameter = protocol.SymbolKindTypeParameter
)

func Optional[T any](value T) OptionalValue[T] { return protocol.NewOptional(value) }

// CapabilityEnabled handles the boolean-or-options shape used by many LSP
// server capabilities. A non-nil options value enables the capability.
func CapabilityEnabled(value any) bool {
	if value == nil {
		return false
	}
	switch enabled := value.(type) {
	case protocol.Boolean:
		return bool(enabled)
	case bool:
		return enabled
	default:
		return true
	}
}

func Marshal(value any) ([]byte, error) { return protocol.Marshal(value) }

func Unmarshal(data []byte, value any) error { return protocol.Unmarshal(data, value) }

func ParseURI(value string) (DocumentURI, error) { return uri.Parse(value) }
