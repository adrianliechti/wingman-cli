package fs

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestMatchAllowedRootHandlesCaseFoldingLengthChanges(t *testing.T) {
	volume := filepath.VolumeName(t.TempDir())
	foldCase := runtime.GOOS == "darwin" || runtime.GOOS == "windows"
	cases := []struct {
		name    string
		root    string
		path    string
		wantSub string
		wantOK  bool
	}{
		{"ascii nested", "/tmp/ok", "/tmp/ok/sub/file.txt", "sub/file.txt", true},
		{"exact root", "/tmp/ok", "/tmp/ok", "", true},
		{"volume root", "/", "/tmp/ok/file.txt", "tmp/ok/file.txt", true},
		{"sibling prefix is not a match", "/tmp/ok", "/tmp/okay/file.txt", "", false},
		{"lowercase shrinks encoded length", "/tmp/ẞẞ", "/tmp/ßß/x", "x", foldCase},
		{"lowercase grows encoded length", "/tmp/İ", "/tmp/i/x", "x", foldCase},
		{"case-insensitive ascii", "/tmp/Work", "/tmp/work/x", "x", foldCase},
		{"unrelated root", "/tmp/ok", "/other/file.txt", "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := filepath.Clean(volume + c.root)
			path := filepath.Clean(volume + c.path)
			rootClean, sub, ok := matchAllowedRootLiteral(path, []string{root})
			if ok != c.wantOK {
				t.Fatalf("match = %v, want %v", ok, c.wantOK)
			}
			if !c.wantOK {
				return
			}
			if rootClean != root {
				t.Fatalf("rootClean = %q, want %q", rootClean, root)
			}
			if sub != filepath.FromSlash(c.wantSub) {
				t.Fatalf("sub = %q, want %q", sub, c.wantSub)
			}
		})
	}
}

func TestMatchAllowedRootIgnoresEmptyRoots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.txt")
	if root, sub, ok := matchAllowedRoot(path, []string{""}); ok {
		t.Fatalf("empty allowed root matched %q as (%q, %q)", path, root, sub)
	}
}
