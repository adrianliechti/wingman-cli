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
	"sync"
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

	// Track opened documents to avoid reopening
	openedDocs map[string]struct{}
	mu         sync.Mutex
}

// Manager caches LSP sessions so servers are reused across tool invocations.
type Manager struct {
	workingDir string
	sessions   map[string]*Session // keyed by server command
	mu         sync.Mutex
}

// NewManager creates a new LSP session manager.
func NewManager(workingDir string) *Manager {
	return &Manager{
		workingDir: workingDir,
		sessions:   make(map[string]*Session),
	}
}

// GetSession returns a cached session or creates a new one for the given file.
func (m *Manager) GetSession(ctx context.Context, filePath string) (*Session, error) {
	server := FindServer(m.workingDir, filePath)
	if server == nil {
		return nil, fmt.Errorf("no LSP server found for file: %s", filePath)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := server.Command

	if session, ok := m.sessions[key]; ok {
		// Verify the session is still alive by checking if the process exists
		if session.cmd.ProcessState == nil {
			return session, nil
		}
		// Process exited, remove stale session
		delete(m.sessions, key)
	}

	session, err := connect(ctx, m.workingDir, *server)
	if err != nil {
		return nil, err
	}

	m.sessions[key] = session
	return session, nil
}

// GetSessionByServer returns a cached session or creates a new one for a specific server.
func (m *Manager) GetSessionByServer(ctx context.Context, server Server) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := server.Command

	if session, ok := m.sessions[key]; ok {
		if session.cmd.ProcessState == nil {
			return session, nil
		}
		delete(m.sessions, key)
	}

	session, err := connect(ctx, m.workingDir, server)
	if err != nil {
		return nil, err
	}

	m.sessions[key] = session
	return session, nil
}

// Close shuts down all cached sessions.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for key, session := range m.sessions {
		session.Close()
		delete(m.sessions, key)
	}
}

// Connect starts and initializes an LSP server for the given file (non-cached).
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
		openedDocs: make(map[string]struct{}),
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
				Definition: DefinitionClientCapabilities{},
				References: ReferencesClientCapabilities{},
				Diagnostic: DiagnosticClientCapabilities{},
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

// OpenDocument opens a document in the LSP server, or no-ops if already open.
// It also sends didChange if the file content has changed since last open.
func (s *Session) OpenDocument(ctx context.Context, filePath string) (string, error) {
	uri := fileURI(filePath)

	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	s.mu.Lock()
	_, alreadyOpen := s.openedDocs[uri]
	s.mu.Unlock()

	if alreadyOpen {
		// Send didChange to sync latest content
		changeParams := DidChangeTextDocumentParams{
			TextDocument: VersionedTextDocumentIdentifier{
				URI:     uri,
				Version: int(time.Now().UnixMilli()),
			},
			ContentChanges: []TextDocumentContentChangeEvent{
				{Text: string(content)},
			},
		}

		if err := s.conn.Notify(ctx, "textDocument/didChange", changeParams); err != nil {
			return "", fmt.Errorf("didChange: %w", err)
		}

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
	if after, ok := strings.CutPrefix(uri, "file://"); ok {
		return after
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
}

type TextDocumentClientCapabilities struct {
	Synchronization TextDocumentSyncClientCapabilities `json:"synchronization,omitempty"`
	Hover           HoverClientCapabilities            `json:"hover,omitempty"`
	Definition      DefinitionClientCapabilities       `json:"definition,omitempty"`
	References      ReferencesClientCapabilities       `json:"references,omitempty"`
	Diagnostic      DiagnosticClientCapabilities       `json:"diagnostic,omitempty"`
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

type DiagnosticClientCapabilities struct{}

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

type Hover struct {
	Contents MarkupContent `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

type MarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
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
