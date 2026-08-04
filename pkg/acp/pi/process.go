package pi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/google/uuid"
)

var errProcessClosed = errors.New("pi process closed")

type spawnOptions struct {
	Path string
	Dir  string
	Env  []string
	Args []string
}

type rpcResponse struct {
	Type    string          `json:"type"`
	ID      string          `json:"id"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

type process struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser

	writeMu sync.Mutex

	mu      sync.Mutex
	pending map[string]chan rpcResponse
	handler func(json.RawMessage)

	done chan struct{}
}

func spawn(opts spawnOptions) (*process, error) {
	path := opts.Path
	if path == "" {
		path = "pi"
	}

	args := append([]string{"--mode", "rpc", "--no-themes"}, opts.Args...)

	cmd := exec.Command(path, args...)
	cmd.Dir = opts.Dir
	cmd.Stderr = os.Stderr
	if opts.Env != nil {
		cmd.Env = opts.Env
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("pi: stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("pi: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("pi: start %s: %w", path, err)
	}

	p := &process{
		cmd:     cmd,
		stdin:   stdin,
		pending: map[string]chan rpcResponse{},
		done:    make(chan struct{}),
	}

	go p.readLoop(stdout)

	return p, nil
}

func (p *process) readLoop(stdout io.Reader) {
	defer p.failPending()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var resp rpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			continue
		}

		if resp.Type == "response" && resp.ID != "" {
			p.mu.Lock()
			ch := p.pending[resp.ID]
			delete(p.pending, resp.ID)
			p.mu.Unlock()
			if ch != nil {
				ch <- resp
			}
			continue
		}

		p.mu.Lock()
		h := p.handler
		p.mu.Unlock()
		if h != nil {
			raw := make(json.RawMessage, len(line))
			copy(raw, line)
			h(raw)
		} else {
			p.handleIdleEvent(line)
		}
	}
}

func (p *process) handleIdleEvent(raw json.RawMessage) {
	var event struct {
		Type   string `json:"type"`
		ID     string `json:"id"`
		Method string `json:"method"`
	}
	if json.Unmarshal(raw, &event) != nil || event.Type != "extension_ui_request" || event.ID == "" {
		return
	}
	switch event.Method {
	case "confirm", "select", "input", "editor":
		// Extension dialogs can occur during session_start, load, or clone when
		// no ACP turn exists to surface them. Cancel so Pi never deadlocks.
		p.sendExtensionResponse(map[string]any{"id": event.ID, "cancelled": true})
	}
}

func (p *process) failPending() {
	close(p.done)

	p.mu.Lock()
	pending := p.pending
	p.pending = map[string]chan rpcResponse{}
	p.mu.Unlock()

	for _, ch := range pending {
		ch <- rpcResponse{Error: errProcessClosed.Error()}
	}
}

func (p *process) setHandler(h func(json.RawMessage)) {
	p.mu.Lock()
	p.handler = h
	p.mu.Unlock()
}

func (p *process) writeLine(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}

	p.writeMu.Lock()
	defer p.writeMu.Unlock()

	select {
	case <-p.done:
		return errProcessClosed
	default:
	}

	if _, err := p.stdin.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func (p *process) request(ctx context.Context, cmd map[string]any) (rpcResponse, error) {
	id := uuid.NewString()
	cmd["id"] = id

	ch := make(chan rpcResponse, 1)
	p.mu.Lock()
	p.pending[id] = ch
	p.mu.Unlock()

	if err := p.writeLine(cmd); err != nil {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return rpcResponse{}, err
	}

	select {
	case <-ctx.Done():
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return rpcResponse{}, ctx.Err()
	case resp := <-ch:
		if !resp.Success {
			msg := resp.Error
			if msg == "" {
				msg = "request failed"
			}
			return resp, fmt.Errorf("pi %v: %s", cmd["type"], msg)
		}
		return resp, nil
	}
}

func (p *process) prompt(ctx context.Context, message string, images []piImage) error {
	_, err := p.request(ctx, map[string]any{"type": "prompt", "message": message, "images": images})
	return err
}

func (p *process) abort(ctx context.Context) error {
	_, err := p.request(ctx, map[string]any{"type": "abort"})
	return err
}

func (p *process) getState(ctx context.Context) (json.RawMessage, error) {
	resp, err := p.request(ctx, map[string]any{"type": "get_state"})
	return resp.Data, err
}

func (p *process) getAvailableModels(ctx context.Context) (json.RawMessage, error) {
	resp, err := p.request(ctx, map[string]any{"type": "get_available_models"})
	return resp.Data, err
}

func (p *process) getAvailableThinkingLevels(ctx context.Context) (json.RawMessage, error) {
	resp, err := p.request(ctx, map[string]any{"type": "get_available_thinking_levels"})
	return resp.Data, err
}

func (p *process) setModel(ctx context.Context, provider, modelID string) error {
	_, err := p.request(ctx, map[string]any{"type": "set_model", "provider": provider, "modelId": modelID})
	return err
}

func (p *process) setThinkingLevel(ctx context.Context, level string) error {
	_, err := p.request(ctx, map[string]any{"type": "set_thinking_level", "level": level})
	return err
}

func (p *process) getMessages(ctx context.Context) (json.RawMessage, error) {
	resp, err := p.request(ctx, map[string]any{"type": "get_messages"})
	return resp.Data, err
}

func (p *process) clone(ctx context.Context) (json.RawMessage, error) {
	resp, err := p.request(ctx, map[string]any{"type": "clone"})
	return resp.Data, err
}

func (p *process) sendExtensionResponse(v map[string]any) {
	v["type"] = "extension_ui_response"
	_ = p.writeLine(v)
}

func (p *process) dispose() {
	if p.stdin != nil {
		_ = p.stdin.Close()
	}

	if p.cmd == nil || p.cmd.Process == nil {
		return
	}

	exited := make(chan struct{})
	go func() {
		_ = p.cmd.Wait()
		close(exited)
	}()

	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		_ = p.cmd.Process.Kill()
		<-exited
	}
}
