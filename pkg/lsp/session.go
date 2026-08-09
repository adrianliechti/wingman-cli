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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.lsp.dev/jsonrpc2"
)

type Session struct {
	server     Server
	conn       jsonrpc2.Conn
	cmd        *exec.Cmd
	rootURI    string
	workingDir string
	cancelFunc context.CancelFunc

	documentMu sync.Mutex
	mu         sync.Mutex
	documents  map[string]*document
	progress   map[string]bool
	created    time.Time
	pullDiags  bool
	alive      atomic.Bool
	closeOnce  sync.Once
}

// document tracks what the server has been told about a URI and what it has
// reported back. Servers publish diagnostics for files we never opened, so
// "tracked" and "opened" are distinct.
type document struct {
	opened  bool
	sum     uint64
	version int
	saved   bool

	diagnostics []Diagnostic
	published   bool
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
		documents:  make(map[string]*document),
		progress:   make(map[string]bool),
		created:    time.Now(),
	}
	session.alive.Store(true)

	conn := jsonrpc2.NewConn(jsonrpc2.NewStream(&cmdStream{cmd: cmd, stdin: stdin, stdout: stdout}))
	session.conn = conn
	conn.Go(connCtx, session.handle)

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

func (s *Session) handle(ctx context.Context, req *jsonrpc2.Request) (any, error) {
	switch req.Method() {
	case "textDocument/publishDiagnostics":
		var params PublishDiagnosticsParams
		if err := json.Unmarshal(req.Params(), &params); err == nil {
			s.mu.Lock()
			if doc := s.document(params.URI); params.Version == 0 || params.Version >= doc.version {
				doc.diagnostics = params.Diagnostics
				doc.published = true
			}
			s.mu.Unlock()
		}
		return nil, nil
	case "window/workDoneProgress/create":
		return nil, nil
	case "$/progress":
		var params ProgressParams
		if err := json.Unmarshal(req.Params(), &params); err == nil {
			s.applyProgress(params.Token, params.Value.Kind)
		}
		return nil, nil
	}

	if req.IsCall() {
		return nil, jsonrpc2.ErrMethodNotFound
	}
	return nil, nil
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

func (s *Session) OpenedDocURIs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	uris := make([]string, 0, len(s.documents))
	for uri, doc := range s.documents {
		if doc.opened {
			uris = append(uris, uri)
		}
	}
	return uris
}

func (s *Session) Close() {
	s.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _ = s.conn.Call(ctx, "shutdown", nil, nil)
		_ = s.conn.Notify(ctx, "exit", nil)
		_ = s.conn.Close()
		s.cancelFunc()
	})
}

func (s *Session) CallAndAwait(ctx context.Context, method string, params any, result any) error {
	var err error

	for attempt := range maxRetries {
		_, err = s.conn.Call(ctx, method, params, result)
		if err == nil || !isTransientError(err) {
			return err
		}

		delay := retryBaseDelay << attempt
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}

	return err
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

func (s *Session) syncDocument(ctx context.Context, filePath string, content []byte, saved bool) (string, error) {
	s.documentMu.Lock()
	defer s.documentMu.Unlock()

	uri := FileURI(filePath)

	h := fnv.New64a()
	h.Write(content)
	sum := h.Sum64()

	s.mu.Lock()
	doc := s.document(uri)
	opened, unchanged, wasSaved := doc.opened, doc.sum == sum, doc.saved
	version := doc.version + 1
	if opened && !unchanged {
		doc.version = version
		doc.diagnostics = nil
		doc.published = false
	}
	s.mu.Unlock()

	notifySaved := func() error {
		if err := s.conn.Notify(ctx, "textDocument/didSave", DidSaveTextDocumentParams{
			TextDocument: TextDocumentIdentifier{URI: uri},
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
		if err := s.conn.Notify(ctx, "textDocument/didChange", DidChangeTextDocumentParams{
			TextDocument:   VersionedTextDocumentIdentifier{URI: uri, Version: version},
			ContentChanges: []TextDocumentContentChangeEvent{{Text: string(content)}},
		}); err != nil {
			return "", fmt.Errorf("didChange: %w", err)
		}
		if saved {
			if err := notifySaved(); err != nil {
				return "", err
			}
		}

	default:
		if err := s.conn.Notify(ctx, "textDocument/didOpen", DidOpenTextDocumentParams{
			TextDocument: TextDocumentItem{
				URI:        uri,
				LanguageID: s.server.LanguageIDForPath(filePath),
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
	if !opened {
		doc.version = 1
	}
	s.mu.Unlock()

	return uri, nil
}

func (s *Session) publishedDiagnostics(uri string) ([]Diagnostic, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	doc := s.documents[uri]
	if doc == nil {
		return nil, false
	}
	return doc.diagnostics, doc.published
}

func (s *Session) PushSeen(uri string) bool {
	_, published := s.publishedDiagnostics(uri)
	return published
}

func (s *Session) SupportsPullDiagnostics() bool {
	return s.pullDiags
}

func (s *Session) pullDiagnostics(ctx context.Context, uri string) ([]Diagnostic, bool) {
	params := DocumentDiagnosticParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
	}

	callCtx, callCancel := context.WithTimeout(ctx, 5*time.Second)
	defer callCancel()

	var result json.RawMessage
	if err := s.CallAndAwait(callCtx, "textDocument/diagnostic", params, &result); err != nil || result == nil || string(result) == "null" {
		return nil, false
	}

	var report FullDocumentDiagnosticReport
	if err := json.Unmarshal(result, &report); err == nil && report.Kind != "" {
		return report.Items, true
	}

	var diagnostics []Diagnostic
	if err := json.Unmarshal(result, &diagnostics); err == nil {
		return diagnostics, true
	}

	return nil, false
}

// DiagnosticsState reports the diagnostics for uri and whether the server has
// actually produced a result: known=false means the server neither published
// nor answered a pull request, so "no diagnostics" cannot be concluded.
func (s *Session) DiagnosticsState(ctx context.Context, uri string) ([]Diagnostic, bool) {
	published, ok := s.publishedDiagnostics(uri)
	if ok {
		return published, true
	}

	if s.pullDiags {
		if diags, ok := s.pullDiagnostics(ctx, uri); ok {
			return diags, true
		}
	}

	return nil, false
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

func (s *Session) Diagnostics(ctx context.Context, uri string, filePath string) (string, error) {
	diags, known := s.WaitForDiagnostics(ctx, uri, 3*time.Second)
	if !known {
		return "No diagnostics data: the language server did not report results for this file (it may still be analyzing or not support diagnostics). Do not treat this as a clean result.", nil
	}
	if len(diags) == 0 {
		return "No diagnostics found", nil
	}

	return FormatDiagnostics(diags, filePath, s.workingDir), nil
}

type DefLocation struct {
	Path   string
	Line   int
	Column int
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
	position := Position{Line: line, Character: column}
	document := TextDocumentIdentifier{URI: uri}

	var params any = TextDocumentPositionParams{TextDocument: document, Position: position}
	if method == methodReferences {
		params = ReferenceParams{
			TextDocument: document,
			Position:     position,
			Context:      ReferenceContext{IncludeDeclaration: true},
		}
	}

	var result json.RawMessage
	if err := s.CallAndAwait(ctx, method, params, &result); err != nil {
		return nil, err
	}

	return parseLocationResponse(result)
}

func (s *Session) defLocations(ctx context.Context, method, uri string, line, column int) ([]DefLocation, error) {
	locations, err := s.locations(ctx, method, uri, line, column)
	if err != nil {
		return nil, err
	}

	out := make([]DefLocation, 0, len(locations))
	for _, location := range locations {
		out = append(out, DefLocation{
			Path:   uriToPath(location.URI),
			Line:   location.Range.Start.Line,
			Column: location.Range.Start.Character,
		})
	}
	return out, nil
}

func (s *Session) DefinitionLocations(ctx context.Context, uri string, line, column int) ([]DefLocation, error) {
	return s.defLocations(ctx, methodDefinition, uri, line, column)
}

func (s *Session) TypeDefinitionLocations(ctx context.Context, uri string, line, column int) ([]DefLocation, error) {
	return s.defLocations(ctx, methodTypeDefinition, uri, line, column)
}

func (s *Session) ImplementationLocations(ctx context.Context, uri string, line, column int) ([]DefLocation, error) {
	return s.defLocations(ctx, methodImplementation, uri, line, column)
}

func (s *Session) ReferenceLocations(ctx context.Context, uri string, line, column int) ([]DefLocation, error) {
	return s.defLocations(ctx, methodReferences, uri, line, column)
}

func (s *Session) Definition(ctx context.Context, uri string, line, column int) (string, error) {
	locations, err := s.locations(ctx, methodDefinition, uri, line, column)
	if err != nil {
		return "", err
	}
	if len(locations) == 0 {
		return "No definition found", nil
	}

	return formatDefinitions(locations, s.workingDir), nil
}

func (s *Session) References(ctx context.Context, uri string, line, column int) (string, error) {
	return s.formatLocationRequest(ctx, methodReferences, "References", uri, line, column)
}

func (s *Session) Implementation(ctx context.Context, uri string, line, column int) (string, error) {
	return s.formatLocationRequest(ctx, methodImplementation, "Implementations", uri, line, column)
}

func (s *Session) formatLocationRequest(ctx context.Context, method, title, uri string, line, column int) (string, error) {
	locations, err := s.locations(ctx, method, uri, line, column)
	if err != nil {
		return "", err
	}
	if len(locations) == 0 {
		return fmt.Sprintf("No %s found", strings.ToLower(title)), nil
	}

	return formatLocations(title, locations, s.workingDir), nil
}

func (s *Session) Hover(ctx context.Context, uri string, line, column int) (string, error) {
	contents, err := s.HoverInformation(ctx, uri, line, column)
	if err != nil {
		return "", err
	}
	if contents == "" {
		return "No hover information available", nil
	}
	return contents, nil
}

func (s *Session) HoverInformation(ctx context.Context, uri string, line, column int) (string, error) {
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: line, Character: column},
	}

	var result json.RawMessage
	if err := s.CallAndAwait(ctx, "textDocument/hover", params, &result); err != nil {
		return "", err
	}

	var hover HoverResponse
	if err := unmarshalResult(result, &hover); err != nil {
		return "", err
	}

	return hover.Contents.Value, nil
}

func (s *Session) DocumentSymbols(ctx context.Context, uri string, filePath string) (string, error) {
	docSymbols, symInfos, err := s.DocumentSymbolItems(ctx, uri)
	if err != nil {
		return "", err
	}
	if len(symInfos) > 0 {
		return formatSymbolInformations(symInfos, s.workingDir), nil
	}
	if len(docSymbols) > 0 {
		return formatDocumentSymbols(docSymbols, filePath, s.workingDir, 0), nil
	}
	return "No symbols found", nil
}

// DocumentSymbolItems returns the file's symbols in whichever of the two
// documentSymbol response shapes the server chose: a DocumentSymbol tree or a
// flat SymbolInformation list.
func (s *Session) DocumentSymbolItems(ctx context.Context, uri string) ([]DocumentSymbol, []SymbolInformation, error) {
	params := DocumentSymbolParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
	}

	var result json.RawMessage
	if err := s.CallAndAwait(ctx, "textDocument/documentSymbol", params, &result); err != nil {
		return nil, nil, err
	}

	if isNullResult(result) {
		return nil, nil, nil
	}

	var symInfos []SymbolInformation
	if err := json.Unmarshal(result, &symInfos); err == nil && len(symInfos) > 0 && symInfos[0].Location.URI != "" {
		return nil, symInfos, nil
	}

	var docSymbols []DocumentSymbol
	if err := json.Unmarshal(result, &docSymbols); err == nil && len(docSymbols) > 0 {
		return docSymbols, nil, nil
	}

	return nil, nil, nil
}

type symbolCandidate struct {
	name      string
	qualified string
	position  Position
}

// SymbolPosition locates a named symbol in a file, preferring the language
// server's own symbol table and falling back to the first textual occurrence of
// the name's last segment.
func (s *Session) SymbolPosition(ctx context.Context, uri string, name string) (Position, bool) {
	path := uriToPath(uri)

	docSymbols, symInfos, err := s.DocumentSymbolItems(ctx, uri)
	if err == nil {
		var candidates []symbolCandidate
		for _, sym := range symInfos {
			candidates = append(candidates, symbolCandidate{name: sym.Name, qualified: sym.Name, position: sym.Location.Range.Start})
		}
		collectSymbolCandidates(docSymbols, "", &candidates)

		if matched, ok := matchSymbol(candidates, name); ok {
			return snapToIdentifier(path, matched), true
		}
	}

	return PositionOfSymbol(path, symbolLeaf(name))
}

// snapToIdentifier adjusts a candidate position to the symbol's name token:
// SymbolInformation ranges start at the declaration keyword, which servers
// reject as "no identifier" for position-based requests.
func snapToIdentifier(path string, matched symbolCandidate) Position {
	pos := matched.position

	lines, err := readLines(path)
	if err != nil || pos.Line < 0 || pos.Line >= len(lines) {
		return pos
	}

	text := lines[pos.Line]
	leaf := symbolLeaf(matched.name)
	if idx := findWordInLineFrom(text, leaf, runeColFromUTF16(text, pos.Character)); idx >= 0 {
		pos.Character = utf16ColFromRune(text, idx)
	}

	return pos
}

func collectSymbolCandidates(symbols []DocumentSymbol, prefix string, out *[]symbolCandidate) {
	for _, sym := range symbols {
		qualified := sym.Name
		if prefix != "" {
			qualified = prefix + "." + sym.Name
		}
		*out = append(*out, symbolCandidate{name: sym.Name, qualified: qualified, position: sym.SelectionRange.Start})
		collectSymbolCandidates(sym.Children, qualified, out)
	}
}

func matchSymbol(candidates []symbolCandidate, query string) (symbolCandidate, bool) {
	for _, c := range candidates {
		if c.name == query || c.qualified == query {
			return c, true
		}
	}

	if strings.Contains(query, ".") {
		normalized := normalizeSymbol(query)
		for _, c := range candidates {
			if strings.HasSuffix(normalizeSymbol(c.qualified), normalized) || strings.HasSuffix(normalizeSymbol(c.name), normalized) {
				return c, true
			}
		}
	}

	leaf := symbolLeaf(query)
	for _, c := range candidates {
		if symbolLeaf(c.name) == leaf {
			return c, true
		}
	}

	return symbolCandidate{}, false
}

func symbolLeaf(name string) string {
	name = strings.TrimSuffix(name, "()")
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		name = name[idx+1:]
	}
	return strings.Trim(name, "*()")
}

func normalizeSymbol(name string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '(', ')', '*', ' ':
			return -1
		}
		return r
	}, name)
}

func (s *Session) CallHierarchy(ctx context.Context, uri string, line, column int, incoming bool) (string, error) {
	items, err := s.prepareCallHierarchyItems(ctx, uri, line, column)
	if err != nil {
		return "", err
	}

	if len(items) == 0 {
		return "No call hierarchy item found at this position", nil
	}

	var out string
	if incoming {
		out, err = s.incomingCalls(ctx, items[0])
	} else {
		out, err = s.outgoingCalls(ctx, items[0])
	}
	if err != nil {
		return "", err
	}

	if len(items) > 1 {
		out += fmt.Sprintf("(%d call hierarchy items at this position; showing calls for %s)\n", len(items), items[0].Name)
	}

	return out, nil
}

func (s *Session) prepareCallHierarchyItems(ctx context.Context, uri string, line, column int) ([]CallHierarchyItem, error) {
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: line, Character: column},
	}

	var prepareResult json.RawMessage
	if err := s.CallAndAwait(ctx, "textDocument/prepareCallHierarchy", params, &prepareResult); err != nil {
		return nil, err
	}

	return parseCallHierarchyItems(prepareResult)
}

func (s *Session) initialize(ctx context.Context) error {
	params := InitializeParams{
		ProcessID: os.Getpid(),
		RootURI:   s.rootURI,
		Capabilities: ClientCapabilities{
			TextDocument: TextDocumentClientCapabilities{
				Synchronization: TextDocumentSyncClientCapabilities{DidSave: true},
				Hover:           HoverClientCapabilities{ContentFormat: []string{"plaintext", "markdown"}},
				Definition:      DefinitionClientCapabilities{},
				TypeDefinition:  TypeDefinitionClientCapabilities{},
				References:      ReferencesClientCapabilities{},
				Implementation:  ImplementationClientCapabilities{},
				DocumentSymbol:  DocumentSymbolClientCapabilities{},
				Diagnostic:      DiagnosticClientCapabilities{},
				CallHierarchy:   CallHierarchyClientCapabilities{},
			},
			Window: WindowClientCapabilities{WorkDoneProgress: true},
		},
	}

	var result InitializeResult
	if _, err := s.conn.Call(ctx, "initialize", params, &result); err != nil {
		return err
	}
	s.pullDiags = diagnosticProviderEnabled(result.Capabilities.DiagnosticProvider)

	if err := s.conn.Notify(ctx, "initialized", struct{}{}); err != nil {
		return fmt.Errorf("initialized notification: %w", err)
	}

	return nil
}

func diagnosticProviderEnabled(provider json.RawMessage) bool {
	value := strings.TrimSpace(string(provider))
	return value != "" && value != "null" && value != "false"
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

// parseLocationResponse decodes the Location | Location[] | LocationLink[]
// union shared by definition, references and implementation. An empty array is
// a valid "nothing found" answer, not a malformed response.
func parseLocationResponse(data json.RawMessage) ([]Location, error) {
	if isNullResult(data) {
		return nil, nil
	}

	var single Location
	if err := json.Unmarshal(data, &single); err == nil {
		if single.URI == "" {
			return nil, nil
		}
		return []Location{single}, nil
	}

	var locations []Location
	if err := json.Unmarshal(data, &locations); err == nil && (len(locations) == 0 || locations[0].URI != "") {
		return locations, nil
	}

	var links []LocationLink
	if err := json.Unmarshal(data, &links); err == nil {
		locations = make([]Location, 0, len(links))
		for _, link := range links {
			locations = append(locations, Location{URI: link.TargetURI, Range: link.TargetSelectionRange})
		}
		return locations, nil
	}

	return nil, fmt.Errorf("unexpected location response format")
}

func parseCallHierarchyItems(data json.RawMessage) ([]CallHierarchyItem, error) {
	var items []CallHierarchyItem
	if err := unmarshalResult(data, &items); err != nil {
		return nil, err
	}

	return items, nil
}

func isNullResult(data json.RawMessage) bool {
	return len(data) == 0 || string(data) == "null"
}

func unmarshalResult(data json.RawMessage, v any) error {
	if isNullResult(data) {
		return nil
	}
	return json.Unmarshal(data, v)
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
