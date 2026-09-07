package codex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/adrianliechti/wingman-agent/internal/process"
	codexcli "github.com/adrianliechti/wingman-agent/pkg/external/codex"
)

type Options struct {
	Model string

	Effort string

	Path string
	Dir  string
	Env  []string

	ExtraArgs []string

	Stderr io.Writer
}

func Spawn(ctx context.Context, opts Options) (*Agent, error) {
	codexPath := opts.Path
	if codexPath == "" {
		codexPath = codexcli.BinPath()
	}

	args := append(append([]string{}, opts.ExtraArgs...), "app-server")
	cmd := exec.CommandContext(ctx, codexPath, args...)
	cmd.WaitDelay = 2 * time.Second
	process.Hide(cmd)
	cmd.Dir = opts.Dir
	cmd.Stderr = opts.Stderr
	if cmd.Stderr == nil {
		cmd.Stderr = os.Stderr
	}
	if opts.Env != nil {
		cmd.Env = opts.Env
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("codex: stdin pipe: %w", err)
	}
	// Own the read end so Wait can observe process death without closing stdout
	// underneath the RPC reader before it drains the final notifications.
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("codex: stdout pipe: %w", err)
	}
	cmd.Stdout = stdoutWriter
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stdoutWriter.Close()
		return nil, fmt.Errorf("codex: start %s: %w", codexPath, err)
	}
	_ = stdoutWriter.Close()

	rpc := newRPCClient(stdin, stdout)
	client := newCodexClient(rpc)
	a := newAgent(client, opts.Model, opts.Effort)
	a.cmd = cmd
	a.stdin = stdin
	a.processDone = make(chan struct{})
	rpc.start()
	go func() {
		a.processErr = cmd.Wait()
		close(a.processDone)
		// A tool subprocess can inherit stdout and keep it open after Codex
		// exits. Allow buffered frames to drain, then fail instead of waiting
		// forever for that unrelated process to close its copy of the pipe.
		select {
		case <-rpc.done:
		case <-time.After(100 * time.Millisecond):
			rpc.close(errors.Join(fmt.Errorf("app-server process exited"), a.processErr))
		}
	}()
	return a, nil
}

func (a *Agent) Done() <-chan struct{} {
	if a.codex == nil || a.codex.rpc == nil {
		return a.closed
	}
	return a.codex.rpc.done
}

func (a *Agent) Close() error {
	a.closeOnce.Do(func() {
		a.mu.Lock()
		sessions := make([]*session, 0, len(a.sessions))
		for _, s := range a.sessions {
			sessions = append(sessions, s)
		}
		a.sessions = make(map[acp.SessionId]*session)
		a.mu.Unlock()
		for _, s := range sessions {
			s.markClosed()
		}
		if a.stdin != nil {
			_ = a.stdin.Close()
		}
		if a.cmd != nil && a.cmd.Process != nil {
			select {
			case <-a.processDone:
			case <-time.After(2 * time.Second):
				_ = a.cmd.Process.Kill()
				<-a.processDone
			}
		}
		if a.codex != nil && a.codex.rpc != nil {
			a.codex.rpc.close(io.EOF)
		}
		close(a.closed)
	})
	return nil
}

func Run(ctx context.Context, opts Options, in io.Reader, out io.Writer, logger *slog.Logger) error {
	a, err := Spawn(ctx, opts)
	if err != nil {
		return err
	}
	defer a.Close()

	writer := newClientWriter(out)
	defer writer.Close()
	conn := acp.NewAgentSideConnection(a, writer, in)
	if logger != nil {
		conn.SetLogger(logger)
	}
	a.SetAgentConnection(conn)

	select {
	case <-conn.Done():
	case <-a.Done():
		// Prefer an intentional client/context shutdown if it raced backend EOF.
		select {
		case <-conn.Done():
			return nil
		case <-ctx.Done():
			return nil
		default:
		}
		_ = a.Close()
		return errors.Join(a.codex.rpc.closedError(), a.processErr)
	case <-writer.Done():
		return writer.Err()
	case <-ctx.Done():
	}
	return nil
}
