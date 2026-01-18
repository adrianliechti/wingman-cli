package rewind

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/filesystem"
	"github.com/go-git/go-git/v5/utils/merkletrie"
)

type Checkpoint struct {
	Hash    string
	Message string
	Time    time.Time
}

type Manager struct {
	repo       *git.Repository
	worktree   *git.Worktree
	gitDir     string
	workingDir string
}

func New(workingDir string) (*Manager, error) {
	sessionID := fmt.Sprintf("%d", time.Now().UnixNano())
	gitDir := filepath.Join(os.TempDir(), "wingman-rewind-"+sessionID)

	if err := os.MkdirAll(gitDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create git dir: %w", err)
	}

	gitDirFS := osfs.New(gitDir)
	workTreeFS := osfs.New(workingDir)

	storage := filesystem.NewStorage(gitDirFS, cache.NewObjectLRUDefault())

	repo, err := git.Init(storage, nil)

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

	repo, err = git.Open(storage, workTreeFS)
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
	}

	if err := m.baseline(); err != nil {
		os.RemoveAll(gitDir)
		return nil, fmt.Errorf("failed to create baseline commit: %w", err)
	}

	return m, nil
}

func (m *Manager) baseline() error {
	if err := m.worktree.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		return fmt.Errorf("failed to add files: %w", err)
	}

	_, err := m.worktree.Commit("Baseline", &git.CommitOptions{
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

	return nil
}

func (m *Manager) commit(message string) error {
	if err := m.worktree.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		return fmt.Errorf("failed to add files: %w", err)
	}

	status, err := m.worktree.Status()

	if err != nil {
		return fmt.Errorf("failed to get status: %w", err)
	}

	if status.IsClean() {
		return nil
	}

	_, err = m.worktree.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  "wingman",
			Email: "wingman@local",
			When:  time.Now(),
		},
		AllowEmptyCommits: false,
	})

	if err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	return nil
}

func (m *Manager) Commit(message string) error {
	return m.commit(message)
}

func (m *Manager) List() ([]Checkpoint, error) {
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

func (m *Manager) Restore(hash string) error {
	if hash == "" {
		return errors.New("empty hash")
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
}

// DiffFromBaseline returns the diff between the baseline commit and the current HEAD
func (m *Manager) DiffFromBaseline() ([]FileDiff, error) {
	// Get all commits
	checkpoints, err := m.List()
	if err != nil {
		return nil, fmt.Errorf("failed to list checkpoints: %w", err)
	}

	if len(checkpoints) == 0 {
		return nil, errors.New("no checkpoints available")
	}

	// The baseline is the last commit in the list (oldest)
	baselineHash := checkpoints[len(checkpoints)-1].Hash

	// The current state is the first commit (newest) - but we need to commit any pending changes first
	if err := m.worktree.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		return nil, fmt.Errorf("failed to add files: %w", err)
	}

	// Get the baseline commit
	baselineCommit, err := m.repo.CommitObject(plumbing.NewHash(baselineHash))
	if err != nil {
		return nil, fmt.Errorf("failed to get baseline commit: %w", err)
	}

	baselineTree, err := baselineCommit.Tree()
	if err != nil {
		return nil, fmt.Errorf("failed to get baseline tree: %w", err)
	}

	// Get HEAD commit
	headRef, err := m.repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %w", err)
	}

	headCommit, err := m.repo.CommitObject(headRef.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD commit: %w", err)
	}

	headTree, err := headCommit.Tree()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD tree: %w", err)
	}

	// Get the diff between baseline and HEAD
	changes, err := baselineTree.Diff(headTree)
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

		diffs = append(diffs, FileDiff{
			Path:   path,
			Status: status,
			Patch:  patch.String(),
		})
	}

	if len(diffs) == 0 {
		return nil, errors.New("no changes from baseline")
	}

	return diffs, nil
}
