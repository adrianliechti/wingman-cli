package lsp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/fileuri"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
)

type initializationRecorder struct {
	protocol.UnimplementedServer
	params *protocol.InitializeParams
}

func (r *initializationRecorder) Initialize(_ context.Context, params *protocol.InitializeParams) (*protocol.InitializeResult, error) {
	r.params = params
	return &protocol.InitializeResult{}, nil
}

func (*initializationRecorder) Initialized(context.Context, *protocol.InitializedParams) error {
	return nil
}

func TestInitializationAndWorkspaceRequestsUseTheSameProjectURI(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project with spaces")
	uri := fileuri.FromPath(root)
	rpc := &initializationRecorder{}
	session := &Session{rootURI: uri, rpc: rpc}
	if err := session.initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	folders, set := rpc.params.WorkspaceFolders.Get()
	if !set || len(folders) != 1 || folders[0].URI.String() != uri || folders[0].Name != "project with spaces" {
		t.Fatalf("initial workspace folders = %+v (set=%v)", folders, set)
	}
	if capability := rpc.params.Capabilities.Workspace.WorkspaceFolders; capability == nil || !*capability {
		t.Fatal("workspace folder support was not advertised")
	}
	response, err := (&sessionClient{session: session}).WorkspaceFolders(t.Context())
	if err != nil || !reflect.DeepEqual(response, folders) {
		t.Fatalf("workspace folder request = %+v, %v", response, err)
	}
}

func TestCapabilityEnabledHandlesBooleanOrOptions(t *testing.T) {
	if CapabilityEnabled(nil) || CapabilityEnabled(protocol.Boolean(false)) || CapabilityEnabled(false) {
		t.Fatal("disabled capability reported as enabled")
	}
	if !CapabilityEnabled(protocol.Boolean(true)) || !CapabilityEnabled(true) || !CapabilityEnabled(&protocol.HoverOptions{}) {
		t.Fatal("enabled capability reported as disabled")
	}
}

func TestDocumentSyncPolicyAndIncrementalChange(t *testing.T) {
	includeText := true
	openClose := true
	incremental := protocol.TextDocumentSyncKindIncremental
	session := &Session{capabilities: protocol.ServerCapabilities{
		TextDocumentSync: &protocol.TextDocumentSyncOptions{
			OpenClose: &openClose,
			Change:    &incremental,
			Save:      &protocol.SaveOptions{IncludeText: &includeText},
		},
	}}
	policy := session.documentSyncPolicy()
	if !policy.openClose || policy.change != incremental || !policy.save || !policy.includeText {
		t.Fatalf("policy = %+v", policy)
	}

	changes := documentChanges(incremental, "first\n😀x", "replacement")
	if len(changes) != 1 {
		t.Fatalf("changes = %#v", changes)
	}
	change, ok := changes[0].(*protocol.TextDocumentContentChangePartial)
	if !ok || change.Range.End.Line != 1 || change.Range.End.Character != 3 || change.Text != "replacement" {
		t.Fatalf("incremental change = %#v", changes[0])
	}
}

func TestOpenDocumentDoesNotReplaceDirtyEditorBuffer(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("disk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	uri := fileuri.FromPath(path)
	session := &Session{documents: map[string]*document{
		uri: {opened: true, saved: false, content: "editor\n"},
	}}
	got, err := session.OpenDocument(context.Background(), path)
	if err != nil || got != uri {
		t.Fatalf("OpenDocument() = %q, %v", got, err)
	}
	if content, ok := session.DocumentContent(path); !ok || content != "editor\n" {
		t.Fatalf("content = %q, %v", content, ok)
	}
}

func TestSessionTracksWorkDoneProgressDetails(t *testing.T) {
	message := "Loading Gradle project"
	percentage := uint32(20)
	session := &Session{}
	session.applyProgress("gradle", progressUpdate{
		Kind:       "begin",
		Title:      "Indexing Kotlin",
		Message:    &message,
		Percentage: &percentage,
	})
	if !session.Analyzing() {
		t.Fatal("session did not report active analysis")
	}
	progress := session.Progress()
	if len(progress) != 1 || progress[0].Title != "Indexing Kotlin" || progress[0].Message != message || progress[0].Percentage == nil || *progress[0].Percentage != 20 {
		t.Fatalf("progress = %+v", progress)
	}

	message = "Resolving dependencies"
	percentage = 65
	session.applyProgress("gradle", progressUpdate{Kind: "report", Message: &message, Percentage: &percentage})
	progress = session.Progress()
	if len(progress) != 1 || progress[0].Title != "Indexing Kotlin" || progress[0].Message != message || progress[0].Percentage == nil || *progress[0].Percentage != 65 {
		t.Fatalf("reported progress = %+v", progress)
	}

	session.applyProgress("gradle", progressUpdate{Kind: "end"})
	if session.Analyzing() || len(session.Progress()) != 0 {
		t.Fatal("completed progress remained active")
	}
}

func TestManagerActivitiesAreReadOnlyAndProjectScoped(t *testing.T) {
	root := t.TempDir()
	session := &Session{
		server: Server{
			Name: "gopls", Label: "Go",
		},
		rootURI:  fileuri.FromPath(root),
		progress: map[string]WorkProgress{"index": {Title: "Indexing"}},
	}
	session.alive.Store(true)
	manager := NewManager(root)
	defer manager.cancel()
	manager.sessions["go"] = session

	activities := manager.Activities()
	if len(activities) != 1 || activities[0].Server != "gopls" || activities[0].Label != "Go" || activities[0].ProjectDir != root || !activities[0].Analyzing || len(activities[0].Operations) != 1 {
		t.Fatalf("activities = %+v", activities)
	}
}

func TestGetSessionStopsRestartingAfterRepeatedCrashes(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root)
	project := projectRoot{
		Dir:    root,
		Server: Server{Name: "test-server", Command: filepath.Join(root, "missing-language-server")},
	}
	key := projectKey(project)

	// Each round simulates a session that started and then died.
	for attempt := 1; attempt <= maxRestarts; attempt++ {
		manager.sessions[key] = &Session{}
		if _, err := manager.getSession(context.Background(), project); err == nil {
			t.Fatalf("attempt %d: expected the restart to fail", attempt)
		}
		if manager.restarts[key] != attempt {
			t.Fatalf("attempt %d: restarts = %d, want %d", attempt, manager.restarts[key], attempt)
		}
	}

	manager.sessions[key] = &Session{}
	_, err := manager.getSession(context.Background(), project)
	if err == nil || !strings.Contains(err.Error(), "not restarting") {
		t.Fatalf("err = %v, want the restart cap to apply", err)
	}

	// The cap must survive the dead session being dropped from the map.
	if _, err := manager.getSession(context.Background(), project); err == nil || !strings.Contains(err.Error(), "not restarting") {
		t.Fatalf("err = %v, want the restart cap to stay in effect", err)
	}
}

func TestServerInitializationOptionsInvalidateOldDescriptor(t *testing.T) {
	manager := NewManager(t.TempDir())
	server := Server{Name: "jdtls", InitializationOptions: []byte(`{"bundles":[]}`)}

	if err := manager.SetServerInitializationOptions("JDTLS", map[string]any{"bundles": []string{"debug.jar"}}); err != nil {
		t.Fatal(err)
	}
	manager.detectMu.Lock()
	current := manager.serverInitializationOptionsCurrentLocked(server)
	manager.detectMu.Unlock()
	if current {
		t.Fatal("descriptor with old initialization options remained current")
	}
	if len(manager.initializationOptions["jdtls"]) == 0 {
		t.Fatal("initialization options were not normalized by server name")
	}
}

func TestRetryRPCReturnsLastTransientErrorWithoutAnotherDelay(t *testing.T) {
	previousDelay := retryBaseDelay
	retryBaseDelay = 0
	t.Cleanup(func() { retryBaseDelay = previousDelay })

	ctx, cancel := context.WithCancel(context.Background())
	want := &jsonrpc2.Error{Code: codeRequestCancelled, Message: "retry"}
	attempts := 0
	_, err := retryRPC(ctx, func() (struct{}, error) {
		attempts++
		if attempts == maxRetries {
			// Cancellation after the final response must not replace that response
			// while retryRPC waits for a retry it will never perform.
			cancel()
		}
		return struct{}{}, want
	})
	if !errors.Is(err, want) {
		t.Fatalf("retry error = %v, want final transient error %v", err, want)
	}
	if attempts != maxRetries {
		t.Fatalf("attempts = %d, want %d", attempts, maxRetries)
	}
}

func TestManagerCloseCancelsInFlightSessionStart(t *testing.T) {
	manager := NewManager(t.TempDir())
	started := make(chan struct{})
	manager.connect = func(ctx context.Context, _ string, _ Server) (*Session, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	project := projectRoot{Dir: t.TempDir(), Server: Server{Name: "blocked"}}
	result := make(chan error, 1)
	go func() {
		_, err := manager.getSession(context.Background(), project)
		result <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("session start did not begin")
	}
	manager.Close()

	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "manager is closed") {
			t.Fatalf("getSession error = %v, want manager closed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("manager close left session startup blocked")
	}
}

func TestCancellingFirstCallerDoesNotCancelSharedSessionStart(t *testing.T) {
	manager := NewManager(t.TempDir())
	t.Cleanup(manager.cancel)

	started := make(chan struct{})
	release := make(chan struct{})
	want := &Session{}
	want.alive.Store(true)
	manager.connect = func(ctx context.Context, _ string, _ Server) (*Session, error) {
		close(started)
		select {
		case <-release:
			return want, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	project := projectRoot{Dir: t.TempDir(), Server: Server{Name: "slow"}}
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstResult := make(chan error, 1)
	go func() {
		_, err := manager.getSession(firstCtx, project)
		firstResult <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("session start did not begin")
	}
	cancelFirst()
	select {
	case err := <-firstResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("first getSession error = %v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled caller remained blocked")
	}

	secondResult := make(chan struct {
		session *Session
		err     error
	}, 1)
	go func() {
		session, err := manager.getSession(context.Background(), project)
		secondResult <- struct {
			session *Session
			err     error
		}{session: session, err: err}
	}()
	close(release)

	select {
	case result := <-secondResult:
		if result.err != nil || result.session != want {
			t.Fatalf("second getSession = %p, %v; want %p, nil", result.session, result.err, want)
		}
	case <-time.After(time.Second):
		t.Fatal("shared session start did not finish")
	}
}
