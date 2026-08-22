package tooling

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Environment makes an absolute command's sibling executables visible to
// shebang launchers. This matters when a GUI process discovers a standard user
// tool directory that was not present in its inherited PATH.
func Environment(command string, environment []string) []string {
	result := append([]string(nil), environment...)
	if !filepath.IsAbs(command) {
		return result
	}
	directory := filepath.Dir(command)
	for index, item := range result {
		key, value, ok := strings.Cut(item, "=")
		if !ok || !strings.EqualFold(key, "PATH") {
			continue
		}
		if !pathContains(value, directory) {
			value = directory + string(os.PathListSeparator) + value
		}
		result[index] = key + "=" + value
		return result
	}
	return append(result, "PATH="+directory)
}

func pathContains(value, directory string) bool {
	directory = filepath.Clean(directory)
	for _, entry := range filepath.SplitList(value) {
		if samePath(filepath.Clean(entry), directory) {
			return true
		}
	}
	return false
}

func samePath(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}
