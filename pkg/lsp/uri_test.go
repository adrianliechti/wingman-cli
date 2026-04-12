package lsp

import (
	"runtime"
	"testing"
)

func TestFileURI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-specific paths")
	}

	tests := []struct {
		path string
		want string
	}{
		{"/home/user/file.go", "file:///home/user/file.go"},
		{"/tmp/test.txt", "file:///tmp/test.txt"},
		{"/path/with spaces/file.go", "file:///path/with%20spaces/file.go"},
	}

	for _, tt := range tests {
		got := FileURI(tt.path)
		if got != tt.want {
			t.Errorf("FileURI(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestUriToPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-specific paths")
	}

	tests := []struct {
		uri  string
		want string
	}{
		{"file:///home/user/file.go", "/home/user/file.go"},
		{"file:///tmp/test.txt", "/tmp/test.txt"},
		{"file:///path/with%20spaces/file.go", "/path/with spaces/file.go"},
	}

	for _, tt := range tests {
		got := uriToPath(tt.uri)
		if got != tt.want {
			t.Errorf("uriToPath(%q) = %q, want %q", tt.uri, got, tt.want)
		}
	}
}

func TestFileURI_Roundtrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-specific paths")
	}

	paths := []string{
		"/home/user/project/main.go",
		"/tmp/test file.go",
		"/var/log/app.log",
	}

	for _, path := range paths {
		uri := FileURI(path)
		got := uriToPath(uri)
		if got != path {
			t.Errorf("roundtrip failed: %q -> %q -> %q", path, uri, got)
		}
	}
}
