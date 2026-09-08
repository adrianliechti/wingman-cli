package pathutil

import (
	"io/fs"
	"os"
	"path/filepath"
)

// WalkDir follows links in the selected root, including Windows junctions,
// while preserving the caller's path spelling in callbacks. Like
// filepath.WalkDir, it does not follow links encountered beneath the root.
// Directory reads are anchored to an os.Root so a directory replaced by a link
// during the walk cannot redirect traversal outside the selected root.
func WalkDir(root string, visit fs.WalkDirFunc) error {
	info, err := os.Stat(root)
	if err == nil && info.IsDir() {
		var dir *os.Root
		dir, err = os.OpenRoot(root)
		if err == nil {
			defer dir.Close()
			return fs.WalkDir(dir.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
				path := root
				if name != "." {
					path = filepath.Join(root, filepath.FromSlash(name))
				} else if entry != nil {
					entry = fs.FileInfoToDirEntry(info)
				}
				return visit(path, entry, walkErr)
			})
		}
	}
	var entry fs.DirEntry
	if info != nil {
		entry = fs.FileInfoToDirEntry(info)
	}
	err = visit(root, entry, err)
	if err == fs.SkipDir || err == fs.SkipAll {
		return nil
	}
	return err
}
