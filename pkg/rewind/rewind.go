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
