package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"os/exec"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/fileuri"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	lspuri "go.lsp.dev/uri"

	"github.com/adrianliechti/wingman-agent/internal/tooling"
)

type Session struct {
	server     Server
	conn       jsonrpc2.Conn
	rpc        protocol.Server
	cmd        *exec.Cmd
	rootURI    string
	cancelFunc context.CancelFunc

	documentMu   sync.Mutex
	mu           sync.Mutex
	documents    map[string]*document
	progress     map[string]bool
	created      time.Time
	pullDiags    bool
	capabilities protocol.ServerCapabilities
	alive        atomic.Bool
	closeOnce    sync.Once
}

// document tracks what the server has been told about a URI and what it has
// reported back. Servers publish diagnostics for files we never opened, so
// "tracked" and "opened" are distinct.
type document struct {
	opened  bool
	sum     uint64
	version int
	saved   bool
	content string

	wireDiagnostics []protocol.Diagnostic
	published       bool
}

func (s *Session) document(uri string) *document {
	doc := s.documents[uri]
	if doc == nil {
		doc = &document{}
		s.documents[uri] = doc
	}
	return doc
}

const startupTimeout = 30 * time.Second

func connect(ctx context.Context, workingDir string, server Server) (*Session, error) {
	cmd := exec.Command(server.Command, server.Args...)
	cmd.Dir = workingDir
	cmd.Env = tooling.Environment(server.Command, os.Environ())
	cmd.Stderr = io.Discard

	setSysProcAttr(cmd)

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

	connCtx, cancel := context.WithCancel(context.Background())

	session := &Session{
		server:     server,
		cmd:        cmd,
		rootURI:    fileuri.FromPath(workingDir),
		cancelFunc: cancel,
		documents:  make(map[string]*document),
		progress:   make(map[string]bool),
		created:    time.Now(),
	}
	session.alive.Store(true)

	_, conn, rpc := protocol.NewClient(
		connCtx,
		&sessionClient{session: session},
		jsonrpc2.NewStream(&cmdStream{cmd: cmd, stdin: stdin, stdout: stdout}),
	)
	session.conn = conn
	session.rpc = rpc

	go func() {
		<-conn.Done()
		session.alive.Store(false)
	}()

	initCtx, initCancel := context.WithTimeout(ctx, startupTimeout)
	defer initCancel()

	if err := session.initialize(initCtx); err != nil {
		_ = conn.Close()
		cancel()
		return nil, fmt.Errorf("initialize: %w", err)
	}

	return session, nil
}

type sessionClient struct {
	protocol.UnimplementedClient
	session *Session
}

func (c *sessionClient) PublishDiagnostics(_ context.Context, params *protocol.PublishDiagnosticsParams) error {
	version, _ := params.Version.Get()
	uri := params.URI.String()
	c.session.mu.Lock()
	if doc := c.session.document(uri); version == 0 || int(version) >= doc.version {
		doc.wireDiagnostics = slices.Clone(params.Diagnostics)
		doc.published = true
	}
	c.session.mu.Unlock()
	return nil
}

func (c *sessionClient) WorkDoneProgressCreate(context.Context, *protocol.WorkDoneProgressCreateParams) error {
	return nil
}

func (c *sessionClient) Progress(_ context.Context, params *protocol.ProgressParams) error {
	var value struct {
		Kind string `json:"kind"`
	}
	if err := protocol.Unmarshal(params.Value, &value); err == nil {
		token, _ := protocol.Marshal(params.Token)
		c.session.applyProgress(token, value.Kind)
	}
	return nil
}

func (s *Session) IsAlive() bool {
	return s.alive.Load()
}

func (s *Session) applyProgress(token json.RawMessage, kind string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch kind {
	case "begin":
		s.progress[string(token)] = true
	case "end":
		delete(s.progress, string(token))
	}
}

// Analyzing reports whether the server has announced ongoing background work
// (indexing, loading packages) via work-done progress.
func (s *Session) Analyzing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.progress) > 0
}

func (s *Session) Age() time.Duration {
	return time.Since(s.created)
}

type openDocument struct {
	Path    string
	Content string
	Saved   bool
}

func (s *Session) openedDocuments() []openDocument {
	s.mu.Lock()
	defer s.mu.Unlock()

	documents := make([]openDocument, 0, len(s.documents))
	for uri, doc := range s.documents {
		if doc.opened {
			path, _ := fileuri.Path(uri)
			documents = append(documents, openDocument{
				Path:    path,
				Content: doc.content,
				Saved:   doc.saved,
			})
		}
	}
	return documents
}

func (s *Session) Close() {
	s.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_ = s.rpc.Shutdown(ctx)
		_ = s.rpc.Exit(ctx)
		_ = s.conn.Close()
		s.cancelFunc()
	})
}

func retryRPC[T any](ctx context.Context, call func() (T, error)) (T, error) {
	var result T
	var err error
	for attempt := range maxRetries {
		result, err = call()
		if err == nil || !isTransientError(err) {
			return result, err
		}
		delay := retryBaseDelay << attempt
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(delay):
		}
	}
	return result, err
}

const maxRetries = 3

var retryBaseDelay = 500 * time.Millisecond

const (
	codeServerCancelled  = -32802
	codeContentModified  = -32801
	codeRequestCancelled = -32800
)

func isTransientError(err error) bool {
	var wireErr *jsonrpc2.Error
	if !errors.As(err, &wireErr) {
		return false
	}

	return wireErr.Code == codeServerCancelled ||
		wireErr.Code == codeContentModified ||
		wireErr.Code == codeRequestCancelled
}

func (s *Session) OpenDocument(ctx context.Context, filePath string) (string, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	return s.syncDocument(ctx, filePath, content, true)
}

// SyncDocument updates the language server with an editor buffer without
// claiming that the buffer has been saved to disk.
func (s *Session) SyncDocument(ctx context.Context, filePath, content string) (string, error) {
	return s.syncDocument(ctx, filePath, []byte(content), false)
}

// SaveDocument synchronizes an editor buffer and explicitly tells the server
// that the same content has been persisted to disk.
func (s *Session) SaveDocument(ctx context.Context, filePath, content string) (string, error) {
	return s.syncDocument(ctx, filePath, []byte(content), true)
}

// CloseDocument ends the lifetime of an editor-owned document. A session can
// also know diagnostics for unopened files, so only documents explicitly
// opened by syncDocument receive didClose.
func (s *Session) CloseDocument(ctx context.Context, filePath string) error {
	s.documentMu.Lock()
	defer s.documentMu.Unlock()

	uri := fileuri.FromPath(filePath)
	s.mu.Lock()
	doc := s.documents[uri]
	opened := doc != nil && doc.opened
	s.mu.Unlock()
	if !opened {
		return nil
	}
	if err := s.rpc.DidClose(ctx, &protocol.DidCloseTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: lspuri.MustParse(uri)},
	}); err != nil {
		return fmt.Errorf("didClose: %w", err)
	}

	s.mu.Lock()
	delete(s.documents, uri)
	s.mu.Unlock()
	return nil
}

func (s *Session) DocumentContent(filePath string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.documents[fileuri.FromPath(filePath)]
	if doc == nil || !doc.opened {
		return "", false
	}
	return doc.content, true
}

func (s *Session) syncDocument(ctx context.Context, filePath string, content []byte, saved bool) (string, error) {
	s.documentMu.Lock()
	defer s.documentMu.Unlock()

	uri := fileuri.FromPath(filePath)

	h := fnv.New64a()
	h.Write(content)
	sum := h.Sum64()

	s.mu.Lock()
	doc := s.document(uri)
	opened, unchanged, wasSaved := doc.opened, doc.sum == sum, doc.saved
	version := doc.version + 1
	if opened && !unchanged {
		doc.version = version
		doc.wireDiagnostics = nil
		doc.published = false
	}
	s.mu.Unlock()

	notifySaved := func() error {
		if err := s.rpc.DidSave(ctx, &protocol.DidSaveTextDocumentParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: lspuri.MustParse(uri)},
		}); err != nil {
			return fmt.Errorf("didSave: %w", err)
		}
		return nil
	}

	switch {
	case opened && unchanged:
		if !saved || wasSaved {
			return uri, nil
		}
		if err := notifySaved(); err != nil {
			return "", err
		}

	case opened:
		if err := s.rpc.DidChange(ctx, &protocol.DidChangeTextDocumentParams{
			TextDocument: protocol.VersionedTextDocumentIdentifier{
				TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: lspuri.MustParse(uri)},
				Version:                int32(version),
			},
			ContentChanges: []protocol.TextDocumentContentChangeEvent{
				&protocol.TextDocumentContentChangeWholeDocument{Text: string(content)},
			},
		}); err != nil {
			return "", fmt.Errorf("didChange: %w", err)
		}
		if saved {
			if err := notifySaved(); err != nil {
				return "", err
			}
		}

	default:
		if err := s.rpc.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{
			TextDocument: protocol.TextDocumentItem{
				URI:        lspuri.MustParse(uri),
				LanguageID: protocol.LanguageKind(s.server.LanguageIDForPath(filePath)),
				Version:    1,
				Text:       string(content),
			},
		}); err != nil {
			return "", fmt.Errorf("didOpen: %w", err)
		}
	}

	s.mu.Lock()
	doc = s.document(uri)
	doc.opened = true
	doc.sum = sum
	doc.saved = saved
	doc.content = string(content)
	if !opened {
		doc.version = 1
	}
	s.mu.Unlock()

	return uri, nil
}

func (s *Session) publishedProtocolDiagnostics(uri string) ([]protocol.Diagnostic, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	doc := s.documents[uri]
	if doc == nil {
		return nil, false
	}
	return slices.Clone(doc.wireDiagnostics), doc.published
}

func (s *Session) PushSeen(uri string) bool {
	_, published := s.publishedProtocolDiagnostics(uri)
	return published
}

func (s *Session) SupportsPullDiagnostics() bool {
	return s.pullDiags
}

func (s *Session) pullProtocolDiagnostics(ctx context.Context, uri string) ([]protocol.Diagnostic, bool) {
	callCtx, callCancel := context.WithTimeout(ctx, 5*time.Second)
	defer callCancel()
	report, err := retryRPC(callCtx, func() (protocol.DocumentDiagnosticReport, error) {
		return s.rpc.Diagnostic(callCtx, &protocol.DocumentDiagnosticParams{TextDocument: wireDocument(uri)})
	})
	if err != nil {
		return nil, false
	}
	switch report := report.(type) {
	case *protocol.RelatedFullDocumentDiagnosticReport:
		return report.Items, true
	case *protocol.RelatedUnchangedDocumentDiagnosticReport:
		return s.publishedProtocolDiagnostics(uri)
	}
	return nil, false
}

func (s *Session) protocolDiagnostics(ctx context.Context, uri string) ([]protocol.Diagnostic, bool) {
	if published, ok := s.publishedProtocolDiagnostics(uri); ok {
		return published, true
	}
	if s.pullDiags {
		return s.pullProtocolDiagnostics(ctx, uri)
	}
	return nil, false
}

// DiagnosticsState reports the diagnostics for uri and whether the server has
// actually produced a result: known=false means the server neither published
// nor answered a pull request, so "no diagnostics" cannot be concluded.
func (s *Session) DiagnosticsState(ctx context.Context, uri string) ([]Diagnostic, bool) {
	return s.protocolDiagnostics(ctx, uri)
}

func (s *Session) WaitForDiagnostics(ctx context.Context, uri string, timeout time.Duration) ([]Diagnostic, bool) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	for {
		if diags, known := s.DiagnosticsState(waitCtx, uri); known {
			return diags, true
		}

		select {
		case <-waitCtx.Done():
			return nil, false
		case <-ticker.C:
		}
	}
}

const (
	methodDefinition     = "textDocument/definition"
	methodTypeDefinition = "textDocument/typeDefinition"
	methodImplementation = "textDocument/implementation"
	methodReferences     = "textDocument/references"
)

// locations runs any of the position-based navigation requests, all of which
// answer with Location | Location[] | LocationLink[].
func (s *Session) locations(ctx context.Context, method, uri string, line, column int) ([]Location, error) {
	documentPosition := protocol.TextDocumentPositionParams{
		TextDocument: wireDocument(uri),
		Position:     wirePosition(line, column),
	}
	switch method {
	case methodDefinition:
		result, err := retryRPC(ctx, func() (protocol.DefinitionResult, error) {
			return s.rpc.Definition(ctx, &protocol.DefinitionParams{TextDocumentPositionParams: documentPosition})
		})
		return locationsFromDefinition(result), err
	case methodTypeDefinition:
		result, err := retryRPC(ctx, func() (protocol.DefinitionResult, error) {
			return s.rpc.TypeDefinition(ctx, &protocol.TypeDefinitionParams{TextDocumentPositionParams: documentPosition})
		})
		return locationsFromDefinition(result), err
	case methodImplementation:
		result, err := retryRPC(ctx, func() (protocol.DefinitionResult, error) {
			return s.rpc.Implementation(ctx, &protocol.ImplementationParams{TextDocumentPositionParams: documentPosition})
		})
		return locationsFromDefinition(result), err
	case methodReferences:
		result, err := retryRPC(ctx, func() ([]protocol.Location, error) {
			return s.rpc.References(ctx, &protocol.ReferenceParams{
				TextDocumentPositionParams: documentPosition,
				Context:                    protocol.ReferenceContext{IncludeDeclaration: true},
			})
		})
		return []Location(result), err
	default:
		return nil, fmt.Errorf("unsupported location method %q", method)
	}
}

func (s *Session) DefinitionLocations(ctx context.Context, uri string, line, column int) ([]Location, error) {
	return s.locations(ctx, methodDefinition, uri, line, column)
}

func (s *Session) TypeDefinitionLocations(ctx context.Context, uri string, line, column int) ([]Location, error) {
	return s.locations(ctx, methodTypeDefinition, uri, line, column)
}

func (s *Session) ImplementationLocations(ctx context.Context, uri string, line, column int) ([]Location, error) {
	return s.locations(ctx, methodImplementation, uri, line, column)
}

func (s *Session) ReferenceLocations(ctx context.Context, uri string, line, column int) ([]Location, error) {
	return s.locations(ctx, methodReferences, uri, line, column)
}

func (s *Session) Hover(ctx context.Context, uri string, line, column int) (*Hover, error) {
	return retryRPC(ctx, func() (*protocol.Hover, error) {
		return s.rpc.Hover(ctx, &protocol.HoverParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: wireDocument(uri),
			Position:     wirePosition(line, column),
		}})
	})
}

func (s *Session) CompletionItems(ctx context.Context, uri string, line, column int, completionContext *CompletionContext) (CompletionList, error) {
	params := &protocol.CompletionParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
		TextDocument: wireDocument(uri),
		Position:     wirePosition(line, column),
	}}
	if completionContext != nil {
		params.Context = *completionContext
	}
	result, err := retryRPC(ctx, func() (protocol.CompletionResult, error) {
		return s.rpc.Completion(ctx, params)
	})
	if err != nil {
		return CompletionList{}, err
	}
	switch result := result.(type) {
	case protocol.CompletionItemSlice:
		return protocol.CompletionList{Items: []protocol.CompletionItem(result)}, nil
	case *protocol.CompletionList:
		return *result, nil
	default:
		return protocol.CompletionList{}, nil
	}
}

func (s *Session) ResolveCompletionItem(ctx context.Context, item CompletionItem) (CompletionItem, error) {
	result, err := retryRPC(ctx, func() (*protocol.CompletionItem, error) {
		return s.rpc.CompletionResolve(ctx, &item)
	})
	if err != nil {
		return CompletionItem{}, err
	}
	if result == nil {
		return item, nil
	}
	return *result, nil
}

func (s *Session) SignatureHelp(ctx context.Context, uri string, line, column int, signatureContext *SignatureHelpContext) (*SignatureHelp, error) {
	params := &protocol.SignatureHelpParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
		TextDocument: wireDocument(uri),
		Position:     wirePosition(line, column),
	}}
	if signatureContext != nil {
		params.Context = *signatureContext
	}
	help, err := retryRPC(ctx, func() (*protocol.SignatureHelp, error) {
		return s.rpc.SignatureHelp(ctx, params)
	})
	if err != nil {
		return nil, err
	}
	return help, nil
}

func (s *Session) DocumentSymbols(ctx context.Context, uri string) (DocumentSymbolResult, error) {
	return retryRPC(ctx, func() (protocol.DocumentSymbolResult, error) {
		return s.rpc.DocumentSymbol(ctx, &protocol.DocumentSymbolParams{TextDocument: wireDocument(uri)})
	})
}

func (s *Session) WorkspaceSymbols(ctx context.Context, query string) (WorkspaceSymbolResult, error) {
	return retryRPC(ctx, func() (protocol.WorkspaceSymbolResult, error) {
		return s.rpc.Symbols(ctx, &protocol.WorkspaceSymbolParams{Query: query})
	})
}

func (s *Session) DocumentHighlights(ctx context.Context, uri string, line, column int) ([]DocumentHighlight, error) {
	result, err := retryRPC(ctx, func() ([]protocol.DocumentHighlight, error) {
		return s.rpc.DocumentHighlight(ctx, &protocol.DocumentHighlightParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: wireDocument(uri),
			Position:     wirePosition(line, column),
		}})
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Session) FoldingRanges(ctx context.Context, uri string) ([]FoldingRange, error) {
	result, err := retryRPC(ctx, func() ([]protocol.FoldingRange, error) {
		return s.rpc.FoldingRanges(ctx, &protocol.FoldingRangeParams{TextDocument: wireDocument(uri)})
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Session) SemanticTokens(ctx context.Context, uri string) (*SemanticTokens, error) {
	return retryRPC(ctx, func() (*protocol.SemanticTokens, error) {
		return s.rpc.SemanticTokensFull(ctx, &protocol.SemanticTokensParams{TextDocument: wireDocument(uri)})
	})
}

func (s *Session) PrepareCallHierarchy(ctx context.Context, uri string, line, column int) ([]CallHierarchyItem, error) {
	return retryRPC(ctx, func() ([]protocol.CallHierarchyItem, error) {
		return s.rpc.PrepareCallHierarchy(ctx, &protocol.CallHierarchyPrepareParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: wireDocument(uri),
			Position:     wirePosition(line, column),
		}})
	})
}

func (s *Session) initialize(ctx context.Context) error {
	processID := int32(os.Getpid())
	rootURI := lspuri.MustParse(s.rootURI)
	params := &protocol.InitializeParams{
		ProcessID:             &processID,
		RootURI:               &rootURI,
		InitializationOptions: protocol.LSPAny(s.server.InitializationOptions),
		Capabilities: protocol.ClientCapabilities{
			Workspace: &protocol.WorkspaceClientCapabilities{
				WorkspaceEdit: &protocol.WorkspaceEditClientCapabilities{
					DocumentChanges:         pointer(true),
					FailureHandling:         protocol.FailureHandlingKindTextOnlyTransactional,
					NormalizesLineEndings:   pointer(false),
					ChangeAnnotationSupport: &protocol.ChangeAnnotationsSupportOptions{},
				},
			},
			TextDocument: &protocol.TextDocumentClientCapabilities{
				Synchronization: &protocol.TextDocumentSyncClientCapabilities{DidSave: pointer(true)},
				Completion: &protocol.CompletionClientCapabilities{
					ContextSupport: pointer(true),
					CompletionItem: &protocol.ClientCompletionItemOptions{
						SnippetSupport:          pointer(true),
						CommitCharactersSupport: pointer(true),
						PreselectSupport:        pointer(true),
						InsertReplaceSupport:    pointer(true),
						DocumentationFormat:     []protocol.MarkupKind{protocol.MarkupKindMarkdown, protocol.MarkupKindPlainText},
						ResolveSupport: protocol.ClientCompletionItemResolveOptions{
							Properties: []string{"documentation", "detail", "additionalTextEdits", "command"},
						},
					},
				},
				SignatureHelp: &protocol.SignatureHelpClientCapabilities{
					ContextSupport: pointer(true),
					SignatureInformation: &protocol.ClientSignatureInformationOptions{
						DocumentationFormat:    []protocol.MarkupKind{protocol.MarkupKindMarkdown, protocol.MarkupKindPlainText},
						ParameterInformation:   &protocol.ClientSignatureParameterInformationOptions{LabelOffsetSupport: pointer(true)},
						ActiveParameterSupport: pointer(true),
					},
				},
				Hover:             &protocol.HoverClientCapabilities{ContentFormat: []protocol.MarkupKind{protocol.MarkupKindMarkdown, protocol.MarkupKindPlainText}},
				Definition:        &protocol.DefinitionClientCapabilities{LinkSupport: pointer(true)},
				TypeDefinition:    &protocol.TypeDefinitionClientCapabilities{},
				References:        &protocol.ReferenceClientCapabilities{},
				Implementation:    &protocol.ImplementationClientCapabilities{},
				DocumentSymbol:    &protocol.DocumentSymbolClientCapabilities{HierarchicalDocumentSymbolSupport: pointer(true)},
				DocumentHighlight: &protocol.DocumentHighlightClientCapabilities{},
				FoldingRange:      &protocol.FoldingRangeClientCapabilities{},
				Rename:            &protocol.RenameClientCapabilities{PrepareSupport: pointer(true), HonorsChangeAnnotations: pointer(true)},
				CodeAction: &protocol.CodeActionClientCapabilities{
					IsPreferredSupport:      pointer(true),
					DisabledSupport:         pointer(true),
					DataSupport:             pointer(true),
					HonorsChangeAnnotations: pointer(true),
					CodeActionLiteralSupport: protocol.ClientCodeActionLiteralOptions{
						CodeActionKind: protocol.ClientCodeActionKindOptions{},
					},
					ResolveSupport: protocol.ClientCodeActionResolveOptions{Properties: []string{"edit", "command"}},
				},
				Formatting:       &protocol.DocumentFormattingClientCapabilities{},
				RangeFormatting:  &protocol.DocumentRangeFormattingClientCapabilities{},
				OnTypeFormatting: &protocol.DocumentOnTypeFormattingClientCapabilities{},
				SemanticTokens: protocol.SemanticTokensClientCapabilities{
					Requests: protocol.ClientSemanticTokensRequestOptions{
						Full:  protocol.Boolean(true),
						Range: protocol.Boolean(true),
					},
					TokenTypes: []string{
						"namespace", "type", "class", "enum", "interface", "struct",
						"typeParameter", "parameter", "variable", "property", "enumMember",
						"event", "function", "method", "macro", "label", "comment", "string",
						"keyword", "number", "regexp", "operator", "decorator",
					},
					TokenModifiers: []string{
						"declaration", "definition", "readonly", "static", "deprecated",
						"abstract", "async", "modification", "documentation", "defaultLibrary",
					},
					Formats: []protocol.TokenFormat{protocol.TokenFormatRelative},
				},
				InlayHint:     &protocol.InlayHintClientCapabilities{},
				Diagnostic:    &protocol.DiagnosticClientCapabilities{},
				CallHierarchy: &protocol.CallHierarchyClientCapabilities{},
			},
			Window: &protocol.WindowClientCapabilities{WorkDoneProgress: pointer(true)},
		},
	}

	result, err := s.rpc.Initialize(ctx, params)
	if err != nil {
		return err
	}
	s.pullDiags = result.Capabilities.DiagnosticProvider != nil
	s.capabilities = result.Capabilities

	if err := s.rpc.Initialized(ctx, &protocol.InitializedParams{}); err != nil {
		return fmt.Errorf("initialized notification: %w", err)
	}

	return nil
}

func pointer[T any](value T) *T { return &value }

func (s *Session) Capabilities() protocol.ServerCapabilities {
	return s.capabilities
}

func (s *Session) IncomingCalls(ctx context.Context, item CallHierarchyItem) ([]CallHierarchyIncomingCall, error) {
	return retryRPC(ctx, func() ([]protocol.CallHierarchyIncomingCall, error) {
		return s.rpc.IncomingCalls(ctx, &protocol.CallHierarchyIncomingCallsParams{Item: item})
	})
}

func (s *Session) OutgoingCalls(ctx context.Context, item CallHierarchyItem) ([]CallHierarchyOutgoingCall, error) {
	return retryRPC(ctx, func() ([]protocol.CallHierarchyOutgoingCall, error) {
		return s.rpc.OutgoingCalls(ctx, &protocol.CallHierarchyOutgoingCallsParams{Item: item})
	})
}

type cmdStream struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func (c *cmdStream) Read(p []byte) (int, error) {
	return c.stdout.Read(p)
}

func (c *cmdStream) Write(p []byte) (int, error) {
	return c.stdin.Write(p)
}

func (c *cmdStream) Close() error {
	c.stdin.Close()
	c.stdout.Close()

	if c.cmd.Process != nil {
		c.cmd.Process.Kill()
	}

	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()

	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		return fmt.Errorf("timeout waiting for LSP process to exit")
	}
}
