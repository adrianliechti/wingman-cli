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

// Manager runs a shadow git repo in /tmp that snapshots the working dir on
// each user turn. Init is async — New returns immediately and methods block
// on a ready channel until the shadow repo is set up. The shadow repo works
// in any directory, with or without an existing user .git.
type Manager struct {
	workingDir string

	initDone chan struct{}
	initErr  error

	mu           sync.Mutex
	repo         *git.Repository
	worktree     *git.Worktree
	gitDir       string
	baselineHash plumbing.Hash

	// Exclude patterns are read once on first use and cached for the
	// session — gitignore rules rarely change mid-session and the per-call
	// reads (in-tree .gitignore + global + system + XDG) add up when the
	// diff panel polls. RestartRewind creates a fresh Manager so config
	// edits take effect across sessions.
	excludesOnce    sync.Once
	excludesPattern []gitignore.Pattern
	excludesMatcher gitignore.Matcher
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

// New starts a rewind manager for the given working directory. The shadow
// repo is initialized in a goroutine; methods block on it via the ready
// channel. Failures during init surface as errors from those methods, so
// the caller never has to deal with a nil Manager.
func New(workingDir string) *Manager {
	m := &Manager{
		workingDir: workingDir,
		initDone:   make(chan struct{}),
	}
	go m.init()
	return m
}

// CleanupOrphans removes leftover shadow repos from prior sessions that
// didn't get a chance to clean up (e.g. SIGKILL). Only deletes dirs whose
// mtime is older than the cutoff so concurrently-running sessions are safe.
func CleanupOrphans() {
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "wingman-rewind-*"))
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			os.RemoveAll(m)
		}
	}
}

func (m *Manager) init() {
	defer close(m.initDone)

	sessionID := fmt.Sprintf("%d", time.Now().UnixNano())
	gitDir := filepath.Join(os.TempDir(), "wingman-rewind-"+sessionID)

	if err := os.MkdirAll(gitDir, 0755); err != nil {
		m.initErr = fmt.Errorf("failed to create git dir: %w", err)
		return
	}
	m.gitDir = gitDir

	// If the working dir is already a git repo, read through to its object
	// store so we can baseline against HEAD's tree without copying. Otherwise
	// we build a self-contained shadow repo from a working-tree snapshot.
	var userStorer storer.EncodedObjectStorer
	var userHead *object.Commit
	if userRepo, err := git.PlainOpen(m.workingDir); err == nil {
		userStorer = userRepo.Storer
		if ref, err := userRepo.Head(); err == nil {
			if c, err := userRepo.CommitObject(ref.Hash()); err == nil {
				userHead = c
			}
		}
	}

	gitDirFS := osfs.New(gitDir)
	workTreeFS := osfs.New(m.workingDir)
	tempStorage := filesystem.NewStorage(gitDirFS, cache.NewObjectLRUDefault())
	rewindStorage := &readThroughStorage{
		Storer:    tempStorage,
		secondary: userStorer,
	}

	repo, err := git.Init(rewindStorage, nil)
	if err != nil {
		m.initErr = fmt.Errorf("failed to init repo: %w", err)
		return
	}

	cfg, err := repo.Config()
	if err != nil {
		m.initErr = fmt.Errorf("failed to get config: %w", err)
		return
	}
	cfg.Core.Worktree = m.workingDir
	if err := repo.SetConfig(cfg); err != nil {
		m.initErr = fmt.Errorf("failed to set config: %w", err)
		return
	}

	repo, err = git.Open(rewindStorage, workTreeFS)
	if err != nil {
		m.initErr = fmt.Errorf("failed to open repo: %w", err)
		return
	}

	worktree, err := repo.Worktree()
	if err != nil {
		m.initErr = fmt.Errorf("failed to get worktree: %w", err)
		return
	}

	m.repo = repo
	m.worktree = worktree

	if userHead != nil {
		if err := m.baselineFromHEAD(userHead); err != nil {
			m.initErr = fmt.Errorf("failed to create baseline: %w", err)
		}
		return
	}

	if err := m.baselineFromWorkingTree(); err != nil {
		m.initErr = fmt.Errorf("failed to create baseline: %w", err)
	}
}

// ready blocks until init finishes and returns its error (if any).
func (m *Manager) ready() error {
	<-m.initDone
	return m.initErr
}

// baselineFromHEAD writes a baseline commit pointing at the user repo's HEAD
// tree. The tree itself stays in the user's .git/objects and is reachable
// through readThroughStorage — O(1) writes regardless of repo size.
func (m *Manager) baselineFromHEAD(headCommit *object.Commit) error {
	sig := object.Signature{Name: "wingman", Email: "wingman@local", When: time.Now()}
	baselineCommit := &object.Commit{
		Author:    sig,
		Committer: sig,
		Message:   "Session Start",
		TreeHash:  headCommit.TreeHash,
	}

	obj := m.repo.Storer.NewEncodedObject()
	if err := baselineCommit.Encode(obj); err != nil {
		return fmt.Errorf("failed to encode baseline: %w", err)
	}

	hash, err := m.repo.Storer.SetEncodedObject(obj)
	if err != nil {
		return fmt.Errorf("failed to write baseline: %w", err)
	}

	if err := m.setHead(hash); err != nil {
		return err
	}

	m.baselineHash = hash
	return nil
}

// baselineFromWorkingTree snapshots whatever's on disk into a real commit.
// Used when there's no user HEAD to point at — fresh `git init` with no
// commits, or no .git at all (scratch dir).
func (m *Manager) baselineFromWorkingTree() error {
	m.worktree.Excludes = m.excludes()

	if err := m.worktree.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		return fmt.Errorf("failed to stage baseline: %w", err)
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
		return fmt.Errorf("failed to commit baseline: %w", err)
	}

	m.baselineHash = hash
	return nil
}

// setHead points master at the given hash and makes HEAD a symbolic ref to
// master. Used by both the baseline path and Restore so subsequent commits
// always attach via HEAD→master rather than landing on a detached HEAD.
func (m *Manager) setHead(hash plumbing.Hash) error {
	branch := plumbing.NewBranchReferenceName("master")
	if err := m.repo.Storer.SetReference(plumbing.NewHashReference(branch, hash)); err != nil {
		return fmt.Errorf("failed to set branch ref: %w", err)
	}
	if err := m.repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, branch)); err != nil {
		return fmt.Errorf("failed to set HEAD: %w", err)
	}
	return nil
}

// excludes returns the cached gitignore patterns for the worktree. Safe to
// call after init has succeeded; internal callers reach this through ready()
// already so the worktree is guaranteed set up.
func (m *Manager) excludes() []gitignore.Pattern {
	m.excludesOnce.Do(m.computeExcludes)
	return m.excludesPattern
}

// ExcludeMatcher exposes a gitignore matcher built from the cached patterns
// for callers outside the rewind package (e.g. the server's worktree
// fingerprint walk). Returns an empty matcher if init hasn't completed or
// failed, so callers don't have to nil-check.
func (m *Manager) ExcludeMatcher() gitignore.Matcher {
	if err := m.ready(); err != nil {
		return gitignore.NewMatcher(nil)
	}
	m.excludesOnce.Do(m.computeExcludes)
	return m.excludesMatcher
}

func (m *Manager) computeExcludes() {
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

	m.excludesPattern = patterns
	m.excludesMatcher = gitignore.NewMatcher(patterns)
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

func (m *Manager) Commit(message string) error {
	if err := m.ready(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.worktree.Excludes = m.excludes()

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

func (m *Manager) List() ([]Checkpoint, error) {
	if err := m.ready(); err != nil {
		return nil, err
	}

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
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to iterate commits: %w", err)
	}

	return checkpoints, nil
}

// Restore rolls the working tree back to a checkpoint and re-baselines so
// "diff from baseline" thereafter means "since the restore." Excludes are
// loaded before Clean so gitignored files (node_modules, .env, build
// artifacts) are preserved — without that guard, Clean would silently nuke
// them on rollback.
func (m *Manager) Restore(hash string) error {
	if hash == "" {
		return errors.New("empty hash")
	}

	if err := m.ready(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.worktree.Excludes = m.excludes()

	if err := m.worktree.Clean(&git.CleanOptions{
		Dir: true,
	}); err != nil {
		return fmt.Errorf("failed to clean worktree: %w", err)
	}

	target := plumbing.NewHash(hash)
	if err := m.worktree.Checkout(&git.CheckoutOptions{
		Hash:  target,
		Force: true,
	}); err != nil {
		return fmt.Errorf("failed to checkout: %w", err)
	}

	// Move master to match so future commits attach via HEAD→master rather
	// than landing on a detached HEAD.
	if err := m.setHead(target); err != nil {
		return err
	}

	m.baselineHash = target
	return nil
}

func (m *Manager) Cleanup() {
	// Wait for init so we don't race with the goroutine writing into gitDir.
	<-m.initDone
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

	m.worktree.Excludes = m.excludes()

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

// DiffFromBaseline returns the diff between the baseline and the current working
// tree. The right-hand side is taken from the live working tree (not HEAD), so
// changes from any source — agent tools, the user's terminal, an external editor
// — are reflected.
//
// Returns (nil, nil) when the working tree matches the baseline (no diff).
// Errors are reserved for actual git failures so callers can distinguish.
func (m *Manager) DiffFromBaseline() ([]FileDiff, error) {
	if err := m.ready(); err != nil {
		return nil, err
	}

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

	return diffs, nil
}
