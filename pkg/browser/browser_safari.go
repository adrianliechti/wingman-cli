package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func discoverSafari() discoveredProvider {
	if runtime.GOOS != "darwin" {
		return discoveredProvider{setup: "Safari MCP is available on macOS with Safari 27+ or Safari Technology Preview 247+."}
	}
	candidates := []string{
		"/Applications/Safari Technology Preview.app/Contents/MacOS/safaridriver",
		"/usr/bin/safaridriver",
		"/System/Cryptexes/App/usr/bin/safaridriver",
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append([]string{filepath.Join(home, "Applications", "Safari Technology Preview.app", "Contents", "MacOS", "safaridriver")}, candidates...)
	}
	for _, candidate := range candidates {
		if !safariMCPAvailable(candidate) {
			continue
		}
		return discoveredProvider{
			available: true, command: candidate, args: []string{"--mcp"},
			setup: "Enable Safari Settings › Developer › Allow remote automation and external agents before connecting.",
		}
	}
	return discoveredProvider{setup: "Install Safari 27+ or Safari Technology Preview 247+, then enable remote automation and external agents in Safari's Developer settings."}
}

func safariMCPAvailable(path string) bool {
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		return false
	}
	return safariMajorVersion(string(output)) >= 27
}

func safariMajorVersion(version string) int {
	for _, field := range strings.FieldsFunc(version, func(r rune) bool { return r < '0' || r > '9' }) {
		major, err := strconv.Atoi(field)
		if err == nil {
			return major
		}
	}
	return 0
}

func loadSafariScreenshot(message string) ([]byte, error) {
	return loadSafariScreenshotFrom(message, os.TempDir())
}

func loadSafariScreenshotFrom(message, tempRoot string) ([]byte, error) {
	const prefix = "Saved screenshot to '"
	start := strings.Index(message, prefix)
	if start < 0 {
		return nil, fmt.Errorf("Safari did not return an attached screenshot%s", textSuffix(message))
	}
	start += len(prefix)
	end := strings.IndexByte(message[start:], '\'')
	if end < 0 {
		return nil, errors.New("Safari returned an invalid screenshot path")
	}
	reported := message[start : start+end]
	resolved, err := filepath.EvalSymlinks(reported)
	if err != nil {
		return nil, fmt.Errorf("resolve Safari screenshot: %w", err)
	}
	root, err := filepath.EvalSymlinks(tempRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve temporary directory: %w", err)
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, errors.New("Safari returned a screenshot outside the temporary directory")
	}
	base := filepath.Base(resolved)
	if filepath.Base(filepath.Dir(resolved)) != "com.apple.WebDriver" ||
		!strings.HasPrefix(base, "safari-mcp-") || strings.ToLower(filepath.Ext(base)) != ".png" {
		return nil, errors.New("Safari returned an unexpected screenshot path")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("inspect Safari screenshot: %w", err)
	}
	if info.IsDir() || info.Size() <= 0 || info.Size() > 20<<20 {
		return nil, fmt.Errorf("Safari screenshot has invalid size %d", info.Size())
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("read Safari screenshot: %w", err)
	}
	_ = os.Remove(resolved)
	return data, nil
}
