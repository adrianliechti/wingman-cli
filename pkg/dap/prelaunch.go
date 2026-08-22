package dap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adrianliechti/wingman-agent/internal/tooling"
)

const (
	preLaunchReadyTimeout = 60 * time.Second
	preLaunchExitTimeout  = 5 * time.Minute
)

type debugProcess struct {
	cmd      *exec.Cmd
	done     chan struct{}
	mu       sync.Mutex
	err      error
	stopping atomic.Bool
	stopOnce sync.Once
}

func startDebugProcess(plan Plan, output func(string, string)) (*debugProcess, error) {
	launch := plan.PreLaunch
	if launch == nil {
		return nil, nil
	}
	if err := ensureReadyAddressAvailable(launch.ReadyURL); err != nil {
		return nil, err
	}
	stdoutCategory, stderrCategory := "dev server stdout", "dev server stderr"
	if launch.WaitForExit {
		stdoutCategory, stderrCategory = "stdout", "stderr"
	}
	cmd := exec.Command(launch.Command, launch.Args...)
	cmd.Dir = plan.ProjectDir
	cmd.Env = tooling.Environment(launch.Command, os.Environ())
	// Writer-based output lets Wait drain the final lines; WaitDelay keeps a
	// grandchild that inherited the pipes from blocking exit detection.
	cmd.Stdout = &outputWriter{category: stdoutCategory, output: output}
	cmd.Stderr = &outputWriter{category: stderrCategory, output: output}
	cmd.WaitDelay = 3 * time.Second
	configureAdapterProcess(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", launch.Title, err)
	}
	process := &debugProcess{cmd: cmd, done: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		process.mu.Lock()
		process.err = err
		process.mu.Unlock()
		close(process.done)
	}()
	return process, nil
}

type outputWriter struct {
	category string
	output   func(string, string)
}

func (w *outputWriter) Write(p []byte) (int, error) {
	w.output(w.category, string(p))
	return len(p), nil
}

func ensureReadyAddressAvailable(address string) error {
	if address == "" {
		return nil
	}
	parsed, err := url.Parse(address)
	if err != nil || parsed.Host == "" {
		return nil
	}
	connection, err := net.DialTimeout("tcp", parsed.Host, 200*time.Millisecond)
	if err != nil {
		return nil
	}
	_ = connection.Close()
	return fmt.Errorf("development server address %s is already in use", parsed.Host)
}

func (process *debugProcess) waitReady(ctx context.Context, address string) error {
	if process == nil || address == "" {
		return nil
	}
	readyCtx, cancel := context.WithTimeout(ctx, preLaunchReadyTimeout)
	defer cancel()
	client := &http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(readyCtx, http.MethodGet, address, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			return nil
		}
		select {
		case <-process.done:
			return fmt.Errorf("development server exited before %s was ready: %w", address, process.waitError())
		case <-ticker.C:
		case <-readyCtx.Done():
			if errors.Is(readyCtx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("development server did not become ready at %s within %s", address, preLaunchReadyTimeout)
			}
			return readyCtx.Err()
		}
	}
}

func (process *debugProcess) waitExit(ctx context.Context, title string) error {
	exitCtx, cancel := context.WithTimeout(ctx, preLaunchExitTimeout)
	defer cancel()
	select {
	case <-process.done:
		process.mu.Lock()
		err := process.err
		process.mu.Unlock()
		if err != nil {
			return fmt.Errorf("%s failed: %w", title, err)
		}
		return nil
	case <-exitCtx.Done():
		if errors.Is(exitCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%s did not finish within %s", title, preLaunchExitTimeout)
		}
		return exitCtx.Err()
	}
}

func (process *debugProcess) waitError() error {
	process.mu.Lock()
	defer process.mu.Unlock()
	if process.err == nil {
		return errors.New("process exited")
	}
	return process.err
}

func (process *debugProcess) Close() {
	if process == nil {
		return
	}
	process.stopOnce.Do(func() {
		process.stopping.Store(true)
		if process.cmd != nil && process.cmd.Process != nil {
			killAdapterProcess(process.cmd)
		}
	})
}
