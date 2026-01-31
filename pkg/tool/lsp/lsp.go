package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-cli/pkg/tool/lsp/jsonrpc2"
)

// Session represents a connected LSP server session.
type Session struct {
	server     Server
	conn       *jsonrpc2.Connection
	cmd        *exec.Cmd
	rootURI    string
	cancelFunc context.CancelFunc
}

// Connect starts and initializes an LSP server for the given file.
// It walks up the directory tree from the file to find an appropriate server.
func Connect(ctx context.Context, workingDir string, filePath string) (*Session, error) {
	server := FindServer(workingDir, filePath)
	if server == nil {
		return nil, fmt.Errorf("no LSP server found for file: %s", filePath)
	}
	return connect(ctx, workingDir, *server)
}

func connect(ctx context.Context, workingDir string, server Server) (*Session, error) {
	// Create command
	cmd := exec.Command(server.Command, server.Args...)
	cmd.Dir = workingDir
	cmd.Env = os.Environ()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return nil, fmt.Errorf("start command: %w", err)
	}

	// Create JSON-RPC connection
	connCtx, cancel := context.WithCancel(context.Background())

	framer := jsonrpc2.HeaderFramer()
	reader := framer.Reader(stdout)
	writer := framer.Writer(stdin)

	conn := jsonrpc2.NewConnection(connCtx, jsonrpc2.ConnectionConfig{
		Reader: reader,
		Writer: writer,
		Closer: &cmdCloser{cmd: cmd, stdin: stdin, stdout: stdout},
		Bind: func(c *jsonrpc2.Connection) jsonrpc2.Handler {
			return jsonrpc2.HandlerFunc(func(ctx context.Context, req *jsonrpc2.Request) (any, error) {
				// Handle server-initiated requests (e.g., window/showMessage)
				return nil, jsonrpc2.ErrNotHandled
			})
		},
	})

	session := &Session{
		server:     server,
		conn:       conn,
		cmd:        cmd,
		rootURI:    fileURI(workingDir),
		cancelFunc: cancel,
	}

	// Initialize the LSP server
	initCtx, initCancel := context.WithTimeout(ctx, 30*time.Second)
	defer initCancel()

	if err := session.initialize(initCtx); err != nil {
		cancel()
		cmd.Process.Kill()
		cmd.Wait()
		return nil, fmt.Errorf("initialize: %w", err)
	}

	return session, nil
}

// Close shuts down the LSP server connection.
func (s *Session) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Send shutdown request
	call := s.conn.Call(ctx, "shutdown", nil)
	call.Await(ctx, nil)

	// Send exit notification
	s.conn.Notify(ctx, "exit", nil)

	s.cancelFunc()
}

// initialize performs the LSP initialize handshake.
func (s *Session) initialize(ctx context.Context) error {
	params := InitializeParams{
		ProcessID: os.Getpid(),
		RootURI:   s.rootURI,
		Capabilities: ClientCapabilities{
			TextDocument: TextDocumentClientCapabilities{
				Synchronization: TextDocumentSyncClientCapabilities{
					DidSave: true,
				},
				Hover: HoverClientCapabilities{
					ContentFormat: []string{"plaintext", "markdown"},
				},
				Definition: DefinitionClientCapabilities{
					LinkSupport: false,
				},
				Implementation: ImplementationClientCapabilities{
					LinkSupport: false,
				},
				References:     ReferencesClientCapabilities{},
				DocumentSymbol: DocumentSymbolClientCapabilities{},
				CallHierarchy:  CallHierarchyClientCapabilities{},
				PublishDiagnostics: PublishDiagnosticsClientCapabilities{
					RelatedInformation: true,
				},
				Rename: RenameClientCapabilities{
					PrepareSupport: true,
				},
				CodeAction: CodeActionClientCapabilities{
					CodeActionLiteralSupport: &CodeActionLiteralSupport{
						CodeActionKind: CodeActionKindSupport{
							ValueSet: []string{
								"quickfix",
								"refactor",
								"refactor.extract",
								"refactor.inline",
								"refactor.rewrite",
								"source",
								"source.organizeImports",
								"source.fixAll",
							},
						},
					},
				},
				Diagnostic: DiagnosticClientCapabilities{},
			},
			Workspace: WorkspaceClientCapabilities{
				Symbol: WorkspaceSymbolClientCapabilities{},
			},
		},
	}

	var result json.RawMessage
	call := s.conn.Call(ctx, "initialize", params)
	if err := call.Await(ctx, &result); err != nil {
		return err
	}

	// Send initialized notification
	if err := s.conn.Notify(ctx, "initialized", struct{}{}); err != nil {
		return fmt.Errorf("initialized notification: %w", err)
	}

	return nil
}

// OpenDocument opens a document in the LSP server.
func (s *Session) OpenDocument(ctx context.Context, filePath string) (string, error) {
	uri := fileURI(filePath)

	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	params := DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{
			URI:        uri,
			LanguageID: s.server.LanguageID,
			Version:    1,
			Text:       string(content),
		},
	}

	if err := s.conn.Notify(ctx, "textDocument/didOpen", params); err != nil {
		return "", fmt.Errorf("didOpen: %w", err)
	}

	return uri, nil
}

// cmdCloser wraps exec.Cmd for io.Closer interface.
type cmdCloser struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func (c *cmdCloser) Close() error {
	c.stdin.Close()
	c.stdout.Close()
	if c.cmd.Process != nil {
		c.cmd.Process.Kill()
	}
	return c.cmd.Wait()
}

// fileURI converts a file path to a file:// URI.
func fileURI(path string) string {
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}
	return "file://" + absPath
}

// uriToPath converts a file:// URI to a file path.
func uriToPath(uri string) string {
	if strings.HasPrefix(uri, "file://") {
		return strings.TrimPrefix(uri, "file://")
	}
	return uri
}

// LSP Protocol Types

type InitializeParams struct {
	ProcessID    int                `json:"processId"`
	RootURI      string             `json:"rootUri"`
	Capabilities ClientCapabilities `json:"capabilities"`
}

type ClientCapabilities struct {
	TextDocument TextDocumentClientCapabilities `json:"textDocument,omitempty"`
	Workspace    WorkspaceClientCapabilities    `json:"workspace,omitempty"`
}

type WorkspaceClientCapabilities struct {
	Symbol WorkspaceSymbolClientCapabilities `json:"symbol,omitempty"`
}

type WorkspaceSymbolClientCapabilities struct{}

type TextDocumentClientCapabilities struct {
	Synchronization    TextDocumentSyncClientCapabilities   `json:"synchronization,omitempty"`
	Hover              HoverClientCapabilities              `json:"hover,omitempty"`
	Definition         DefinitionClientCapabilities         `json:"definition,omitempty"`
	Implementation     ImplementationClientCapabilities     `json:"implementation,omitempty"`
	References         ReferencesClientCapabilities         `json:"references,omitempty"`
	DocumentSymbol     DocumentSymbolClientCapabilities     `json:"documentSymbol,omitempty"`
	CallHierarchy      CallHierarchyClientCapabilities      `json:"callHierarchy,omitempty"`
	PublishDiagnostics PublishDiagnosticsClientCapabilities `json:"publishDiagnostics,omitempty"`
	Rename             RenameClientCapabilities             `json:"rename,omitempty"`
	CodeAction         CodeActionClientCapabilities         `json:"codeAction,omitempty"`
	Diagnostic         DiagnosticClientCapabilities         `json:"diagnostic,omitempty"`
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

type ImplementationClientCapabilities struct {
	LinkSupport bool `json:"linkSupport,omitempty"`
}

type ReferencesClientCapabilities struct{}

type DocumentSymbolClientCapabilities struct{}

type CallHierarchyClientCapabilities struct{}

type PublishDiagnosticsClientCapabilities struct {
	RelatedInformation bool `json:"relatedInformation,omitempty"`
}

type RenameClientCapabilities struct {
	PrepareSupport bool `json:"prepareSupport,omitempty"`
}

type CodeActionClientCapabilities struct {
	CodeActionLiteralSupport *CodeActionLiteralSupport `json:"codeActionLiteralSupport,omitempty"`
}

type CodeActionLiteralSupport struct {
	CodeActionKind CodeActionKindSupport `json:"codeActionKind"`
}

type CodeActionKindSupport struct {
	ValueSet []string `json:"valueSet"`
}

type DiagnosticClientCapabilities struct {
}

type TextDocumentIdentifier struct {
	URI string `json:"uri"`
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

type DocumentSymbolParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type SymbolInformation struct {
	Name          string   `json:"name"`
	Kind          int      `json:"kind"`
	Location      Location `json:"location"`
	ContainerName string   `json:"containerName,omitempty"`
}

type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           int              `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

type Hover struct {
	Contents MarkupContent `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

type MarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type WorkspaceSymbolParams struct {
	Query string `json:"query"`
}

// CallHierarchyItem represents a call hierarchy item.
type CallHierarchyItem struct {
	Name           string `json:"name"`
	Kind           int    `json:"kind"`
	Tags           []int  `json:"tags,omitempty"`
	Detail         string `json:"detail,omitempty"`
	URI            string `json:"uri"`
	Range          Range  `json:"range"`
	SelectionRange Range  `json:"selectionRange"`
	Data           any    `json:"data,omitempty"`
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

// Diagnostic represents a diagnostic, such as a compiler error or warning.
type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity,omitempty"`
	Code     any    `json:"code,omitempty"`
	Source   string `json:"source,omitempty"`
	Message  string `json:"message"`
}

// DiagnosticSeverity values.
const (
	DiagnosticSeverityError       = 1
	DiagnosticSeverityWarning     = 2
	DiagnosticSeverityInformation = 3
	DiagnosticSeverityHint        = 4
)

// DocumentDiagnosticParams for textDocument/diagnostic request.
type DocumentDiagnosticParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// FullDocumentDiagnosticReport for diagnostic response.
type FullDocumentDiagnosticReport struct {
	Kind  string       `json:"kind"`
	Items []Diagnostic `json:"items"`
}

// RenameParams for textDocument/rename request.
type RenameParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	NewName      string                 `json:"newName"`
}

// WorkspaceEdit represents changes to many resources.
type WorkspaceEdit struct {
	Changes map[string][]TextEdit `json:"changes,omitempty"`
}

// TextEdit represents a textual edit applicable to a text document.
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

// CodeActionParams for textDocument/codeAction request.
type CodeActionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
	Context      CodeActionContext      `json:"context"`
}

// CodeActionContext contains additional diagnostic information.
type CodeActionContext struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
	Only        []string     `json:"only,omitempty"`
}

// CodeAction represents a code action (quick fix, refactoring, etc.).
type CodeAction struct {
	Title       string         `json:"title"`
	Kind        string         `json:"kind,omitempty"`
	Diagnostics []Diagnostic   `json:"diagnostics,omitempty"`
	IsPreferred bool           `json:"isPreferred,omitempty"`
	Edit        *WorkspaceEdit `json:"edit,omitempty"`
	Command     *Command       `json:"command,omitempty"`
}

// Command represents a reference to a command.
type Command struct {
	Title     string `json:"title"`
	Command   string `json:"command"`
	Arguments []any  `json:"arguments,omitempty"`
}

// symbolKindName converts LSP SymbolKind to a human-readable name.
func symbolKindName(kind int) string {
	kinds := map[int]string{
		1:  "File",
		2:  "Module",
		3:  "Namespace",
		4:  "Package",
		5:  "Class",
		6:  "Method",
		7:  "Property",
		8:  "Field",
		9:  "Constructor",
		10: "Enum",
		11: "Interface",
		12: "Function",
		13: "Variable",
		14: "Constant",
		15: "String",
		16: "Number",
		17: "Boolean",
		18: "Array",
		19: "Object",
		20: "Key",
		21: "Null",
		22: "EnumMember",
		23: "Struct",
		24: "Event",
		25: "Operator",
		26: "TypeParameter",
	}
	if name, ok := kinds[kind]; ok {
		return name
	}
	return "Unknown"
}

// diagnosticSeverityName converts DiagnosticSeverity to a human-readable name.
func diagnosticSeverityName(severity int) string {
	switch severity {
	case DiagnosticSeverityError:
		return "Error"
	case DiagnosticSeverityWarning:
		return "Warning"
	case DiagnosticSeverityInformation:
		return "Info"
	case DiagnosticSeverityHint:
		return "Hint"
	default:
		return "Unknown"
	}
}
