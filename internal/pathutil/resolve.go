// Package pathutil resolves filesystem aliases across platforms.
package pathutil

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

// ResolveExistingPrefix resolves links in the longest existing prefix and
// appends any missing components. This lets callers check containment before
// creating an output path. Dangling links are errors, including junctions whose
// targets no longer exist; they must not be treated as missing directories.
func ResolveExistingPrefix(path string) (string, error) {
	if path == "" {
		return Resolve(path)
	}
	existing, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	var missing []string
	for {
		resolved, err := Resolve(existing)
		if err == nil {
			if len(missing) != 0 {
				info, statErr := os.Stat(existing)
				if statErr != nil {
					return "", statErr
				}
				if !info.IsDir() {
					return "", fmt.Errorf("existing parent %s is not a directory", existing)
				}
			}
			for _, component := range slices.Backward(missing) {
				resolved = filepath.Join(resolved, component)
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if _, statErr := os.Lstat(existing); statErr == nil {
			return "", fmt.Errorf("dangling or unresolvable link at %s: %w", existing, err)
		} else if !os.IsNotExist(statErr) {
			return "", statErr
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", err
		}
		missing = append(missing, filepath.Base(existing))
		existing = parent
	}
}
