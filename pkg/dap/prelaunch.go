package dap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

const preLaunchReadyTimeout = 60 * time.Second

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
	cmd := exec.Command(launch.Command, launch.Args...)
	cmd.Dir = plan.ProjectDir
	cmd.Env = os.Environ()
	configureAdapterProcess(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("%s output: %w", launch.Title, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("%s error output: %w", launch.Title, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", launch.Title, err)
	}
	process := &debugProcess{cmd: cmd, done: make(chan struct{})}
	go copyAdapterOutput(stdout, "dev server stdout", output)
	go copyAdapterOutput(stderr, "dev server stderr", output)
	go func() {
		err := cmd.Wait()
		process.mu.Lock()
		process.err = err
		process.mu.Unlock()
		close(process.done)
	}()
	return process, nil
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
