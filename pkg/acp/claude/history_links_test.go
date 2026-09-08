package claude

import (
	"path/filepath"
	"testing"

	"github.com/adrianliechti/wingman-agent/internal/testenv"
)

func TestHistoryProjectDirectoryLinks(t *testing.T) {
	testenv.UserHome(t)
	for _, kind := range []string{"symlink", "junction"} {
		t.Run(kind, func(t *testing.T) {
			target := t.TempDir()
			root := filepath.Join(t.TempDir(), "workspace")
			testenv.DirLink(t, kind, target, root)
			want := projectDirFor(target)
			if got := projectDirFor(root); want == "" || got != want {
				t.Fatalf("history project = %q, want %q", got, want)
			}
		})
	}
}
