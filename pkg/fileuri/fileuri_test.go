package fileuri_test

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/fileuri"
)

func TestWindowsDriveURIs(t *testing.T) {
	for _, path := range []string{"F:/go/pkg/mod", "/F:/go/pkg/mod", "F:/", "/F:/"} {
		t.Run(path, func(t *testing.T) {
			wantPath := strings.TrimPrefix(path, "/")
			wantURI := "file:///" + wantPath
			uri := fileuri.FromPath(path)
			if uri != wantURI {
				t.Fatalf("FromPath(%q) = %q, want %q", path, uri, wantURI)
			}
			if got, ok := fileuri.Path(uri); !ok || got != filepath.FromSlash(wantPath) {
				t.Fatalf("Path(%q) = (%q, %v), want %q", uri, got, ok, filepath.FromSlash(wantPath))
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-specific paths")
	}

	tests := []struct {
		path string
		want string
	}{
		{path: "/home/user/file.go", want: "file:///home/user/file.go"},
		{path: "/tmp/test.txt", want: "file:///tmp/test.txt"},
		{path: "/path/with spaces/file.go", want: "file:///path/with%20spaces/file.go"},
	}
	for _, test := range tests {
		uri := fileuri.FromPath(test.path)
		if uri != test.want {
			t.Errorf("FromPath(%q) = %q, want %q", test.path, uri, test.want)
		}
		if path, ok := fileuri.Path(uri); !ok || path != test.path {
			t.Errorf("Path(%q) = (%q, %v), want (%q, true)", uri, path, ok, test.path)
		}
	}
}

func TestPathRejectsNonFileURI(t *testing.T) {
	if path, ok := fileuri.Path("https://example.com/main.go"); ok || path != "" {
		t.Fatalf("Path(non-file URI) = (%q, %v)", path, ok)
	}
}
