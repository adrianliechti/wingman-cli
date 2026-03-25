package lsp

import (
	"net/url"
	"path/filepath"
	"strings"
)

func fileURI(path string) string {
	absPath := path
	if !isAbsolutePath(path) {
		resolvedPath, err := filepath.Abs(path)
		if err == nil {
			absPath = resolvedPath
		}
	}

	slashPath := filepath.ToSlash(absPath)

	if strings.HasPrefix(slashPath, "//") {
		hostPath := strings.TrimPrefix(slashPath, "//")
		host, rest, ok := strings.Cut(hostPath, "/")
		if !ok {
			rest = ""
		}

		return (&url.URL{Scheme: "file", Host: host, Path: "/" + rest}).String()
	}

	if hasWindowsDrivePrefix(slashPath) {
		slashPath = "/" + slashPath
	}

	return (&url.URL{Scheme: "file", Path: slashPath}).String()
}

func isAbsolutePath(path string) bool {
	slashPath := filepath.ToSlash(path)
	return filepath.IsAbs(path) || strings.HasPrefix(slashPath, "//") || hasWindowsDrivePrefix(slashPath)
}

func uriToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return uri
	}

	path := u.Path
	if u.Host != "" {
		path = "//" + u.Host + path
	} else if hasWindowsDrivePrefix(path) {
		path = path[1:]
	}

	return filepath.FromSlash(path)
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
