package tooling

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Environment makes an absolute command's sibling executables visible to
// shebang launchers. This matters when a GUI process discovers a standard user
// tool directory that was not present in its inherited PATH. The directory of
// an env-style shebang interpreter is added for the same reason: Runnable
// accepts interpreters from standard user tool directories, so launches must
// resolve them too.
func Environment(command string, environment []string) []string {
	result := append([]string(nil), environment...)
	if !filepath.IsAbs(command) {
		return result
	}
	directories := []string{filepath.Dir(command)}
	if extra := interpreterDirectory(command); extra != "" {
		directories = append(directories, extra)
	}
	for index, item := range result {
		key, value, ok := strings.Cut(item, "=")
		if !ok || !sameEnvironmentKey(key, "PATH") {
			continue
		}
		for _, directory := range directories {
			if !pathContains(value, directory) {
				if value == "" {
					value = directory
				} else {
					value = directory + string(os.PathListSeparator) + value
				}
			}
		}
		result[index] = key + "=" + value
		return result
	}
	return append(result, "PATH="+strings.Join(directories, string(os.PathListSeparator)))
}

func sameEnvironmentKey(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

// interpreterDirectory returns the user tool directory holding an env-style
// shebang interpreter that the inherited PATH cannot resolve.
func interpreterDirectory(command string) string {
	if runtime.GOOS == "windows" {
		return ""
	}
	file, err := os.Open(command)
	if err != nil {
		return ""
	}
	defer file.Close()
	line, _ := bufio.NewReader(file).ReadString('\n')
	if !strings.HasPrefix(line, "#!") {
		return ""
	}
	fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "#!"))
	if len(fields) < 2 || filepath.Base(fields[0]) != "env" {
		return ""
	}
	interpreter := ""
	for _, field := range fields[1:] {
		if strings.HasPrefix(field, "-") || strings.Contains(field, "=") {
			continue
		}
		interpreter = field
		break
	}
	if interpreter == "" {
		return ""
	}
	if _, err := exec.LookPath(interpreter); err == nil {
		return ""
	}
	if resolved := findExecutable(userDirectories(), interpreter); resolved != "" {
		return filepath.Dir(resolved)
	}
	return ""
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
