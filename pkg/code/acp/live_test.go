package acp

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	acpclaude "github.com/adrianliechti/wingman-agent/pkg/acp/claude"
	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	extclaude "github.com/adrianliechti/wingman-agent/pkg/external/claude"
)

type scriptedUI struct {
	mu      sync.Mutex
	elicits []tool.ElicitRequest
	answer  func(tool.ElicitRequest) tool.ElicitResult
}

func (u *scriptedUI) Elicit(_ context.Context, req tool.ElicitRequest) (tool.ElicitResult, error) {
	u.mu.Lock()
	u.elicits = append(u.elicits, req)
	u.mu.Unlock()
	return u.answer(req), nil
}

func (u *scriptedUI) Confirm(context.Context, string) (bool, error) {
	return true, nil
}

func (u *scriptedUI) requests() []tool.ElicitRequest {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]tool.ElicitRequest(nil), u.elicits...)
}

// TestLiveWebPipelineAskUserQuestion runs the exact in-process wiring the web
// server uses for the claude agent (server/agents.go claudeBackend), with the
// real CLI: AskUserQuestion must arrive at the code.UI as a translated form
// elicitation, and the answer must round-trip back to the model.
func TestLiveWebPipelineAskUserQuestion(t *testing.T) {
	if os.Getenv("CLAUDE_ACP_LIVE") == "" {
		t.Skip("set CLAUDE_ACP_LIVE=1 to run the live web pipeline test")
	}
	path := extclaude.BinPath()
	if _, err := exec.LookPath(path); err != nil {
		t.Skipf("claude not found: %v", err)
	}

	dir := t.TempDir()
	ws, err := code.NewWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}

	srv := acpclaude.New(acpclaude.Options{Path: path, Env: os.Environ(), Cwd: dir})
	a, err := NewInProcess(ws, "claude", srv, func(conn *acpsdk.AgentSideConnection) {
		srv.SetAgentConnection(conn)
	}, srv.Close)
	if err != nil {
		t.Fatalf("new in-process agent: %v", err)
	}
	defer a.Close()

	ui := &scriptedUI{answer: func(req tool.ElicitRequest) tool.ElicitResult {
		if len(req.Fields) == 0 {
			return tool.ElicitResult{Action: tool.ElicitDecline}
		}
		return tool.ElicitResult{
			Action:  tool.ElicitAccept,
			Content: map[string]any{req.Fields[0].Name: "Blue"},
		}
	}}
	a.SetUI(ui)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	sessionID, err := a.NewSession(ctx)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}

	stream, err := a.Send(ctx, sessionID, []agent.Content{{
		Text: "Use the AskUserQuestion tool to ask me one question: \"Which color?\" with the options Red and Blue. " +
			"After I answer, reply with exactly the color I chose and nothing else.",
	}})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	var text strings.Builder
	for msg, err := range stream {
		if err != nil {
			t.Fatalf("turn: %v", err)
		}
		for _, c := range msg.Content {
			text.WriteString(c.Text)
		}
	}

	elicits := ui.requests()
	if len(elicits) == 0 {
		t.Fatal("AskUserQuestion never reached the web UI layer as an elicitation")
	}
	form := elicits[0]
	if form.Message != "Which color?" {
		t.Errorf("form message = %q", form.Message)
	}
	if len(form.Fields) < 1 || !form.Fields[0].Strict || len(form.Fields[0].Enum) != 2 {
		t.Errorf("form fields = %#v", form.Fields)
	}
	if got := text.String(); !strings.Contains(got, "Blue") || strings.Contains(got, "Red") {
		t.Errorf("reply = %q, want the form answer Blue", got)
	}
}
