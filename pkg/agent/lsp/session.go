package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/lsp/jsonrpc2"
)

// Session represents a connected LSP server session.
type Session struct {
	server     Server
	conn       *jsonrpc2.Connection
	cmd        *exec.Cmd
	rootURI    string
	workingDir string
	cancelFunc context.CancelFunc

	docVersion int64 // atomic counter for document versions

	openedDocs map[string]struct{}
	mu         sync.Mutex

	// Push-based diagnostics from textDocument/publishDiagnostics notifications.
	pushDiags   map[string][]Diagnostic // keyed by URI
	pushDiagsMu sync.Mutex
}

func connect(ctx context.Context, workingDir string, server Server) (*Session, error) {
	cmd := exec.Command(server.Command, server.Args...)
	cmd.Dir = workingDir
	cmd.Env = os.Environ()
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
		rootURI:    FileURI(workingDir),
		workingDir: workingDir,
		cancelFunc: cancel,
		openedDocs: make(map[string]struct{}),
		pushDiags:  make(map[string][]Diagnostic),
	}

	framer := jsonrpc2.HeaderFramer()
	conn := jsonrpc2.NewConnection(connCtx, jsonrpc2.ConnectionConfig{
		Reader: framer.Reader(stdout),
		Writer: framer.Writer(stdin),
		Closer: &cmdCloser{cmd: cmd, stdin: stdin, stdout: stdout},
		Bind: func(c *jsonrpc2.Connection) jsonrpc2.Handler {
			return jsonrpc2.HandlerFunc(func(ctx context.Context, req *jsonrpc2.Request) (any, error) {
				if req.Method == "textDocument/publishDiagnostics" {
					var params PublishDiagnosticsParams
					if err := json.Unmarshal(req.Params, &params); err == nil {
						session.pushDiagsMu.Lock()
						session.pushDiags[params.URI] = params.Diagnostics
						session.pushDiagsMu.Unlock()
					}
					return nil, nil
				}
				return nil, jsonrpc2.ErrNotHandled
			})
		},
	})
	session.conn = conn

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

	call := s.conn.Call(ctx, "shutdown", nil)
	call.Await(ctx, nil)
	s.conn.Notify(ctx, "exit", nil)
	s.cancelFunc()
}

// CallAndAwait invokes an LSP method and waits for the result.
func (s *Session) CallAndAwait(ctx context.Context, method string, params any, result any) error {
	call := s.conn.Call(ctx, method, params)
	return call.Await(ctx, result)
}

// OpenDocument opens a document in the LSP server, syncing content if already open.
func (s *Session) OpenDocument(ctx context.Context, filePath string) (string, error) {
	uri := FileURI(filePath)

	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	s.mu.Lock()
	_, alreadyOpen := s.openedDocs[uri]
	s.mu.Unlock()

	if alreadyOpen {
		changeParams := DidChangeTextDocumentParams{
			TextDocument: VersionedTextDocumentIdentifier{
				URI:     uri,
				Version: int(atomic.AddInt64(&s.docVersion, 1)),
			},
			ContentChanges: []TextDocumentContentChangeEvent{{Text: string(content)}},
		}

		if err := s.conn.Notify(ctx, "textDocument/didChange", changeParams); err != nil {
			return "", fmt.Errorf("didChange: %w", err)
		}

		// Send didSave — many LSP servers only trigger full diagnostics on save
		s.conn.Notify(ctx, "textDocument/didSave", DidSaveTextDocumentParams{
			TextDocument: TextDocumentIdentifier{URI: uri},
		})

		return uri, nil
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

	s.mu.Lock()
	s.openedDocs[uri] = struct{}{}
	s.mu.Unlock()

	return uri, nil
}

// PushDiagnostics returns any diagnostics received via publishDiagnostics for the URI.
func (s *Session) PushDiagnostics(uri string) []Diagnostic {
	s.pushDiagsMu.Lock()
	diags := s.pushDiags[uri]
	s.pushDiagsMu.Unlock()
	return diags
}

// ClearPushDiagnostics removes cached push diagnostics for a URI,
// so that fresh diagnostics will be collected after the next change.
func (s *Session) ClearPushDiagnostics(uri string) {
	s.pushDiagsMu.Lock()
	delete(s.pushDiags, uri)
	s.pushDiagsMu.Unlock()
}

// CollectDiagnostics retrieves diagnostics for a single document URI.
// It first checks push-based diagnostics (from publishDiagnostics notifications),
// then falls back to pull-based diagnostics (textDocument/diagnostic request).
func (s *Session) CollectDiagnostics(ctx context.Context, uri string) []Diagnostic {
	// Check push-based diagnostics first (event-driven, most reliable)
	if diags := s.PushDiagnostics(uri); len(diags) > 0 {
		return diags
	}

	// Fall back to pull-based diagnostics
	params := DocumentDiagnosticParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
	}

	callCtx, callCancel := context.WithTimeout(ctx, 5*time.Second)
	defer callCancel()

	var result json.RawMessage
	if err := s.CallAndAwait(callCtx, "textDocument/diagnostic", params, &result); err != nil || result == nil || string(result) == "null" {
		return nil
	}

	var report FullDocumentDiagnosticReport
	if err := json.Unmarshal(result, &report); err == nil && len(report.Items) > 0 {
		return report.Items
	}

	var diagnostics []Diagnostic
	if err := json.Unmarshal(result, &diagnostics); err == nil {
		return diagnostics
	}

	return nil
}

// WaitForDiagnostics waits for diagnostics until results appear or the context expires.
// It checks both push-based (publishDiagnostics notifications) and pull-based sources.
func (s *Session) WaitForDiagnostics(ctx context.Context, uri string) []Diagnostic {
	// First attempt immediately.
	if diags := s.CollectDiagnostics(ctx, uri); len(diags) > 0 {
		return diags
	}

	// Poll with a short interval, giving the server time to analyze.
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if diags := s.CollectDiagnostics(ctx, uri); len(diags) > 0 {
				return diags
			}
		}
	}
}

// Diagnostics returns formatted diagnostics for a single file.
func (s *Session) Diagnostics(ctx context.Context, uri string, filePath string) (string, error) {
	diags := s.CollectDiagnostics(ctx, uri)
	if len(diags) == 0 {
		return "No diagnostics found", nil
	}

	return FormatDiagnostics(diags, filePath, s.workingDir), nil
}

// Definition returns the definition location(s) for the symbol at the given position.
func (s *Session) Definition(ctx context.Context, uri string, line, column int) (string, error) {
	return s.locationOp(ctx, "textDocument/definition", "Definition", uri, line, column)
}

// References returns all references to the symbol at the given position.
func (s *Session) References(ctx context.Context, uri string, line, column int) (string, error) {
	params := ReferenceParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: line - 1, Character: column - 1},
		Context:      ReferenceContext{IncludeDeclaration: true},
	}

	var result json.RawMessage
	if err := s.CallAndAwait(ctx, "textDocument/references", params, &result); err != nil {
		return "", err
	}

	locations, err := parseLocationResponse(result)
	if err != nil {
		return "", err
	}

	if len(locations) == 0 {
		return "No references found", nil
	}

	return formatLocations("References", locations, s.workingDir), nil
}

// Implementation returns the implementation location(s) for the symbol at the given position.
func (s *Session) Implementation(ctx context.Context, uri string, line, column int) (string, error) {
	return s.locationOp(ctx, "textDocument/implementation", "Implementations", uri, line, column)
}

// Hover returns hover information for the symbol at the given position.
func (s *Session) Hover(ctx context.Context, uri string, line, column int) (string, error) {
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: line - 1, Character: column - 1},
	}

	var result json.RawMessage
	if err := s.CallAndAwait(ctx, "textDocument/hover", params, &result); err != nil {
		return "", err
	}

	if result == nil || string(result) == "null" {
		return "No hover information available", nil
	}

	var hover HoverResponse
	if err := json.Unmarshal(result, &hover); err != nil {
		return "", err
	}

	if hover.Contents.Value == "" {
		return "No hover information available", nil
	}

	return hover.Contents.Value, nil
}

// DocumentSymbols returns the symbols in a document.
func (s *Session) DocumentSymbols(ctx context.Context, uri string, filePath string) (string, error) {
	params := DocumentSymbolParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
	}

	var result json.RawMessage
	if err := s.CallAndAwait(ctx, "textDocument/documentSymbol", params, &result); err != nil {
		return "", err
	}

	if result == nil || string(result) == "null" {
		return "No symbols found", nil
	}

	// Try SymbolInformation[] first — check for location.uri which is unique to it
	var symInfos []SymbolInformation
	if err := json.Unmarshal(result, &symInfos); err == nil && len(symInfos) > 0 && symInfos[0].Location.URI != "" {
		return formatSymbolInformations(symInfos, s.workingDir), nil
	}

	// Fall back to DocumentSymbol[] (hierarchical, has selectionRange but no location)
	var docSymbols []DocumentSymbol
	if err := json.Unmarshal(result, &docSymbols); err == nil && len(docSymbols) > 0 {
		return formatDocumentSymbols(docSymbols, filePath, s.workingDir, 0), nil
	}

	return "No symbols found", nil
}

// CallHierarchy returns incoming or outgoing calls for the symbol at the given position.
func (s *Session) CallHierarchy(ctx context.Context, uri string, line, column int, incoming bool) (string, error) {
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: line - 1, Character: column - 1},
	}

	var prepareResult json.RawMessage
	if err := s.CallAndAwait(ctx, "textDocument/prepareCallHierarchy", params, &prepareResult); err != nil {
		return "", err
	}

	items, err := parseCallHierarchyItems(prepareResult)
	if err != nil || len(items) == 0 {
		return "No call hierarchy item found at this position", nil
	}

	if incoming {
		return s.incomingCalls(ctx, items[0])
	}
	return s.outgoingCalls(ctx, items[0])
}

// --- private helpers ---

func (s *Session) initialize(ctx context.Context) error {
	params := InitializeParams{
		ProcessID: os.Getpid(),
		RootURI:   s.rootURI,
		Capabilities: ClientCapabilities{
			TextDocument: TextDocumentClientCapabilities{
				Synchronization: TextDocumentSyncClientCapabilities{DidSave: true},
				Hover:           HoverClientCapabilities{ContentFormat: []string{"plaintext", "markdown"}},
				Definition:      DefinitionClientCapabilities{},
				References:      ReferencesClientCapabilities{},
				Implementation:  ImplementationClientCapabilities{},
				DocumentSymbol:  DocumentSymbolClientCapabilities{},
				Diagnostic:      DiagnosticClientCapabilities{},
				CallHierarchy:   CallHierarchyClientCapabilities{},
			},
		},
	}

	var result json.RawMessage
	call := s.conn.Call(ctx, "initialize", params)
	if err := call.Await(ctx, &result); err != nil {
		return err
	}

	if err := s.conn.Notify(ctx, "initialized", struct{}{}); err != nil {
		return fmt.Errorf("initialized notification: %w", err)
	}

	return nil
}

func (s *Session) locationOp(ctx context.Context, method, title, uri string, line, column int) (string, error) {
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: line - 1, Character: column - 1},
	}

	var result json.RawMessage
	if err := s.CallAndAwait(ctx, method, params, &result); err != nil {
		return "", err
	}

	locations, err := parseLocationResponse(result)
	if err != nil {
		return "", err
	}

	if len(locations) == 0 {
		return fmt.Sprintf("No %s found", strings.ToLower(title)), nil
	}

	return formatLocations(title, locations, s.workingDir), nil
}

func (s *Session) incomingCalls(ctx context.Context, item CallHierarchyItem) (string, error) {
	var result json.RawMessage
	if err := s.CallAndAwait(ctx, "callHierarchy/incomingCalls", CallHierarchyIncomingCallsParams{Item: item}, &result); err != nil {
		return "", err
	}

	var calls []CallHierarchyIncomingCall
	if err := unmarshalResult(result, &calls); err != nil {
		return "", err
	}

	if len(calls) == 0 {
		return "No incoming calls found", nil
	}

	return formatIncomingCalls(calls, s.workingDir), nil
}

func (s *Session) outgoingCalls(ctx context.Context, item CallHierarchyItem) (string, error) {
	var result json.RawMessage
	if err := s.CallAndAwait(ctx, "callHierarchy/outgoingCalls", CallHierarchyOutgoingCallsParams{Item: item}, &result); err != nil {
		return "", err
	}

	var calls []CallHierarchyOutgoingCall
	if err := unmarshalResult(result, &calls); err != nil {
		return "", err
	}

	if len(calls) == 0 {
		return "No outgoing calls found", nil
	}

	return formatOutgoingCalls(calls, s.workingDir), nil
}

func parseLocationResponse(data json.RawMessage) ([]Location, error) {
	if data == nil || string(data) == "null" {
		return nil, nil
	}

	var loc Location
	if err := json.Unmarshal(data, &loc); err == nil && loc.URI != "" {
		return []Location{loc}, nil
	}

	var locs []Location
	if err := json.Unmarshal(data, &locs); err == nil && len(locs) > 0 && locs[0].URI != "" {
		return locs, nil
	}
	locs = nil

	var links []struct {
		TargetURI            string `json:"targetUri"`
		TargetRange          Range  `json:"targetRange"`
		TargetSelectionRange Range  `json:"targetSelectionRange"`
	}
	if err := json.Unmarshal(data, &links); err == nil && len(links) > 0 && links[0].TargetURI != "" {
		for _, link := range links {
			locs = append(locs, Location{URI: link.TargetURI, Range: link.TargetSelectionRange})
		}
		return locs, nil
	}

	return nil, fmt.Errorf("unexpected location response format")
}

func parseCallHierarchyItems(data json.RawMessage) ([]CallHierarchyItem, error) {
	if data == nil || string(data) == "null" {
		return nil, nil
	}

	var items []CallHierarchyItem
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}

	return items, nil
}

func unmarshalResult(data json.RawMessage, v any) error {
	if data == nil || string(data) == "null" {
		return nil
	}
	return json.Unmarshal(data, v)
}

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

	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()

	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		return fmt.Errorf("timeout waiting for LSP process to exit")
	}
}
