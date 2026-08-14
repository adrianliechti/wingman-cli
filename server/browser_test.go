package server

import (
	"reflect"
	"testing"
)

func TestRevealCommand(t *testing.T) {
	tests := []struct {
		name   string
		goos   string
		target string
		isDir  bool
		want   string
		args   []string
	}{
		{"macOS file", "darwin", "/work/file.go", false, "open", []string{"-R", "/work/file.go"}},
		{"Windows folder", "windows", `C:\\work\\folder`, true, "explorer.exe", []string{`/select,C:\\work\\folder`}},
		{"Linux file", "linux", "/work/folder/file.go", false, "xdg-open", []string{"/work/folder"}},
		{"Linux folder", "linux", "/work/folder", true, "xdg-open", []string{"/work/folder"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, args := revealCommand(test.goos, test.target, test.isDir)
			if got != test.want || !reflect.DeepEqual(args, test.args) {
				t.Fatalf("revealCommand() = %q, %#v; want %q, %#v", got, args, test.want, test.args)
			}
		})
	}
}
