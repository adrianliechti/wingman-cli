package devtools

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"
)

func TestLiveManagedArchiveDownloads(t *testing.T) {
	if os.Getenv("WINGMAN_LIVE_DEVTOOLS") == "" {
		t.Skip("set WINGMAN_LIVE_DEVTOOLS=1 to download managed release archives")
	}
	if _, err := codeLLDBAssetName(runtime.GOOS, runtime.GOARCH); err != nil {
		t.Skip(err)
	}
	if _, err := netCoreDbgAssetName(runtime.GOOS, runtime.GOARCH); err != nil {
		t.Skip(err)
	}

	manager := newManager(t.TempDir())
	manager.install = manager.installRecipe
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	changed, err := manager.Update(ctx, []Requirement{
		{Alternatives: []string{"codelldb"}},
		{Alternatives: []string{"netcoredbg"}},
		{Alternatives: []string{"jdtls"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("clean managed root was not updated")
	}
	for _, command := range []string{"codelldb", "netcoredbg", "jdtls"} {
		if manager.Resolve(command) == "" {
			t.Errorf("%s was not installed", command)
		}
	}
	if len(manager.JavaDebugBundles()) != 1 {
		t.Errorf("managed java-debug bundle was not installed")
	}
}
