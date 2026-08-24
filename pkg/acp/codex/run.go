package codex

import (
	"context"
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
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("codex: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("codex: start %s: %w", codexPath, err)
	}

	rpc := newRPCClient(stdin, stdout)
	client := newCodexClient(rpc)
	rpc.start()

	a := newAgent(client, opts.Model, opts.Effort)
	a.cmd = cmd
	a.stdin = stdin
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
		if a.stdin != nil {
			_ = a.stdin.Close()
		}
		if a.cmd != nil && a.cmd.Process != nil {
			exited := make(chan struct{})
			go func() {
				_ = a.cmd.Wait()
				close(exited)
			}()
			select {
			case <-exited:
			case <-time.After(2 * time.Second):
				_ = a.cmd.Process.Kill()
				<-exited
			}
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

	conn := acp.NewAgentSideConnection(a, out, in)
	if logger != nil {
		conn.SetLogger(logger)
	}
	a.SetAgentConnection(conn)

	select {
	case <-conn.Done():
	case <-a.Done():
	case <-ctx.Done():
	}
	return nil
}
