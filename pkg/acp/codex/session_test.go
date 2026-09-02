package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
)

func TestCancelInterruptsTurnThatStartsAfterCancellation(t *testing.T) {
	agentIO, serverIO := net.Pipe()
	t.Cleanup(func() {
		_ = agentIO.Close()
		_ = serverIO.Close()
	})

	startReceived := make(chan struct{})
	releaseStart := make(chan struct{})
	interrupted := make(chan turnInterruptParams, 1)
	go func() {
		scanner := bufio.NewScanner(serverIO)
		for scanner.Scan() {
			var msg rpcMessage
			if json.Unmarshal(scanner.Bytes(), &msg) != nil {
				continue
			}
			switch msg.Method {
			case "turn/start":
				close(startReceived)
				<-releaseStart
				result, _ := json.Marshal(turnStartResponse{Turn: turn{ID: "late-turn", Status: "inProgress"}})
				writeRPCMessage(serverIO, rpcMessage{Jsonrpc: "2.0", ID: msg.ID, Result: result})
			case "turn/interrupt":
				var params turnInterruptParams
				_ = json.Unmarshal(msg.Params, &params)
				interrupted <- params
				writeRPCMessage(serverIO, rpcMessage{Jsonrpc: "2.0", ID: msg.ID, Result: json.RawMessage(`{}`)})
			}
		}
	}()

	rpc := newRPCClient(agentIO, agentIO)
	client := newCodexClient(rpc)
	rpc.start()
	sess := newSession("thread-1", "default", "default", nil)

	type promptResult struct {
		reason acp.StopReason
		err    error
	}
	done := make(chan promptResult, 1)
	go func() {
		reason, _, err := sess.runTurn(context.Background(), nil, client, acp.ClientCapabilities{}, nil, []acp.ContentBlock{acp.TextBlock("hello")})
		done <- promptResult{reason: reason, err: err}
	}()

	select {
	case <-startReceived:
	case <-time.After(time.Second):
		t.Fatal("turn/start was not received")
	}
	sess.interrupt(context.Background(), client)
	select {
	case result := <-done:
		if result.err != nil || result.reason != acp.StopReasonCancelled {
			t.Fatalf("prompt result = %q, %v", result.reason, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel waited for the delayed turn/start response")
	}

	close(releaseStart)
	select {
	case params := <-interrupted:
		if params.ThreadID != "thread-1" || params.TurnID != "late-turn" {
			t.Fatalf("interrupt params = %+v", params)
		}
	case <-time.After(time.Second):
		t.Fatal("late-started turn was not interrupted")
	}
}

func TestPromptToInputPreservesBlobResources(t *testing.T) {
	imageMIME := "image/png"
	binaryMIME := "application/pdf"
	blocks := []acp.ContentBlock{
		acp.ResourceBlock(acp.EmbeddedResourceResource{BlobResourceContents: &acp.BlobResourceContents{
			Uri: "file:///work/image.png", MimeType: &imageMIME, Blob: "IMAGE",
		}}),
		acp.ResourceBlock(acp.EmbeddedResourceResource{BlobResourceContents: &acp.BlobResourceContents{
			Uri: "file:///work/report.pdf", MimeType: &binaryMIME, Blob: "PDF",
		}}),
	}
	got := promptToInput(blocks)
	if len(got) != 2 {
		t.Fatalf("input = %#v", got)
	}
	image, ok := got[0].(map[string]any)
	if !ok || image["type"] != "image" || image["url"] != "data:image/png;base64,IMAGE" {
		t.Fatalf("image resource = %#v", got[0])
	}
	binary, ok := got[1].(map[string]any)
	text, _ := binary["text"].(string)
	if !ok || binary["type"] != "text" || !strings.Contains(text, `mimeType="application/pdf" encoding="base64"`) || !strings.Contains(text, "PDF") {
		t.Fatalf("binary resource = %#v", got[1])
	}
}

func TestRegisterSessionClosesReplacementAndAgentCloseCancelsSessions(t *testing.T) {
	a := newAgent(&codexClient{}, "default", "")
	old := a.registerSession("thread-1", "default", "", nil)
	oldCtx, cancelOld := context.WithCancel(context.Background())
	old.mu.Lock()
	old.cancelTurn = cancelOld
	old.mu.Unlock()

	replacement := a.registerSession("thread-1", "default", "", nil)
	select {
	case <-oldCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("replaced session was not cancelled")
	}
	if !old.isClosed() || a.lookup("thread-1") != replacement {
		t.Fatal("replacement session was not installed cleanly")
	}

	replacementCtx, cancelReplacement := context.WithCancel(context.Background())
	replacement.mu.Lock()
	replacement.cancelTurn = cancelReplacement
	replacement.mu.Unlock()
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	select {
	case <-replacementCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("agent close did not cancel the live session")
	}
	if !replacement.isClosed() || a.lookup("thread-1") != nil {
		t.Fatal("agent close did not remove and close its sessions")
	}
}

func TestReplayToolRepresentationsAreCompact(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		title     string
		location  string
		inputKeys []string
	}{
		{
			name:     "read",
			raw:      `{"id":"read-1","type":"commandExecution","status":"completed","commandActions":[{"type":"read","path":"/project/main.go"}]}`,
			title:    "Read file",
			location: "/project/main.go",
		},
		{
			name:      "command",
			raw:       `{"id":"cmd-1","type":"commandExecution","status":"completed","command":"/bin/zsh -lc go test ./...","cwd":"/project"}`,
			title:     "Run command",
			inputKeys: []string{"command", "cwd"},
		},
		{
			name:      "mcp",
			raw:       `{"id":"mcp-1","type":"mcpToolCall","status":"completed","server":"docs","tool":"search","arguments":{"query":"ACP"}}`,
			title:     "mcp.docs.search",
			inputKeys: []string{"query"},
		},
		{
			name:      "dynamic",
			raw:       `{"id":"dyn-1","type":"dynamicToolCall","status":"completed","tool":"lookup","arguments":{"query":"tools"}}`,
			title:     "lookup",
			inputKeys: []string{"query"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var updates []acp.SessionUpdate
			replayItem(func(update acp.SessionUpdate) {
				updates = append(updates, update)
			}, json.RawMessage(tc.raw), nil, false)
			if len(updates) == 0 || updates[0].ToolCall == nil {
				t.Fatalf("updates = %#v", updates)
			}
			call := updates[0].ToolCall
			if call.Title != tc.title {
				t.Fatalf("title = %q, want %q", call.Title, tc.title)
			}
			if tc.location != "" {
				if len(call.Locations) != 1 || call.Locations[0].Path != tc.location {
					t.Fatalf("locations = %#v", call.Locations)
				}
				if call.RawInput != nil {
					t.Fatalf("location duplicated into raw input: %#v", call.RawInput)
				}
				return
			}
			input, _ := call.RawInput.(map[string]any)
			if len(input) != len(tc.inputKeys) {
				t.Fatalf("raw input = %#v, want keys %v", call.RawInput, tc.inputKeys)
			}
			for _, key := range tc.inputKeys {
				if _, ok := input[key]; !ok {
					t.Fatalf("raw input = %#v, missing %q", input, key)
				}
			}
		})
	}
}

func TestReplayCommandOutputFallsBackToHistoryItem(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "read",
			raw:  `{"id":"read-1","type":"commandExecution","status":"completed","aggregatedOutput":"package main\n","commandActions":[{"type":"read","path":"/project/main.go"}]}`,
		},
		{
			name: "command",
			raw:  `{"id":"cmd-1","type":"commandExecution","status":"completed","command":"go test ./...","aggregatedOutput":"ok example/project\n"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var updates []acp.SessionUpdate
			replayItem(func(update acp.SessionUpdate) {
				updates = append(updates, update)
			}, json.RawMessage(tc.raw), nil, false)
			if len(updates) != 2 || updates[1].ToolCallUpdate == nil {
				t.Fatalf("updates = %#v", updates)
			}
			content := updates[1].ToolCallUpdate.Content
			if len(content) != 1 || content[0].Content == nil || content[0].Content.Content.Text == nil || content[0].Content.Content.Text.Text == "" {
				t.Fatalf("replayed content = %#v", content)
			}
		})
	}
}

func writeRPCMessage(conn net.Conn, msg rpcMessage) {
	b, _ := json.Marshal(msg)
	_, _ = conn.Write(append(b, '\n'))
}
