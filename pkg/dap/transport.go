package dap

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const adapterStartupTimeout = 30 * time.Second

type adapterConnection struct {
	io.ReadWriteCloser
	cmd         *exec.Cmd
	processDone <-chan error
	closeOnce   sync.Once
}

func (connection *adapterConnection) Close() error {
	var closeErr error
	connection.closeOnce.Do(func() {
		closeErr = connection.ReadWriteCloser.Close()
		if connection.cmd != nil && connection.cmd.Process != nil {
			_ = connection.cmd.Process.Kill()
		}
	})
	return closeErr
}

type splitConnection struct {
	reader io.ReadCloser
	writer io.WriteCloser
}

func (connection *splitConnection) Read(buffer []byte) (int, error) {
	return connection.reader.Read(buffer)
}

func (connection *splitConnection) Write(buffer []byte) (int, error) {
	return connection.writer.Write(buffer)
}

func (connection *splitConnection) Close() error {
	return errors.Join(connection.reader.Close(), connection.writer.Close())
}

func startAdapter(ctx context.Context, plan Plan, output func(string, string)) (*adapterConnection, error) {
	cmd := exec.Command(plan.Adapter.Command, plan.Adapter.Args...)
	cmd.Dir = plan.ProjectDir
	cmd.Env = os.Environ()

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("debug adapter stderr: %w", err)
	}

	switch plan.Adapter.Transport {
	case TransportStdio:
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, fmt.Errorf("debug adapter stdin: %w", err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			_ = stdin.Close()
			return nil, fmt.Errorf("debug adapter stdout: %w", err)
		}
		if err := cmd.Start(); err != nil {
			_ = stdin.Close()
			_ = stdout.Close()
			return nil, fmt.Errorf("start debug adapter %s: %w", plan.Adapter.Name, err)
		}
		go copyAdapterOutput(stderr, "adapter stderr", output)
		return runningConnection(cmd, &splitConnection{reader: stdout, writer: stdin}), nil

	case TransportTCP:
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, fmt.Errorf("debug adapter stdout: %w", err)
		}
		if err := cmd.Start(); err != nil {
			_ = stdout.Close()
			return nil, fmt.Errorf("start debug adapter %s: %w", plan.Adapter.Name, err)
		}
		go copyAdapterOutput(stderr, "adapter stderr", output)

		ready := make(chan readyResult, 1)
		go readTCPAdapterOutput(stdout, plan.Adapter.ReadyPrefix, output, ready)

		startupCtx, cancel := context.WithTimeout(ctx, adapterStartupTimeout)
		defer cancel()
		select {
		case result := <-ready:
			if result.err != nil {
				_ = cmd.Process.Kill()
				return nil, fmt.Errorf("start debug adapter %s: %w", plan.Adapter.Name, result.err)
			}
			conn, err := (&net.Dialer{}).DialContext(startupCtx, "tcp", result.address)
			if err != nil {
				_ = cmd.Process.Kill()
				return nil, fmt.Errorf("connect to debug adapter %s at %s: %w", plan.Adapter.Name, result.address, err)
			}
			return runningConnection(cmd, conn), nil
		case <-startupCtx.Done():
			_ = cmd.Process.Kill()
			return nil, fmt.Errorf("start debug adapter %s: %w", plan.Adapter.Name, startupCtx.Err())
		}
	default:
		return nil, fmt.Errorf("debug adapter %s uses unsupported transport %q", plan.Adapter.Name, plan.Adapter.Transport)
	}
}

func runningConnection(cmd *exec.Cmd, connection io.ReadWriteCloser) *adapterConnection {
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		close(done)
	}()
	return &adapterConnection{ReadWriteCloser: connection, cmd: cmd, processDone: done}
}

type readyResult struct {
	address string
	err     error
}

func readTCPAdapterOutput(reader io.Reader, prefix string, output func(string, string), ready chan<- readyResult) {
	buffered := bufio.NewReader(reader)
	reported := false
	for {
		line, err := buffered.ReadString('\n')
		trimmed := strings.TrimSpace(line)
		if !reported && strings.HasPrefix(trimmed, prefix) {
			address := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
			if address == "" {
				ready <- readyResult{err: errors.New("adapter reported an empty listen address")}
			} else {
				ready <- readyResult{address: address}
			}
			reported = true
		} else if line != "" {
			output("stdout", line)
		}
		if err != nil {
			if !reported {
				if errors.Is(err, io.EOF) {
					err = errors.New("adapter exited before reporting its listen address")
				}
				ready <- readyResult{err: err}
			}
			return
		}
	}
}

func copyAdapterOutput(reader io.Reader, category string, output func(string, string)) {
	buffer := make([]byte, 32*1024)
	for {
		count, err := reader.Read(buffer)
		if count > 0 {
			output(category, string(buffer[:count]))
		}
		if err != nil {
			return
		}
	}
}
