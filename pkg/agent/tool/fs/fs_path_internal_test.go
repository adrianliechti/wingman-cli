package fs

import (
	"runtime"
	"testing"
)

func TestNormalizePathArg(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{`/F:\xxx\yyy`, `F:\xxx\yyy`},
		{`/f:/xxx/yyy`, `f:/xxx/yyy`},
		{`/F:\\go\\pkg\\mod`, `F:\\go\\pkg\\mod`},
		{`/F:/`, `F:/`},
		{`F:\xxx\yyy`, `F:\xxx\yyy`},
		{`F:relative`, `F:relative`},
		{`/F:relative`, `/F:relative`},
		{`\\server\share\file`, `\\server\share\file`},
		{`//server/share/file`, `//server/share/file`},
		{`\\?\F:\xxx\yyy`, `\\?\F:\xxx\yyy`},
		{`/usr/local`, `/usr/local`},
		{`/F:`, `/F:`},
		{`/\\server\share\file`, `/\\server\share\file`},
		{`/`, `/`},
		{``, ``},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			want := tt.path
			if runtime.GOOS == "windows" {
				want = tt.want
			}
			if got := normalizePathArg(tt.path); got != want {
				t.Fatalf("normalizePathArg(%q) = %q, want %q", tt.path, got, want)
			}
		})
	}
}
