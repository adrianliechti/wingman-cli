package plugin

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/adrianliechti/wingman-agent/internal/pathutil"
)

// resolvePath canonicalises the longest existing prefix of path and re-appends
// the components that do not exist yet, so a target that a plugin has not
// created can still be checked for containment. A dangling symlink is an error
// rather than a resolvable path.
func resolvePath(path string) (string, error) {
	resolved, err := pathutil.ResolveExistingPrefix(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", path, err)
	}
	return resolved, nil
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
