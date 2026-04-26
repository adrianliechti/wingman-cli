package rewind

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/storage"
	"github.com/go-git/go-git/v5/storage/filesystem"
	"github.com/go-git/go-git/v5/utils/merkletrie"
)

type Checkpoint struct {
	Hash    string
	Message string
	Time    time.Time
}

type Manager struct {
	mu           sync.Mutex
	repo         *git.Repository
	worktree     *git.Worktree
	gitDir       string
	workingDir   string
	userRepo     *git.Repository // user's actual .git, opened read-only for HEAD lookups
	baselineHash plumbing.Hash
	// firstVisibleHash is the oldest commit shown in List(). When the working
	// tree was dirty at launch, the HEAD baseline commit lives below this and
	// is hidden so the chain reads simply: "Uncommitted Work" → per-turn …
	firstVisibleHash plumbing.Hash
}

// readThroughStorage is a Storer that delegates writes to a primary store and
// falls back to a read-only secondary object store on cache misses. It lets us
// reference objects from the user's .git/objects without copying them into our
// temp rewind store, which avoids an O(repo-size) walk at startup.
type readThroughStorage struct {
	storage.Storer
	secondary storer.EncodedObjectStorer
}

func (s *readThroughStorage) EncodedObject(t plumbing.ObjectType, h plumbing.Hash) (plumbing.EncodedObject, error) {
	obj, err := s.Storer.EncodedObject(t, h)
	if err == nil {
		return obj, nil
	}
	if errors.Is(err, plumbing.ErrObjectNotFound) && s.secondary != nil {
		return s.secondary.EncodedObject(t, h)
	}
	return obj, err
}

func (s *readThroughStorage) HasEncodedObject(h plumbing.Hash) error {
	if err := s.Storer.HasEncodedObject(h); err == nil {
		return nil
	} else if !errors.Is(err, plumbing.ErrObjectNotFound) {
		return err
	}
	if s.secondary != nil {
		return s.secondary.HasEncodedObject(h)
	}
	return plumbing.ErrObjectNotFound
}

func (s *readThroughStorage) EncodedObjectSize(h plumbing.Hash) (int64, error) {
	size, err := s.Storer.EncodedObjectSize(h)
	if err == nil {
		return size, nil
	}
	if errors.Is(err, plumbing.ErrObjectNotFound) && s.secondary != nil {
		return s.secondary.EncodedObjectSize(h)
	}
	return size, err
}

func New(workingDir string) (*Manager, error) {
	sessionID := fmt.Sprintf("%d", time.Now().UnixNano())
	gitDir := filepath.Join(os.TempDir(), "wingman-rewind-"+sessionID)

	if err := os.MkdirAll(gitDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create git dir: %w", err)
	}

	gitDirFS := osfs.New(gitDir)
	workTreeFS := osfs.New(workingDir)

	tempStorage := filesystem.NewStorage(gitDirFS, cache.NewObjectLRUDefault())

	// Open the user's .git read-only, if it exists, so we can reference its
	// objects via read-through without copying.
	var userRepo *git.Repository
	if r, err := git.PlainOpen(workingDir); err == nil {
		userRepo = r
	}

	var rewindStorage storage.Storer = tempStorage
	if userRepo != nil {
		rewindStorage = &readThroughStorage{
			Storer:    tempStorage,
			secondary: userRepo.Storer,
		}
	}

	repo, err := git.Init(rewindStorage, nil)

	if err != nil {
		os.RemoveAll(gitDir)

		return nil, fmt.Errorf("failed to init repo: %w", err)
	}

	cfg, err := repo.Config()

	if err != nil {
		os.RemoveAll(gitDir)

		return nil, fmt.Errorf("failed to get config: %w", err)
	}

	cfg.Core.Worktree = workingDir

	if err := repo.SetConfig(cfg); err != nil {
		os.RemoveAll(gitDir)

		return nil, fmt.Errorf("failed to set config: %w", err)
	}

	repo, err = git.Open(rewindStorage, workTreeFS)

	if err != nil {
		os.RemoveAll(gitDir)

		return nil, fmt.Errorf("failed to open repo: %w", err)
	}

	worktree, err := repo.Worktree()

	if err != nil {
		os.RemoveAll(gitDir)

		return nil, fmt.Errorf("failed to get worktree: %w", err)
	}

	m := &Manager{
		repo:       repo,
		worktree:   worktree,
		gitDir:     gitDir,
		workingDir: workingDir,
		userRepo:   userRepo,
	}

	if err := m.initBaseline(); err != nil {
		os.RemoveAll(gitDir)

		return nil, fmt.Errorf("failed to create baseline commit: %w", err)
	}

	return m, nil
}

// initBaseline creates the baseline commit for the rewind history.
// Preferred: take the user's git HEAD as baseline (so pre-existing uncommitted
// changes appear in the diff). If the working dir is not a git repo or has no
// HEAD yet, fall back to snapshotting the current working tree.
//
// When HEAD is used and the working tree differs from it, a follow-up
// "Uncommitted Work" checkpoint is created so the user's pre-existing edits
// are preserved as a restorable point.
func (m *Manager) initBaseline() error {
	hash, ok := m.baselineFromUserHEAD()
	if !ok {
		if err := m.baselineFromWorkingTree(); err != nil {
			return err
		}
		m.firstVisibleHash = m.baselineHash
		return nil
	}

	m.baselineHash = hash
	m.firstVisibleHash = hash

	// If the working tree differs from HEAD, "Uncommitted Work" lands on top
	// and becomes the new first-visible entry — hiding the bare HEAD baseline.
	// On a clean tree m.commit is a no-op and HEAD stays at the baseline.
	_ = m.commit("Uncommitted Work")
	if headRef, err := m.repo.Head(); err == nil {
		m.firstVisibleHash = headRef.Hash()
	}
	return nil
}

// baselineFromUserHEAD attempts to read the user's git HEAD and create a
// baseline commit in our rewind store referencing the HEAD tree. Returns the
// new commit hash and true on success.
//
// We don't copy the HEAD tree's objects — our store is wired up read-through
// to the user's .git/objects, so referencing the tree hash is enough.
func (m *Manager) baselineFromUserHEAD() (plumbing.Hash, bool) {
	if m.userRepo == nil {
		return plumbing.ZeroHash, false
	}

	headRef, err := m.userRepo.Head()
	if err != nil {
		return plumbing.ZeroHash, false
	}

	headCommit, err := m.userRepo.CommitObject(headRef.Hash())
	if err != nil {
		return plumbing.ZeroHash, false
	}

	sig := object.Signature{Name: "wingman", Email: "wingman@local", When: time.Now()}
	baselineCommit := &object.Commit{
		Author:    sig,
		Committer: sig,
		Message:   "Session Start",
		TreeHash:  headCommit.TreeHash,
	}

	obj := m.repo.Storer.NewEncodedObject()
	if err := baselineCommit.Encode(obj); err != nil {
		return plumbing.ZeroHash, false
	}

	hash, err := m.repo.Storer.SetEncodedObject(obj)
	if err != nil {
		return plumbing.ZeroHash, false
	}

	branch := plumbing.NewBranchReferenceName("master")
	if err := m.repo.Storer.SetReference(plumbing.NewHashReference(branch, hash)); err != nil {
		return plumbing.ZeroHash, false
	}
	if err := m.repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, branch)); err != nil {
		return plumbing.ZeroHash, false
	}

	return hash, true
}

// baselineFromWorkingTree is the fallback baseline strategy when the user's
// working dir is not a git repo, or has no HEAD yet. It snapshots whatever
// is currently on disk.
func (m *Manager) baselineFromWorkingTree() error {
	m.worktree.Excludes = m.loadExcludePatterns()

	if err := m.worktree.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		return fmt.Errorf("failed to add files: %w", err)
	}

	hash, err := m.worktree.Commit("Session Start", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "wingman",
			Email: "wingman@local",
			When:  time.Now(),
		},
		AllowEmptyCommits: true,
	})

	if err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	m.baselineHash = hash

	return nil
}


func (m *Manager) loadExcludePatterns() []gitignore.Pattern {
	// In-tree patterns: .git/info/exclude and recursive .gitignore files.
	patterns, _ := gitignore.ReadPatterns(m.worktree.Filesystem, nil)

	// core.excludesfile from ~/.gitconfig and /etc/gitconfig. The helpers
	// expect a filesystem rooted at "/" so absolute paths resolve.
	rootFS := osfs.New("/")
	if global, err := gitignore.LoadGlobalPatterns(rootFS); err == nil {
		patterns = append(patterns, global...)
	}
	if system, err := gitignore.LoadSystemPatterns(rootFS); err == nil {
		patterns = append(patterns, system...)
	}

	// XDG-standard $XDG_CONFIG_HOME/git/ignore (default ~/.config/git/ignore).
	// Git honors this even when core.excludesfile is unset; go-git does not,
	// so we read it ourselves.
	patterns = append(patterns, readXDGIgnore()...)

	return patterns
}

func readXDGIgnore() []gitignore.Pattern {
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		xdg = filepath.Join(home, ".config")
	}

	f, err := os.Open(filepath.Join(xdg, "git", "ignore"))
	if err != nil {
		return nil
	}
	defer f.Close()

	var ps []gitignore.Pattern
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") || len(strings.TrimSpace(line)) == 0 {
			continue
		}
		ps = append(ps, gitignore.ParsePattern(line, nil))
	}
	return ps
}

func (m *Manager) commit(message string) error {
	// Cheap status check first: if nothing changed, skip the expensive
	// AddWithOptions walk entirely. This is the common case for clean repos
	// and dramatically cheaper than always staging.
	m.worktree.Excludes = m.loadExcludePatterns()

	status, err := m.worktree.Status()
	if err != nil {
		return fmt.Errorf("failed to get status: %w", err)
	}
	if status.IsClean() {
		return nil
	}

	if err := m.worktree.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		return fmt.Errorf("failed to add files: %w", err)
	}

	if _, err := m.worktree.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  "wingman",
			Email: "wingman@local",
			When:  time.Now(),
		},
		AllowEmptyCommits: false,
	}); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	return nil
}

func (m *Manager) Commit(message string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.commit(message)
}

func (m *Manager) List() ([]Checkpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ref, err := m.repo.Head()

	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %w", err)
	}

	iter, err := m.repo.Log(&git.LogOptions{From: ref.Hash()})

	if err != nil {
		return nil, fmt.Errorf("failed to get log: %w", err)
	}

	var checkpoints []Checkpoint

	err = iter.ForEach(func(c *object.Commit) error {
		checkpoints = append(checkpoints, Checkpoint{
			Hash:    c.Hash.String(),
			Message: c.Message,
			Time:    c.Author.When,
		})

		// Stop after we've emitted the oldest user-visible checkpoint;
		// anything below (e.g. the hidden HEAD baseline when "Uncommitted
		// Work" exists) is implementation detail.
		if !m.firstVisibleHash.IsZero() && c.Hash == m.firstVisibleHash {
			return storer.ErrStop
		}

		return nil
	})

	if err != nil && err != storer.ErrStop {
		return nil, fmt.Errorf("failed to iterate commits: %w", err)
	}

	return checkpoints, nil
}

func (m *Manager) Restore(hash string) error {
	if hash == "" {
		return errors.New("empty hash")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Clean untracked files first so restore is complete
	if err := m.worktree.Clean(&git.CleanOptions{
		Dir: true,
	}); err != nil {
		return fmt.Errorf("failed to clean worktree: %w", err)
	}

	err := m.worktree.Checkout(&git.CheckoutOptions{
		Hash:  plumbing.NewHash(hash),
		Force: true,
	})

	if err != nil {
		return fmt.Errorf("failed to checkout: %w", err)
	}

	return nil
}

func (m *Manager) Cleanup() {
	if m.gitDir != "" {
		os.RemoveAll(m.gitDir)
	}
}

// FileStatus represents the status of a file in the diff
type FileStatus int

const (
	StatusAdded FileStatus = iota
	StatusModified
	StatusDeleted
)

// FileDiff represents a diff for a single file
type FileDiff struct {
	Path   string
	Status FileStatus
	Patch  string

	// Original is the file's content at the baseline (empty when added).
	Original string
	// Modified is the file's content in the current working tree (empty when deleted).
	Modified string
}

// snapshotTree captures the current working tree as a tree object without polluting
// the user-visible checkpoint history. It works by writing a transient commit and
// then resetting the branch ref back to where it was before — the commit and tree
// objects remain in the object store as garbage, which is fine for a /tmp repo.
func (m *Manager) snapshotTree() (*object.Tree, error) {
	prevHead, err := m.repo.Head()

	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %w", err)
	}

	m.worktree.Excludes = m.loadExcludePatterns()

	if err := m.worktree.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		return nil, fmt.Errorf("failed to stage: %w", err)
	}

	snapshotHash, err := m.worktree.Commit("__live__", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "wingman",
			Email: "wingman@local",
			When:  time.Now(),
		},
		AllowEmptyCommits: true,
	})

	// Always try to roll the branch ref back, even if the commit failed.
	if rollbackErr := m.repo.Storer.SetReference(plumbing.NewHashReference(prevHead.Name(), prevHead.Hash())); rollbackErr != nil {
		if err == nil {
			err = fmt.Errorf("failed to reset HEAD after snapshot: %w", rollbackErr)
		}
	}

	if err != nil {
		return nil, fmt.Errorf("failed to snapshot: %w", err)
	}

	snapshotCommit, err := m.repo.CommitObject(snapshotHash)

	if err != nil {
		return nil, fmt.Errorf("failed to load snapshot commit: %w", err)
	}

	tree, err := snapshotCommit.Tree()

	if err != nil {
		return nil, fmt.Errorf("failed to get snapshot tree: %w", err)
	}

	return tree, nil
}

// DiffFromBaseline returns the diff between the baseline and the current working tree.
// The right-hand side is taken from the live working tree (not HEAD), so changes from
// any source — agent tools, the user's terminal, an external editor — are reflected.
func (m *Manager) DiffFromBaseline() ([]FileDiff, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.baselineHash.IsZero() {
		return nil, errors.New("no baseline available")
	}

	baselineCommit, err := m.repo.CommitObject(m.baselineHash)

	if err != nil {
		return nil, fmt.Errorf("failed to get baseline commit: %w", err)
	}

	baselineTree, err := baselineCommit.Tree()

	if err != nil {
		return nil, fmt.Errorf("failed to get baseline tree: %w", err)
	}

	liveTree, err := m.snapshotTree()

	if err != nil {
		return nil, fmt.Errorf("failed to snapshot working tree: %w", err)
	}

	changes, err := baselineTree.Diff(liveTree)

	if err != nil {
		return nil, fmt.Errorf("failed to compute diff: %w", err)
	}

	if len(changes) == 0 {
		return nil, errors.New("no changes from baseline")
	}

	var diffs []FileDiff

	for _, change := range changes {
		patch, err := change.Patch()

		if err != nil {
			continue
		}

		var status FileStatus
		var path string

		action, err := change.Action()

		if err != nil {
			continue
		}

		switch action {
		case merkletrie.Insert:
			status = StatusAdded
			path = change.To.Name
		case merkletrie.Delete:
			status = StatusDeleted
			path = change.From.Name
		case merkletrie.Modify:
			status = StatusModified
			path = change.To.Name
		default:
			continue
		}

		from, to, _ := change.Files()
		var original, modified string
		if from != nil {
			if c, err := from.Contents(); err == nil {
				original = c
			}
		}
		if to != nil {
			if c, err := to.Contents(); err == nil {
				modified = c
			}
		}

		diffs = append(diffs, FileDiff{
			Path:     path,
			Status:   status,
			Patch:    patch.String(),
			Original: original,
			Modified: modified,
		})
	}

	if len(diffs) == 0 {
		return nil, errors.New("no changes from baseline")
	}

	return diffs, nil
}
