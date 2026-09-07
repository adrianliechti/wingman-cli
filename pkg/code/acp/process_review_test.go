package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/code"
)

// This helper acts as an ACP process and can leave stdout inherited by a child
// when it exits. The child exits on stdin EOF, so normal transport cleanup also
// cleans up the helper without a timer or an orphaned long-running process.
func TestACPClientProcessHelper(t *testing.T) {
	mode := os.Getenv("WINGMAN_ACP_CLIENT_REVIEW_HELPER")
	if mode == "" {
		return
	}
	if mode == "hold" {
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			os.Exit(2)
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}}
		case "session/new":
			result = map[string]any{"sessionId": "session"}
		case "session/prompt":
			if mode == "inherited-output" {
				exe, _ := os.Executable()
				child := exec.Command(exe, "-test.run=^TestACPClientProcessHelper$")
				child.Env = append(os.Environ(), "WINGMAN_ACP_CLIENT_REVIEW_HELPER=hold")
				child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
				if child.Start() != nil {
					os.Exit(3)
				}
				os.Exit(0)
			}
			result = map[string]any{"stopReason": "end_turn"}
		default:
			continue
		}
		if encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}) != nil {
			os.Exit(4)
		}
		if req.Method == "session/prompt" {
			os.Exit(0)
		}
	}
	os.Exit(0)
}

func TestProcessExitDrainsFinalResponseOrFailsInheritedOutput(t *testing.T) {
	for _, mode := range []string{"final-response", "inherited-output"} {
		t.Run(mode, func(t *testing.T) {
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			a, err := New(ctx, &code.Workspace{RootPath: t.TempDir()}, code.AgentDef{
				Name: "test", Command: exe, Args: []string{"-test.run=^TestACPClientProcessHelper$"},
				Env: map[string]string{"WINGMAN_ACP_CLIENT_REVIEW_HELPER": mode, "GORACE": "atexit_sleep_ms=0"},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			id, err := a.NewSession(ctx)
			if err != nil {
				t.Fatal(err)
			}
			stream, err := a.Send(ctx, id, []agent.Content{{Text: "go"}})
			if err != nil {
				t.Fatal(err)
			}
			var failure error
			for _, err := range stream {
				if err != nil {
					failure = err
				}
			}
			if mode == "final-response" && failure != nil {
				t.Fatalf("final response lost at EOF: %v", failure)
			}
			if mode == "inherited-output" && failure == nil {
				t.Fatal("process death was reported as a successful turn")
			}
			if ctx.Err() != nil {
				t.Fatalf("waited for caller deadline after process exit: %v", ctx.Err())
			}
			select {
			case <-a.processDone:
			case <-ctx.Done():
				t.Fatal("process was not reaped")
			}
		})
	}
}
