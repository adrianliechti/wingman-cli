package dap

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPreLaunchProcessWaitsForHTTPAndStops(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	plan := Plan{
		ProjectDir: t.TempDir(),
		PreLaunch: &ProcessLaunch{
			Title: "test server", Command: executable,
			Args:     []string{"-test.run=^TestPreLaunchHelperProcess$", "--", strconv.Itoa(port)},
			ReadyURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		},
	}
	var output outputBuffer
	process, err := startDebugProcess(plan, output.append)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := process.waitReady(ctx, plan.PreLaunch.ReadyURL); err != nil {
		process.Close()
		t.Fatalf("waitReady: %v\noutput:\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "helper ready") {
		t.Fatalf("output = %q", output.String())
	}
	process.Close()
	select {
	case <-process.done:
	case <-time.After(5 * time.Second):
		t.Fatal("prelaunch process did not stop")
	}
}

func TestPreLaunchReadyCheckDoesNotFollowRedirects(t *testing.T) {
	var redirected atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected.Add(1)
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	process := &debugProcess{done: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := process.waitReady(ctx, source.URL); err != nil {
		t.Fatal(err)
	}
	if redirected.Load() != 0 {
		t.Fatal("prelaunch readiness probe followed an HTTP redirect")
	}
}

func TestPreLaunchHelperProcess(t *testing.T) {
	separator := -1
	for index, arg := range os.Args {
		if arg == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		return
	}
	port, err := strconv.Atoi(os.Args[separator+1])
	if err != nil {
		return
	}
	server := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", port), Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ready"))
	})}
	fmt.Println("helper ready")
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
