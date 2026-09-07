package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"testing"
	"testing/synctest"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/acp/internal/acptest"
	"github.com/coder/acp-go-sdk"
)

type delayedPermissionClient struct {
	stubClient
	entered chan context.Context
	release chan struct{}
}

func (*delayedPermissionClient) SessionUpdate(context.Context, acp.SessionNotification) error {
	return nil
}

func (c *delayedPermissionClient) RequestPermission(ctx context.Context, _ acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	c.entered <- ctx
	<-c.release
	return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Selected: &acp.RequestPermissionOutcomeSelected{OptionId: optionAllowOnce}}}, nil
}

func TestProcessPermissionsFollowTurnCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		agentIO, clientIO := net.Pipe()
		defer agentIO.Close()
		defer clientIO.Close()
		a := New(Options{Stderr: io.Discard})
		conn := acp.NewAgentSideConnection(a, agentIO, agentIO)
		client := &delayedPermissionClient{entered: make(chan context.Context, 1), release: make(chan struct{})}
		_ = acp.NewClientSideConnection(client, clientIO, clientIO)
		var out bytes.Buffer
		p := &claudeProc{
			session: a.newSession("s", t.TempDir(), "default", "", nil),
			out:     &streamWriter{w: &out}, results: make(chan turnResult, 1), dead: make(chan struct{}),
			emitted: newToolCallTracker(),
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		p.beginTurn(ctx)
		reader, writer := io.Pipe()
		defer reader.Close()
		defer writer.Close()
		go p.read(context.Background(), conn, "s", reader)
		enc := json.NewEncoder(writer)
		var req controlRequest
		req.RequestID = "permission-1"
		req.Request.Subtype, req.Request.ToolName, req.Request.ToolUseID = "can_use_tool", "Bash", "tool-1"
		req.Request.Input = json.RawMessage(`{"command":"pwd"}`)
		if err := enc.Encode(map[string]any{"type": "control_request", "request_id": req.RequestID, "request": req.Request}); err != nil {
			t.Fatal(err)
		}
		uiCtx := <-client.entered
		cancel()
		synctest.Wait()
		if uiCtx.Err() == nil {
			t.Error("turn cancellation left the permission dialog alive")
		}
		var response struct {
			Response struct {
				Response askResponse `json:"response"`
			} `json:"response"`
		}
		if err := json.Unmarshal(out.Bytes(), &response); err != nil {
			t.Fatalf("permission did not resolve after turn cancellation: %q: %v", out.String(), err)
		}
		if response.Response.Response.Behavior != "deny" {
			t.Fatalf("cancelled permission = %q", out.String())
		}
		close(client.release)
		synctest.Wait()
		if bytes.Count(out.Bytes(), []byte("control_response")) != 1 {
			t.Fatalf("late answer generated another response: %q", out.String())
		}
	})
}

func TestDuplicateResultCannotCompleteTheNextTurn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := New(Options{Stderr: io.Discard})
		p := &claudeProc{session: a.newSession("s", t.TempDir(), "default", "", nil), results: make(chan turnResult, 1), dead: make(chan struct{})}
		reader, writer := io.Pipe()
		defer reader.Close()
		defer writer.Close()
		go p.read(context.Background(), nil, "s", reader)
		p.beginTurn(context.Background())
		oldID := p.turnID
		line := []byte("{\"type\":\"result\",\"subtype\":\"success\"}\n")
		_, _ = writer.Write(line)
		<-p.results
		_, _ = writer.Write(line)
		synctest.Wait()
		p.beginTurn(context.Background())
		if p.turnID == oldID {
			t.Fatal("turn reused its prompt UUID")
		}
		late, _ := json.Marshal(map[string]any{"type": "result", "subtype": "success", "user_message_uuid": oldID})
		_, _ = writer.Write(append(late, '\n'))
		synctest.Wait()
		select {
		case result := <-p.results:
			t.Fatalf("duplicate result leaked into new turn: %#v", result)
		default:
		}
		_, _ = writer.Write(line)
		<-p.results
	})
}

func TestQueuedPromptCancellationDoesNotWaitForRunningPrompt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := New(Options{})
		s := a.newSession("s", t.TempDir(), "default", "", nil)
		a.storeSession(s)
		if err := s.promptMu.Lock(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer s.promptMu.Unlock()
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan acp.StopReason, 1)
		go func() { r, _ := a.Prompt(ctx, acp.PromptRequest{SessionId: s.id}); result <- r.StopReason }()
		synctest.Wait()
		cancel()
		if stop := <-result; stop != acp.StopReasonCancelled {
			t.Fatalf("queued prompt = %s", stop)
		}
	})
}

func TestClaudeResultEOFHelper(t *testing.T) {
	if os.Getenv("CLAUDE_RESULT_EOF_HELPER") == "" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		var input cliInput
		if json.Unmarshal(scanner.Bytes(), &input) != nil || input.UUID == "" {
			os.Exit(2)
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"type": "result", "subtype": "success", "user_message_uuid": input.UUID})
	}
	os.Exit(0)
}

func TestFinalResultSurvivesImmediateProcessExit(t *testing.T) {
	script, dir, env := acptest.CommandHelper(t, "TestClaudeResultEOFHelper", "CLAUDE_RESULT_EOF_HELPER")
	env = append(env, "GORACE=atexit_sleep_ms=0")
	for range 8 {
		a := New(Options{Path: script, Env: env, Stderr: io.Discard})
		s := a.newSession("s", dir, "default", "", nil)
		a.storeSession(s)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		resp, err := a.Prompt(ctx, acp.PromptRequest{SessionId: s.id, Prompt: []acp.ContentBlock{acp.TextBlock("go")}})
		cancel()
		_ = a.Close()
		if err != nil || resp.StopReason != acp.StopReasonEndTurn {
			t.Fatalf("final result lost at EOF: %#v, %v", resp, err)
		}
	}
}
