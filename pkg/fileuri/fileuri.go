// Package fileuri converts native filesystem paths to and from file URIs. It
// contains no workspace-containment or external-file policy.
package fileuri

import (
	"net/url"
	"path/filepath"
	"strings"
)

func FromPath(path string) string {
	absPath := path
	if !isAbsolutePath(path) {
		if resolved, err := filepath.Abs(path); err == nil {
			absPath = resolved
		}
	}

	slashPath := filepath.ToSlash(absPath)
	if hostPath, ok := strings.CutPrefix(slashPath, "//"); ok {
		host, rest, found := strings.Cut(hostPath, "/")
		if !found {
			rest = ""
		}
		return (&url.URL{Scheme: "file", Host: host, Path: "/" + rest}).String()
	}

	if hasWindowsDrivePrefix(slashPath) {
		slashPath = "/" + slashPath
	}
	return (&url.URL{Scheme: "file", Path: slashPath}).String()
}

// Path returns a native filesystem path only for valid file URIs.
func Path(value string) (string, bool) {
	uri, err := url.Parse(value)
	if err != nil || uri.Scheme != "file" {
		return "", false
	}

	path := uri.Path
	if uri.Host != "" {
		path = "//" + uri.Host + path
	} else if hasWindowsDrivePrefix(path) {
		path = path[1:]
	}
	return filepath.FromSlash(path), true
}

func isAbsolutePath(path string) bool {
	slashPath := filepath.ToSlash(path)
	return filepath.IsAbs(path) || strings.HasPrefix(slashPath, "//") || hasWindowsDrivePrefix(slashPath)
}

func hasWindowsDrivePrefix(path string) bool {
	if len(path) < 3 {
		return false
	}
	start := 0
	if path[0] == '/' {
		start = 1
	}
	if len(path[start:]) < 2 {
		return false
	}
	drive := path[start]
	if (drive < 'A' || drive > 'Z') && (drive < 'a' || drive > 'z') {
		return false
	}
	return path[start+1] == ':'
}
