// Package pathutil resolves filesystem aliases across platforms.
package pathutil

import (
	"os"
	"path/filepath"
)

// Resolve returns the absolute path of an existing file or directory, following
// symbolic links and Windows junctions. It does not grant access to that path;
// callers must still enforce their filesystem boundaries when opening it.
func Resolve(path string) (string, error) {
	if path == "" {
		return "", &os.PathError{Op: "resolve", Path: path, Err: os.ErrNotExist}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return resolve(abs)
}
