package fs

import "testing"

func TestMatchAllowedRootHandlesCaseFoldingLengthChanges(t *testing.T) {
	cases := []struct {
		name    string
		root    string
		path    string
		wantSub string
		wantOK  bool
	}{
		{"ascii nested", "/tmp/ok", "/tmp/ok/sub/file.txt", "sub/file.txt", true},
		{"exact root", "/tmp/ok", "/tmp/ok", "", true},
		{"sibling prefix is not a match", "/tmp/ok", "/tmp/okay/file.txt", "", false},
		{"lowercase shrinks encoded length", "/tmp/ẞẞ", "/tmp/ßß/x", "x", true},
		{"lowercase grows encoded length", "/tmp/İ", "/tmp/i/x", "x", true},
		{"case-insensitive ascii", "/tmp/Work", "/tmp/work/x", "x", true},
		{"unrelated root", "/tmp/ok", "/other/file.txt", "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rootClean, sub, ok := matchAllowedRootLiteral(cleanPath(c.path), []string{c.root})
			if ok != c.wantOK {
				t.Fatalf("match = %v, want %v", ok, c.wantOK)
			}
			if !c.wantOK {
				return
			}
			if rootClean != cleanPath(c.root) {
				t.Fatalf("rootClean = %q, want %q", rootClean, cleanPath(c.root))
			}
			if sub != c.wantSub {
				t.Fatalf("sub = %q, want %q", sub, c.wantSub)
			}
		})
	}
}
