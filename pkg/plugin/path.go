package plugin

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// resolvePath canonicalises the longest existing prefix of path and re-appends
// the components that do not exist yet, so a target that a plugin has not
// created can still be checked for containment. A dangling symlink is an error
// rather than a resolvable path.
func resolvePath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		if err != nil {
			return "", fmt.Errorf("resolve %s: %w", path, err)
		}
		path = abs
	}

	existing := filepath.Clean(path)

	var missing []string

	for {
		resolved, err := filepath.EvalSymlinks(existing)

		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}

		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("resolve %s: %w", path, err)
		}

		if info, statErr := os.Lstat(existing); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("resolve %s: dangling symlink at %s", path, existing)
		}

		parent := filepath.Dir(existing)
		if parent == existing {
			return "", fmt.Errorf("resolve %s: no existing ancestor", path)
		}

		missing = append(missing, filepath.Base(existing))
		existing = parent
	}
}

// contains reports whether path is root itself or lies beneath it. Both are
// expected to be already resolved.
func contains(root, path string) bool {
	rel, err := filepath.Rel(root, path)

	if err != nil {
		return false
	}

	if rel == "." {
		return true
	}

	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveContained joins a plugin-relative value onto base, resolves it, and
// rejects anything that escapes root. base and root differ only for
// ${PLUGIN_DATA} paths, which resolve under the data directory.
func resolveContained(value, base, root string) (string, error) {
	path := value

	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}

	resolved, err := resolvePath(path)
	if err != nil {
		return "", err
	}

	if !contains(root, resolved) {
		return "", fmt.Errorf("%s escapes %s", value, root)
	}

	return resolved, nil
}

// relativePath reports whether value is a plugin-relative path as the spec
// defines it: it must begin with "./" and must not use backslashes, which are
// path separators on Windows and would not be portable.
func relativePath(value string) bool {
	suffix, ok := strings.CutPrefix(value, "./")
	return ok && suffix != "" && !strings.Contains(suffix, `\`)
}

// bareCommand reports whether value names an executable to be found through the
// platform's search rules rather than inside the plugin.
func bareCommand(value string) bool {
	if value == "" {
		return false
	}

	if strings.ContainsAny(value, `/\`) {
		return false
	}

	return runtime.GOOS != "windows" || filepath.VolumeName(value) == ""
}
