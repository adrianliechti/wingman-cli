package debugadapter

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/dap"
)

func TestLiveDebugpyLaunch(t *testing.T) {
	if os.Getenv("WINGMAN_LIVE_DAP") == "" {
		t.Skip("set WINGMAN_LIVE_DAP=1 to run a real debugpy session")
	}
	root := t.TempDir()
	program := filepath.Join(root, "main.py")
	if err := os.WriteFile(program, []byte(`def work(value):
    return value + 1

print(work(41))
`), 0o644); err != nil {
		t.Fatal(err)
	}

	registry := NewRegistry()
	manager := dap.NewManager(root, registry.Descriptors()...)
	defer manager.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	adapters, err := manager.Adapters(ctx)
	if err != nil {
		t.Fatal(err)
	}
	available := false
	for _, adapter := range adapters {
		if adapter.Name == "debugpy" {
			available = true
			break
		}
	}
	if !available {
		t.Skip("debugpy is not installed")
	}

	session, err := manager.Start(ctx, dap.StartOptions{
		Adapter:    "debugpy",
		ProjectDir: root,
		Configuration: map[string]any{
			"type":       "python",
			"program":    program,
			"cwd":        root,
			"justMyCode": true,
		},
		Breakpoints: map[string][]dap.SourceBreakpoint{
			program: {{Line: 2}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	status := session.Status()
	if status.State != dap.StateStopped {
		status, _ = session.WaitForStop(ctx, session.StateEpoch())
	}
	if status.State != dap.StateStopped {
		t.Fatalf("status = %+v\noutput:\n%s", status, session.Output())
	}
	frames, _, err := session.StackTrace(ctx, 0, 0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) == 0 || frames[0].Source == nil || filepath.Clean(frames[0].Source.Path) != program {
		t.Fatalf("frames = %+v", frames)
	}
	if err := manager.Stop(ctx, session.ID()); err != nil {
		t.Fatal(err)
	}
}
