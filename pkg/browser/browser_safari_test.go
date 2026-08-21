package browser

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSafariMajorVersion(t *testing.T) {
	tests := map[string]int{
		"Included with Safari 26.6.2 (21624.5.1.11.3)": 26,
		"Included with Safari 27 beta":                 27,
		"Safari Technology Preview 247, Safari 27.0":   247,
		"unknown": 0,
	}
	for input, want := range tests {
		if got := safariMajorVersion(input); got != want {
			t.Errorf("safariMajorVersion(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestLoadSafariScreenshotConfinesAndCleansTemporaryFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "com.apple.WebDriver")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "safari-mcp-test.png")
	if err := os.WriteFile(path, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := loadSafariScreenshotFrom("Saved screenshot to '"+path+"' (3 bytes).", root)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "png" {
		t.Fatalf("screenshot = %q", data)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("temporary screenshot was not removed: %v", err)
	}

	outside := filepath.Join(root, "safari-mcp-secret.png")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSafariScreenshotFrom("Saved screenshot to '"+outside+"'.", root); err == nil {
		t.Fatal("accepted a Safari screenshot outside com.apple.WebDriver")
	}
}
