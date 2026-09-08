package fs

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// fileChange is one entry in a best-effort atomic filesystem transaction.
// Every replacement is staged before any original is moved, and failures
// restore already-touched files in reverse order.
type fileChange struct {
	target        fileTarget
	displayPath   string
	before        []byte
	beforePresent bool
	beforeMode    os.FileMode
	after         []byte
	afterPresent  bool

	root       *os.Root
	path       string
	closeRoot  func()
	tempPath   string
	backupPath string
	installed  bool
}

type fileTransaction struct {
	changes []fileChange
}

func commitFileTransaction(workspaceRoot *os.Root, tx *fileTransaction) (err error) {
	for i := range tx.changes {
		change := &tx.changes[i]
		change.root, change.path, change.closeRoot, err = transactionLocation(workspaceRoot, change.target)
		if err != nil {
			closeTransactionRoots(tx)
			return fmt.Errorf("prepare %s: %w", change.displayPath, err)
		}
	}
	defer closeTransactionRoots(tx)

	cleanup := func() {
		for i := range tx.changes {
			change := &tx.changes[i]
			if change.tempPath != "" {
				_ = change.root.Remove(change.tempPath)
			}
		}
	}
	defer func() {
		if err != nil {
			cleanup()
		}
	}()

	// Stage every new version before moving any original out of place.
	for i := range tx.changes {
		change := &tx.changes[i]
		if !change.afterPresent {
			continue
		}
		dir := filepath.Dir(change.path)
		if dir != "." {
			if err := change.root.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("prepare directory for %s: %w", change.displayPath, err)
			}
		}
		change.tempPath = filepath.Join(dir, ".wingman-edit-"+uuid.NewString())
		file, openErr := change.root.OpenFile(change.tempPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if openErr != nil {
			return fmt.Errorf("stage %s: %w", change.displayPath, openErr)
		}
		_, writeErr := file.Write(change.after)
		closeErr := file.Close()
		if writeErr != nil {
			return fmt.Errorf("stage %s: %w", change.displayPath, writeErr)
		}
		if closeErr != nil {
			return fmt.Errorf("stage %s: %w", change.displayPath, closeErr)
		}
		mode := change.beforeMode.Perm()
		if mode == 0 {
			mode = 0o644
		}
		if err := change.root.Chmod(change.tempPath, mode); err != nil {
			return fmt.Errorf("stage permissions for %s: %w", change.displayPath, err)
		}
	}

	rollback := func() {
		for i := len(tx.changes) - 1; i >= 0; i-- {
			change := &tx.changes[i]
			if change.installed {
				_ = change.root.Remove(change.path)
			}
			if change.backupPath != "" {
				_ = change.root.Rename(change.backupPath, change.path)
			}
		}
	}

	for i := range tx.changes {
		change := &tx.changes[i]
		if change.beforePresent {
			change.backupPath = filepath.Join(filepath.Dir(change.path), ".wingman-backup-"+uuid.NewString())
			if err := change.root.Rename(change.path, change.backupPath); err != nil {
				rollback()
				return fmt.Errorf("prepare %s: %w", change.displayPath, err)
			}
		}
		if change.afterPresent {
			if err := change.root.Rename(change.tempPath, change.path); err != nil {
				rollback()
				return fmt.Errorf("install %s: %w", change.displayPath, err)
			}
			change.tempPath = ""
			change.installed = true
		}
	}

	for i := range tx.changes {
		change := &tx.changes[i]
		if change.backupPath != "" {
			_ = change.root.Remove(change.backupPath)
			change.backupPath = ""
		}
	}
	cleanup()
	return nil
}

func closeTransactionRoots(tx *fileTransaction) {
	for i := range tx.changes {
		if tx.changes[i].closeRoot != nil {
			tx.changes[i].closeRoot()
			tx.changes[i].closeRoot = nil
		}
	}
}

// transactionLocation turns all supported target forms into an os.Root plus
// a relative path. The symlink fallback preserves the existing file-tool
// behavior for absolute in-root links while keeping containment enforced by
// os.Root during staging and renames.
func transactionLocation(workspaceRoot *os.Root, target fileTarget) (*os.Root, string, func(), error) {
	root, path, closeRoot, err := fileTargetRoot(workspaceRoot, target)
	if err != nil {
		return nil, "", nil, err
	}
	if root == nil {
		root, path, closeRoot, err = absoluteTransactionLocation(path)
		if err != nil {
			return nil, "", nil, err
		}
	}
	path = filepath.Clean(filepath.FromSlash(path))

	if sub, ok := resolveRootPath(root, path); ok {
		path = filepath.Clean(sub)
	}
	return root, path, closeRoot, nil
}

func absoluteTransactionLocation(path string) (*os.Root, string, func(), error) {
	path = filepath.Clean(path)
	volume := filepath.VolumeName(path)
	base := string(filepath.Separator)
	if volume != "" {
		base = volume + string(filepath.Separator)
	}
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return nil, "", nil, err
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, "", nil, err
	}
	return root, rel, func() { _ = root.Close() }, nil
}
