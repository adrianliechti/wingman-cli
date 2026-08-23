package changes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	formatdiff "github.com/go-git/go-git/v5/plumbing/format/diff"
	"github.com/go-git/go-git/v5/plumbing/object"
	utildiff "github.com/go-git/go-git/v5/utils/diff"
)

var ErrClosed = errors.New("change tracker closed")
var ErrNoDiff = errors.New("diff not found")

type FileStatus int

const (
	StatusAdded FileStatus = iota
	StatusModified
	StatusDeleted
)

type FileDiff struct {
	Path         string
	OriginalPath string
	Status       FileStatus
	Patch        string
	Original     string
	Modified     string
}

type DiffLayer string

const (
	DiffCombined DiffLayer = ""
	DiffStaged   DiffLayer = "staged"
	DiffUnstaged DiffLayer = "unstaged"
)

type statusEntry struct {
	path         string
	originalPath string
	index        git.StatusCode
	worktree     git.StatusCode
	conflict     bool
}

type Manager struct {
	workingDir string
	prefix     string

	repo     *git.Repository
	worktree *git.Worktree

	initDone chan struct{}
	initErr  error

	mu     sync.Mutex
	closed bool
}

func New(workingDir string) *Manager {
	if absolute, err := filepath.Abs(workingDir); err == nil {
		workingDir = absolute
	}
	m := &Manager{
		workingDir: workingDir,
		initDone:   make(chan struct{}),
	}
	go m.init()
	return m
}

func (m *Manager) init() {
	defer close(m.initDone)

	repo, err := git.PlainOpenWithOptions(m.workingDir, &git.PlainOpenOptions{
		DetectDotGit:          true,
		EnableDotGitCommonDir: true,
	})
	if err != nil {
		m.initErr = fmt.Errorf("open Git repository: %w", err)
		return
	}
	worktree, err := repo.Worktree()
	if err != nil {
		m.initErr = fmt.Errorf("open Git worktree: %w", err)
		return
	}
	root, err := repositoryRoot(m.workingDir)
	if err != nil {
		m.initErr = err
		return
	}
	rel, err := filepath.Rel(root, m.workingDir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		m.initErr = errors.New("workspace is outside the Git worktree")
		return
	}
	if rel != "." {
		m.prefix = filepath.ToSlash(rel) + "/"
	}
	m.repo = repo
	m.worktree = worktree
}

func (m *Manager) ready() error {
	<-m.initDone
	return m.initErr
}

func (m *Manager) Diffs(ctx context.Context) ([]FileDiff, error) {
	return m.DiffsLayer(ctx, DiffCombined)
}

// DiffsLayer returns a consistent snapshot of every change in one Git layer.
// The manager lock stays held while the status, HEAD tree, and file contents
// are read so callers such as commit-message generation never mix snapshots.
func (m *Manager) DiffsLayer(ctx context.Context, layer DiffLayer) ([]FileDiff, error) {
	if err := m.ready(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if layer != DiffCombined && layer != DiffStaged && layer != DiffUnstaged {
		return nil, errors.New("invalid diff layer")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrClosed
	}

	entries, err := m.status()
	if err != nil {
		return nil, err
	}
	head, err := m.headTree()
	if err != nil {
		return nil, err
	}
	diffs := make([]FileDiff, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if layer == DiffStaged && (entry.index == git.Unmodified || entry.index == git.Untracked) {
			continue
		}
		if layer == DiffUnstaged && entry.worktree == git.Unmodified {
			continue
		}
		diff, err := m.fileDiff(ctx, head, entry, layer)
		if err != nil {
			return nil, err
		}
		diffs = append(diffs, diff)
	}
	return diffs, nil
}

func (m *Manager) Diff(ctx context.Context, path string, layer DiffLayer) (FileDiff, error) {
	if err := m.ready(); err != nil {
		return FileDiff{}, err
	}
	if err := ctx.Err(); err != nil {
		return FileDiff{}, err
	}
	if layer != DiffCombined && layer != DiffStaged && layer != DiffUnstaged {
		return FileDiff{}, errors.New("invalid diff layer")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return FileDiff{}, ErrClosed
	}

	entries, err := m.status()
	if err != nil {
		return FileDiff{}, err
	}
	for _, entry := range entries {
		if entry.path != path {
			continue
		}
		if layer == DiffStaged && (entry.index == git.Unmodified || entry.index == git.Untracked) {
			return FileDiff{}, fmt.Errorf("%w: path has no staged change", ErrNoDiff)
		}
		if layer == DiffUnstaged && entry.worktree == git.Unmodified {
			return FileDiff{}, fmt.Errorf("%w: path has no unstaged change", ErrNoDiff)
		}
		head, err := m.headTree()
		if err != nil {
			return FileDiff{}, err
		}
		return m.fileDiff(ctx, head, entry, layer)
	}
	return FileDiff{}, fmt.Errorf("%w: no change for path", ErrNoDiff)
}

func (m *Manager) Revert(ctx context.Context, path string) error {
	if err := m.ready(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}

	entries, err := m.status()
	if err != nil {
		return err
	}
	var match *statusEntry
	for i := range entries {
		if entries[i].path == path {
			match = &entries[i]
			break
		}
	}
	if match == nil {
		return errors.New("no change for path")
	}
	if match.worktree == git.Unmodified {
		return errors.New("path has no unstaged change")
	}
	return m.restoreWorktreeFromIndex(match.path)
}

func (m *Manager) Fingerprint(ctx context.Context) uint64 {
	if err := m.ready(); err != nil || ctx.Err() != nil {
		return 0
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0
	}

	entries, err := m.status()
	if err != nil {
		return 0
	}
	h := fnv.New64a()
	if head, err := m.repo.Head(); err == nil {
		_, _ = fmt.Fprintf(h, "head\x00%s\x00%s\n", head.Name(), head.Hash())
	}
	index, _ := m.repo.Storer.Index()
	for _, entry := range entries {
		_, _ = fmt.Fprintf(h, "%s\x00%s\x00%c%c\x00%t\n", entry.path, entry.originalPath, entry.index, entry.worktree, entry.conflict)
		if index != nil {
			if indexed, err := index.Entry(m.objectPath(entry.path)); err == nil {
				_, _ = fmt.Fprintf(h, "index\x00%s\x00%o\n", indexed.Hash, indexed.Mode)
			}
		}
		if info, err := os.Lstat(filepath.Join(m.workingDir, filepath.FromSlash(entry.path))); err == nil {
			_, _ = fmt.Fprintf(h, "worktree\x00%d\x00%d\x00%o\n", info.Size(), info.ModTime().UnixNano(), info.Mode())
		}
	}
	return h.Sum64()
}

func (m *Manager) Close() {
	<-m.initDone
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
}

func (m *Manager) fileDiff(ctx context.Context, head *object.Tree, entry statusEntry, layer DiffLayer) (FileDiff, error) {
	originalPath := entry.path
	if entry.originalPath != "" {
		originalPath = entry.originalPath
	}
	original, originalMode, headExists, err := treeFile(m.repo, head, m.objectPath(originalPath))
	if err != nil {
		return FileDiff{}, err
	}
	working, workingMode, workingExists, err := m.worktreeFile(entry.path)
	if err != nil {
		return FileDiff{}, err
	}

	modified, modifiedMode, modifiedExists := working, workingMode, workingExists
	if !entry.conflict {
		switch layer {
		case DiffStaged:
			modified, modifiedMode, modifiedExists, err = m.indexFile(m.objectPath(entry.path))
		case DiffUnstaged:
			originalPath = entry.path
			original, originalMode, headExists, err = m.indexFile(m.objectPath(entry.path))
		case DiffCombined:
			// When the filesystem matches HEAD but the index does not, show the index.
			// Otherwise show the combined HEAD-to-filesystem change.
			showIndex := entry.index != git.Unmodified && entry.index != git.Untracked &&
				headExists == workingExists && originalMode == workingMode && bytes.Equal(original, working)
			if showIndex {
				modified, modifiedMode, modifiedExists, err = m.indexFile(m.objectPath(entry.path))
			}
		}
		if err != nil {
			return FileDiff{}, err
		}
	}

	status := StatusModified
	if !headExists {
		status = StatusAdded
	}
	if !modifiedExists {
		status = StatusDeleted
	}
	patch, err := makePatch(ctx, originalPath, entry.path, status, original, originalMode, modified, modifiedMode)
	if err != nil {
		return FileDiff{}, err
	}

	diff := FileDiff{Path: entry.path, OriginalPath: entry.originalPath, Status: status, Patch: patch}
	if !isBinary(original) && !isBinary(modified) {
		diff.Original = string(original)
		diff.Modified = string(modified)
	}
	return diff, nil
}

func (m *Manager) restoreWorktreeFromIndex(path string) error {
	objectPath := m.objectPath(path)
	data, mode, exists, err := m.indexFile(objectPath)
	if err != nil {
		return err
	}
	if !exists {
		return m.remove(path)
	}
	if mode == filemode.Submodule {
		return errors.New("discarding submodule changes is not supported")
	}

	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return errors.New("invalid change path")
	}
	target := filepath.Join(m.workingDir, clean)
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("remove changed file: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create parent directory: %w", err)
	}
	if mode == filemode.Symlink {
		if err := os.Symlink(string(data), target); err != nil {
			return fmt.Errorf("restore symlink: %w", err)
		}
		return nil
	}
	perm := os.FileMode(0o644)
	if mode == filemode.Executable {
		perm = 0o755
	}
	if err := os.WriteFile(target, data, perm); err != nil {
		return fmt.Errorf("restore changed file: %w", err)
	}
	return nil
}

func (m *Manager) status() ([]statusEntry, error) {
	status, err := m.worktree.Status()
	if err != nil {
		return nil, fmt.Errorf("read Git status: %w", err)
	}
	entries := make([]statusEntry, 0, len(status))
	for repoPath, fileStatus := range status {
		if fileStatus.Staging == git.Unmodified && fileStatus.Worktree == git.Unmodified {
			continue
		}

		path := repoPath
		originalPath := ""
		if fileStatus.Staging == git.Renamed && fileStatus.Extra != "" {
			originalPath = repoPath
			path = fileStatus.Extra
		}
		rel, ok := m.scopePath(path)
		if !ok {
			continue
		}
		originalRel := ""
		if originalPath != "" {
			originalRel, _ = m.scopePath(originalPath)
		}
		entries = append(entries, statusEntry{
			path:         rel,
			originalPath: originalRel,
			index:        fileStatus.Staging,
			worktree:     fileStatus.Worktree,
			conflict:     fileStatus.Staging == git.UpdatedButUnmerged || fileStatus.Worktree == git.UpdatedButUnmerged,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	return entries, nil
}

func (m *Manager) headTree() (*object.Tree, error) {
	head, err := m.repo.Head()
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Git HEAD: %w", err)
	}
	commit, err := m.repo.CommitObject(head.Hash())
	if err != nil {
		return nil, fmt.Errorf("read HEAD commit: %w", err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("read HEAD tree: %w", err)
	}
	return tree, nil
}

func (m *Manager) indexFile(path string) ([]byte, filemode.FileMode, bool, error) {
	index, err := m.repo.Storer.Index()
	if err != nil {
		return nil, filemode.Empty, false, fmt.Errorf("read Git index: %w", err)
	}
	entry, err := index.Entry(path)
	if err != nil || entry.Hash.IsZero() {
		return nil, filemode.Empty, false, nil
	}
	data, err := blobContents(m.repo, entry.Hash, entry.Mode)
	if err != nil {
		return nil, filemode.Empty, false, fmt.Errorf("read indexed file %s: %w", path, err)
	}
	return data, entry.Mode, true, nil
}

func treeFile(repo *git.Repository, tree *object.Tree, path string) ([]byte, filemode.FileMode, bool, error) {
	if tree == nil {
		return nil, filemode.Empty, false, nil
	}
	entry, err := tree.FindEntry(path)
	if errors.Is(err, object.ErrEntryNotFound) || errors.Is(err, object.ErrDirectoryNotFound) {
		return nil, filemode.Empty, false, nil
	}
	if errors.Is(err, plumbing.ErrObjectNotFound) {
		// FindEntry tries to load every intermediate component as a tree. If
		// one is a file, go-git returns ErrObjectNotFound even though the
		// repository is healthy and the requested path is simply absent.
		blocked, lookupErr := treePathBlockedByFile(repo, tree, path)
		if lookupErr != nil {
			return nil, filemode.Empty, false, fmt.Errorf("inspect tree entry %s: %w", path, lookupErr)
		}
		if blocked {
			return nil, filemode.Empty, false, nil
		}
	}
	if err != nil {
		return nil, filemode.Empty, false, fmt.Errorf("read tree entry %s: %w", path, err)
	}
	if entry.Mode == filemode.Dir {
		return nil, filemode.Empty, false, nil
	}
	data, err := blobContents(repo, entry.Hash, entry.Mode)
	if err != nil {
		return nil, filemode.Empty, false, fmt.Errorf("read HEAD file %s: %w", path, err)
	}
	return data, entry.Mode, true, nil
}

func treePathBlockedByFile(repo *git.Repository, tree *object.Tree, path string) (bool, error) {
	parts := strings.Split(path, "/")
	for _, part := range parts[:len(parts)-1] {
		var entry *object.TreeEntry
		for i := range tree.Entries {
			if tree.Entries[i].Name == part {
				entry = &tree.Entries[i]
				break
			}
		}
		if entry == nil {
			return false, nil
		}
		if entry.Mode != filemode.Dir {
			return true, nil
		}
		var err error
		tree, err = object.GetTree(repo.Storer, entry.Hash)
		if err != nil {
			return false, err
		}
	}
	return false, nil
}

func blobContents(repo *git.Repository, hash plumbing.Hash, mode filemode.FileMode) ([]byte, error) {
	if mode == filemode.Submodule {
		return []byte("Subproject commit " + hash.String() + "\n"), nil
	}
	blob, err := object.GetBlob(repo.Storer, hash)
	if err != nil {
		return nil, err
	}
	reader, err := blob.Reader()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func workingFile(path string) ([]byte, filemode.FileMode, bool, error) {
	info, err := os.Lstat(path)
	if isMissingPathError(err) {
		return nil, filemode.Empty, false, nil
	}
	if err != nil {
		return nil, filemode.Empty, false, err
	}
	if info.IsDir() {
		return nil, filemode.Submodule, true, nil
	}
	mode, err := filemode.NewFromOSFileMode(info.Mode())
	if err != nil {
		return nil, filemode.Empty, false, err
	}
	if mode == filemode.Symlink {
		target, err := os.Readlink(path)
		return []byte(target), mode, true, err
	}
	data, err := os.ReadFile(path)
	return data, mode, true, err
}

func (m *Manager) worktreeFile(path string) ([]byte, filemode.FileMode, bool, error) {
	blocked, err := worktreePathBlockedByFile(m.workingDir, path)
	if err != nil {
		return nil, filemode.Empty, false, err
	}
	if blocked {
		return nil, filemode.Empty, false, nil
	}
	data, mode, exists, err := workingFile(filepath.Join(m.workingDir, filepath.FromSlash(path)))
	if err != nil || !exists || mode != filemode.Submodule {
		return data, mode, exists, err
	}
	indexed, indexedMode, indexedExists, err := m.indexFile(m.objectPath(path))
	if err != nil {
		return nil, filemode.Empty, false, err
	}
	if indexedExists && indexedMode == filemode.Submodule {
		return indexed, indexedMode, true, nil
	}
	return nil, filemode.Empty, false, nil
}

func worktreePathBlockedByFile(root, path string) (bool, error) {
	parts := strings.Split(filepath.FromSlash(path), string(filepath.Separator))
	current := root
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if isMissingPathError(err) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		// Git treats symlinks as files. Do not follow one in an intermediate
		// component when a directory-to-symlink change removes nested paths.
		if !info.IsDir() {
			return true, nil
		}
	}
	return false, nil
}

func isMissingPathError(err error) bool {
	return os.IsNotExist(err) || errors.Is(err, syscall.ENOTDIR)
}

func (m *Manager) scopePath(path string) (string, bool) {
	path = cleanStatusPath(path)
	if m.prefix == "" {
		return path, true
	}
	if !strings.HasPrefix(path, m.prefix) {
		return "", false
	}
	return strings.TrimPrefix(path, m.prefix), true
}

func cleanStatusPath(path string) string {
	return strings.TrimPrefix(filepath.ToSlash(path), "./")
}

func repositoryRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(dir); err == nil && !info.IsDir() {
		dir = filepath.Dir(dir)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, git.GitDirName)); err == nil {
			return dir, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", git.ErrRepositoryNotExists
		}
		dir = parent
	}
}

func (m *Manager) objectPath(path string) string {
	return m.prefix + filepath.ToSlash(path)
}

func (m *Manager) remove(path string) error {
	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return errors.New("invalid change path")
	}
	return os.RemoveAll(filepath.Join(m.workingDir, clean))
}

type encodedPatch struct {
	files []formatdiff.FilePatch
}

func (p *encodedPatch) FilePatches() []formatdiff.FilePatch { return p.files }
func (p *encodedPatch) Message() string                     { return "" }

type encodedFile struct {
	path string
	hash plumbing.Hash
	mode filemode.FileMode
}

func newEncodedFile(path string, data []byte, mode filemode.FileMode) *encodedFile {
	if mode == filemode.Empty {
		mode = filemode.Regular
	}
	return &encodedFile{path: path, hash: plumbing.ComputeHash(plumbing.BlobObject, data), mode: mode}
}

func (f *encodedFile) Hash() plumbing.Hash     { return f.hash }
func (f *encodedFile) Mode() filemode.FileMode { return f.mode }
func (f *encodedFile) Path() string            { return f.path }

type encodedFilePatch struct {
	from, to formatdiff.File
	binary   bool
	chunks   []formatdiff.Chunk
}

func (p *encodedFilePatch) IsBinary() bool { return p.binary }
func (p *encodedFilePatch) Files() (formatdiff.File, formatdiff.File) {
	return p.from, p.to
}
func (p *encodedFilePatch) Chunks() []formatdiff.Chunk { return p.chunks }

type encodedChunk struct {
	content string
	op      formatdiff.Operation
}

func (c *encodedChunk) Content() string            { return c.content }
func (c *encodedChunk) Type() formatdiff.Operation { return c.op }

func makePatch(ctx context.Context, originalPath, path string, status FileStatus, original []byte, originalMode filemode.FileMode, modified []byte, modifiedMode filemode.FileMode) (string, error) {
	filePatch := &encodedFilePatch{binary: isBinary(original) || isBinary(modified)}
	if status != StatusAdded {
		filePatch.from = newEncodedFile(originalPath, original, originalMode)
	}
	if status != StatusDeleted {
		filePatch.to = newEncodedFile(path, modified, modifiedMode)
	}
	if !filePatch.binary {
		for _, change := range utildiff.Do(string(original), string(modified)) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			op := formatdiff.Equal
			switch change.Type {
			case -1:
				op = formatdiff.Delete
			case 1:
				op = formatdiff.Add
			}
			filePatch.chunks = append(filePatch.chunks, &encodedChunk{content: change.Text, op: op})
		}
	}

	var output bytes.Buffer
	encoder := formatdiff.NewUnifiedEncoder(&output, formatdiff.DefaultContextLines)
	if err := encoder.Encode(&encodedPatch{files: []formatdiff.FilePatch{filePatch}}); err != nil {
		return "", fmt.Errorf("encode diff: %w", err)
	}
	return output.String(), nil
}

func isBinary(data []byte) bool {
	if len(data) > 8000 {
		data = data[:8000]
	}
	return bytes.IndexByte(data, 0) >= 0
}
