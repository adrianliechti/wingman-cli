package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
)

func TestParseBuiltinCommand(t *testing.T) {
	tests := []struct {
		name   string
		prompt []acp.ContentBlock
		want   builtinCommand
		ok     bool
	}{
		{name: "plan", prompt: []acp.ContentBlock{acp.TextBlock(" /PLAN ")}, want: builtinCommand{name: "plan"}, ok: true},
		{name: "rename spaces", prompt: []acp.ContentBlock{acp.TextBlock("/rename Sprint cleanup")}, want: builtinCommand{name: "rename", args: "Sprint cleanup"}, ok: true},
		{name: "rename tab", prompt: []acp.ContentBlock{acp.TextBlock("/rename\tSprint cleanup")}, want: builtinCommand{name: "rename", args: "Sprint cleanup"}, ok: true},
		{name: "unknown", prompt: []acp.ContentBlock{acp.TextBlock("/status")}, want: builtinCommand{name: "status"}, ok: true},
		{name: "plain text", prompt: []acp.ContentBlock{acp.TextBlock("hello")}},
		{name: "multiple blocks", prompt: []acp.ContentBlock{acp.TextBlock("/rename X"), acp.TextBlock("extra")}},
		{name: "image", prompt: []acp.ContentBlock{acp.ImageBlock("AAAA", "image/png")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseBuiltinCommand(tt.prompt)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("parseBuiltinCommand() = %#v, %v; want %#v, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestAvailableCommandsIncludeRenameInput(t *testing.T) {
	commands := availableCommands()
	for _, command := range commands {
		if command.Name != "rename" {
			continue
		}
		if command.Input == nil || command.Input.Unstructured == nil || command.Input.Unstructured.Hint != "new name" {
			t.Fatalf("rename command input = %#v", command.Input)
		}
		return
	}
	t.Fatal("rename command not advertised")
}

type titleUpdateClient struct {
	liveClient
	updates chan acp.SessionNotification
}

func (c *titleUpdateClient) SessionUpdate(_ context.Context, notification acp.SessionNotification) error {
	c.updates <- notification
	return nil
}

func TestNameNotificationsReachIdleSessions(t *testing.T) {
	agentIO, clientIO := net.Pipe()
	appAgentIO, appServerIO := net.Pipe()
	t.Cleanup(func() {
		_ = agentIO.Close()
		_ = clientIO.Close()
		_ = appAgentIO.Close()
		_ = appServerIO.Close()
	})

	rpc := newRPCClient(appAgentIO, appAgentIO)
	client := newCodexClient(rpc)
	agent := newAgent(client, "default", "")
	agent.mu.Lock()
	agent.sessions["thread-1"] = newSession("thread-1", "default", "", nil)
	agent.mu.Unlock()
	rpc.start()
	agent.SetAgentConnection(acp.NewAgentSideConnection(agent, agentIO, agentIO))
	recorder := &titleUpdateClient{updates: make(chan acp.SessionNotification, 1)}
	_ = acp.NewClientSideConnection(recorder, clientIO, clientIO)

	writeRPCMessage(appServerIO, rpcMessage{Jsonrpc: "2.0", Method: "thread/name/updated", Params: json.RawMessage(`{"threadId":"thread-1","threadName":"Sprint cleanup"}`)})
	select {
	case notification := <-recorder.updates:
		if notification.SessionId != "thread-1" || notification.Update.SessionInfoUpdate == nil || notification.Update.SessionInfoUpdate.Title == nil || *notification.Update.SessionInfoUpdate.Title != "Sprint cleanup" {
			t.Fatalf("title notification = %#v", notification)
		}
	case <-time.After(time.Second):
		t.Fatal("idle title notification was not forwarded")
	}

	resp, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: "thread-1",
		Prompt:    []acp.ContentBlock{acp.TextBlock("/rename")},
	})
	if err != nil || resp.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("empty rename response = %#v, err=%v", resp, err)
	}
	select {
	case notification := <-recorder.updates:
		chunk := notification.Update.AgentMessageChunk
		if chunk == nil || chunk.Content.Text == nil || chunk.Content.Text.Text != "Usage: /rename <new name>" {
			t.Fatalf("empty rename update = %#v", notification)
		}
	case <-time.After(time.Second):
		t.Fatal("empty rename usage was not sent")
	}

	agent.mu.Lock()
	delete(agent.sessions, "thread-1")
	agent.mu.Unlock()
	writeRPCMessage(appServerIO, rpcMessage{Jsonrpc: "2.0", Method: "thread/name/updated", Params: json.RawMessage(`{"threadId":"thread-1","threadName":"Late rename"}`)})
	select {
	case notification := <-recorder.updates:
		t.Fatalf("closed session received title notification: %#v", notification)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestPromptRenameUsesThreadNameSet(t *testing.T) {
	agentIO, serverIO := net.Pipe()
	t.Cleanup(func() {
		_ = agentIO.Close()
		_ = serverIO.Close()
	})

	received := make(chan threadSetNameParams, 2)
	go func() {
		scanner := bufio.NewScanner(serverIO)
		requests := 0
		for scanner.Scan() {
			var msg rpcMessage
			if json.Unmarshal(scanner.Bytes(), &msg) != nil || msg.Method != "thread/name/set" {
				continue
			}
			var params threadSetNameParams
			_ = json.Unmarshal(msg.Params, &params)
			received <- params
			requests++
			if requests == 1 {
				writeRPCMessage(serverIO, rpcMessage{Jsonrpc: "2.0", ID: msg.ID, Result: json.RawMessage(`{}`)})
				continue
			}
			writeRPCMessage(serverIO, rpcMessage{Jsonrpc: "2.0", ID: msg.ID, Error: &rpcError{Code: -32000, Message: "rename rejected"}})
			return
		}
		if err := scanner.Err(); err != nil {
			t.Errorf("scan app-server request: %v", err)
		}
	}()

	rpc := newRPCClient(agentIO, agentIO)
	client := newCodexClient(rpc)
	rpc.start()
	agent := newAgent(client, "default", "")
	agent.sessions["thread-1"] = newSession("thread-1", "default", "", nil)

	resp, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: "thread-1",
		MessageId: new("message-1"),
		Prompt:    []acp.ContentBlock{acp.TextBlock("/rename Sprint cleanup")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != acp.StopReasonEndTurn || resp.UserMessageId == nil || *resp.UserMessageId != "message-1" {
		t.Fatalf("prompt response = %#v", resp)
	}
	select {
	case params := <-received:
		if params.ThreadID != "thread-1" || params.Name != "Sprint cleanup" {
			t.Fatalf("thread/name/set params = %#v", params)
		}
	case <-time.After(time.Second):
		t.Fatal("thread/name/set was not sent")
	}

	_, err = agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: "thread-1",
		Prompt:    []acp.ContentBlock{acp.TextBlock("/rename Rejected")},
	})
	if err == nil {
		t.Fatal("rename RPC error was not returned")
	}
	select {
	case params := <-received:
		if params.Name != "Rejected" {
			t.Fatalf("failed thread/name/set params = %#v", params)
		}
	case <-time.After(time.Second):
		t.Fatal("failed thread/name/set was not sent")
	}
}
