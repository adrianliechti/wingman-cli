package pathutil

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func resolve(path string) (string, error) {
	// EvalSymlinks does not follow junctions in Go 1.23 and later. Open a
	// metadata handle so Windows resolves all reparse points in the path.
	name := path
	if !strings.HasPrefix(name, `\\?\`) && !strings.HasPrefix(name, `\\.\`) {
		if strings.HasPrefix(name, `\\`) {
			name = `\\?\UNC\` + name[2:]
		} else {
			name = `\\?\` + name
		}
	}
	ptr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return "", &os.PathError{Op: "resolve", Path: path, Err: err}
	}
	handle, err := windows.CreateFile(ptr, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", &os.PathError{Op: "resolve", Path: path, Err: err}
	}
	defer windows.CloseHandle(handle)

	buffer := make([]uint16, 260)
	for {
		// Zero selects FILE_NAME_NORMALIZED | VOLUME_NAME_DOS.
		n, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", &os.PathError{Op: "resolve", Path: path, Err: err}
		}
		if n >= uint32(len(buffer)) {
			buffer = make([]uint16, n+1)
			continue
		}
		resolved := windows.UTF16ToString(buffer[:n])
		if strings.HasPrefix(resolved, `\\?\UNC\`) {
			resolved = `\\` + resolved[len(`\\?\UNC\`):]
		} else if dos, ok := strings.CutPrefix(resolved, `\\?\`); ok && len(filepath.VolumeName(dos)) == 2 {
			resolved = dos
		}
		return filepath.Clean(resolved), nil
	}
}
