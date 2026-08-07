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
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/lsp/jsonrpc2"
)

type Session struct {
	server     Server
	conn       *jsonrpc2.Connection
	cmd        *exec.Cmd
	rootURI    string
	workingDir string
	cancelFunc context.CancelFunc

	mu          sync.Mutex
	openedDocs  map[string]uint64
	docVersions map[string]int
	pushDiags   map[string][]Diagnostic
	pushSeen    map[string]bool
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
		openedDocs:  make(map[string]uint64),
		docVersions: make(map[string]int),
		pushDiags:   make(map[string][]Diagnostic),
		pushSeen:    make(map[string]bool),
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
						session.mu.Lock()
						if params.Version == 0 || params.Version >= session.docVersions[params.URI] {
							session.pushDiags[params.URI] = params.Diagnostics
							session.pushSeen[params.URI] = true
						}
						session.mu.Unlock()
					}
					return nil, nil
				}
				return nil, jsonrpc2.ErrNotHandled
			})
		},
	})
	session.conn = conn

	initCtx, initCancel := context.WithTimeout(ctx, startupTimeout)
	defer initCancel()

	if err := session.initialize(initCtx); err != nil {
		cancel()
		cmd.Process.Kill()
		cmd.Wait()
		return nil, fmt.Errorf("initialize: %w", err)
	}

	return session, nil
}

func (s *Session) IsAlive() bool {
	return s.cmd.ProcessState == nil
}

func (s *Session) OpenedDocURIs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	uris := make([]string, 0, len(s.openedDocs))
	for uri := range s.openedDocs {
		uris = append(uris, uri)
	}
	return uris
}

func (s *Session) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	call := s.conn.Call(ctx, "shutdown", nil)
	call.Await(ctx, nil)
	s.conn.Notify(ctx, "exit", nil)
	s.cancelFunc()
}

func (s *Session) CallAndAwait(ctx context.Context, method string, params any, result any) error {
	var err error

	for attempt := range maxRetries {
		call := s.conn.Call(ctx, method, params)
		err = call.Await(ctx, result)
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

func isTransientError(err error) bool {
	var wireErr *jsonrpc2.WireError
	if !errors.As(err, &wireErr) {
		return false
	}

	switch wireErr.Code {
	case -32801:
		return true
	case -32800:
		return true
	case -32802:
		return true
	default:
		return false
	}
}

func (s *Session) OpenDocument(ctx context.Context, filePath string) (string, error) {
	uri := FileURI(filePath)

	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	h := fnv.New64a()
	h.Write(content)
	sum := h.Sum64()

	s.mu.Lock()
	prev, alreadyOpen := s.openedDocs[uri]
	s.mu.Unlock()

	if alreadyOpen {
		if prev == sum {
			return uri, nil
		}

		s.mu.Lock()
		version := s.docVersions[uri] + 1
		s.docVersions[uri] = version
		delete(s.pushDiags, uri)
		delete(s.pushSeen, uri)
		s.mu.Unlock()

		changeParams := DidChangeTextDocumentParams{
			TextDocument: VersionedTextDocumentIdentifier{
				URI:     uri,
				Version: version,
			},
			ContentChanges: []TextDocumentContentChangeEvent{{Text: string(content)}},
		}

		if err := s.conn.Notify(ctx, "textDocument/didChange", changeParams); err != nil {
			return "", fmt.Errorf("didChange: %w", err)
		}

		s.conn.Notify(ctx, "textDocument/didSave", DidSaveTextDocumentParams{
			TextDocument: TextDocumentIdentifier{URI: uri},
		})

		s.mu.Lock()
		s.openedDocs[uri] = sum
		s.mu.Unlock()

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
	s.openedDocs[uri] = sum
	s.docVersions[uri] = 1
	s.mu.Unlock()

	return uri, nil
}

func (s *Session) PushDiagnostics(uri string) []Diagnostic {
	diags, _ := s.pushedDiagnostics(uri)
	return diags
}

func (s *Session) pushedDiagnostics(uri string) ([]Diagnostic, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pushDiags[uri], s.pushSeen[uri]
}

func (s *Session) PushSeen(uri string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pushSeen[uri]
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
	pushed, seen := s.pushedDiagnostics(uri)
	if seen && len(pushed) > 0 {
		return pushed, true
	}

	if diags, ok := s.pullDiagnostics(ctx, uri); ok {
		return diags, true
	}

	if seen {
		return pushed, true
	}

	return nil, false
}

func (s *Session) CollectDiagnostics(ctx context.Context, uri string) []Diagnostic {
	diags, _ := s.DiagnosticsState(ctx, uri)
	return diags
}

func (s *Session) WaitForDiagnostics(ctx context.Context, uri string, timeout time.Duration) ([]Diagnostic, bool) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	for {
		if diags, known := s.DiagnosticsState(ctx, uri); known {
			return diags, true
		}

		select {
		case <-ctx.Done():
			return nil, false
		case <-deadline.C:
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

func (s *Session) Definition(ctx context.Context, uri string, line, column int) (string, error) {
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: line, Character: column},
	}

	var result json.RawMessage
	if err := s.CallAndAwait(ctx, "textDocument/definition", params, &result); err != nil {
		return "", err
	}

	locations, err := parseLocationResponse(result)
	if err != nil {
		return "", err
	}

	if len(locations) == 0 {
		return "No definition found", nil
	}

	return formatDefinitions(locations, s.workingDir), nil
}

type DefLocation struct {
	Path   string
	Line   int
	Column int
}

func (s *Session) DefinitionLocations(ctx context.Context, uri string, line, column int) ([]DefLocation, error) {
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: line, Character: column},
	}

	var result json.RawMessage
	if err := s.CallAndAwait(ctx, "textDocument/definition", params, &result); err != nil {
		return nil, err
	}

	locations, err := parseLocationResponse(result)
	if err != nil {
		return nil, err
	}

	out := make([]DefLocation, 0, len(locations))
	for _, l := range locations {
		out = append(out, DefLocation{Path: uriToPath(l.URI), Line: l.Range.Start.Line, Column: l.Range.Start.Character})
	}
	return out, nil
}

func (s *Session) References(ctx context.Context, uri string, line, column int) (string, error) {
	params := ReferenceParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: line, Character: column},
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

func (s *Session) Implementation(ctx context.Context, uri string, line, column int) (string, error) {
	return s.locationOp(ctx, "textDocument/implementation", "Implementations", uri, line, column)
}

func (s *Session) Hover(ctx context.Context, uri string, line, column int) (string, error) {
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

	if hover.Contents.Value == "" {
		return "No hover information available", nil
	}

	return hover.Contents.Value, nil
}

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

	var symInfos []SymbolInformation
	if err := json.Unmarshal(result, &symInfos); err == nil && len(symInfos) > 0 && symInfos[0].Location.URI != "" {
		return formatSymbolInformations(symInfos, s.workingDir), nil
	}

	var docSymbols []DocumentSymbol
	if err := json.Unmarshal(result, &docSymbols); err == nil && len(docSymbols) > 0 {
		return formatDocumentSymbols(docSymbols, filePath, s.workingDir, 0), nil
	}

	return "No symbols found", nil
}

type symbolCandidate struct {
	name      string
	qualified string
	position  Position
}

func (s *Session) SymbolPosition(ctx context.Context, uri string, name string) (Position, bool) {
	params := DocumentSymbolParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
	}

	var result json.RawMessage
	if err := s.CallAndAwait(ctx, "textDocument/documentSymbol", params, &result); err != nil {
		return Position{}, false
	}

	if result == nil || string(result) == "null" {
		return Position{}, false
	}

	var candidates []symbolCandidate

	var symInfos []SymbolInformation
	if err := json.Unmarshal(result, &symInfos); err == nil && len(symInfos) > 0 && symInfos[0].Location.URI != "" {
		for _, sym := range symInfos {
			candidates = append(candidates, symbolCandidate{name: sym.Name, qualified: sym.Name, position: sym.Location.Range.Start})
		}
	} else {
		var docSymbols []DocumentSymbol
		if err := json.Unmarshal(result, &docSymbols); err == nil {
			collectSymbolCandidates(docSymbols, "", &candidates)
		}
	}

	matched, ok := matchSymbol(candidates, name)
	if !ok {
		return Position{}, false
	}

	return snapToIdentifier(uriToPath(uri), matched), true
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
		Position:     Position{Line: line, Character: column},
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
