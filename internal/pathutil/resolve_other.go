//go:build !windows

package pathutil

import "path/filepath"

func resolve(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
