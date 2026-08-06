package changes

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
)

var ErrNotGitRepository = errors.New("workspace is not a Git repository")
var ErrDirtyWorktree = errors.New("local changes can only move between branches at the same commit; clean the worktree before switching to a divergent branch")

type GitFileStatus struct {
	Path           string
	OriginalPath   string
	IndexStatus    string
	WorktreeStatus string
	Staged         bool
	Changed        bool
	Conflict       bool
}

type GitStatus struct {
	Branch    string
	Upstream  string
	Ahead     int
	Behind    int
	HasRemote bool
	Files     []GitFileStatus
}

type GitBranch struct {
	Name    string
	Remote  string
	Current bool
}

func (m *Manager) IsNativeGit() bool {
	if err := m.ready(); err != nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.closed && m.native
}

func (m *Manager) GitStatus(ctx context.Context) (GitStatus, error) {
	if err := m.lockNative(ctx); err != nil {
		return GitStatus{}, err
	}
	defer m.mu.Unlock()
	return m.gitStatusLocked()
}

func (m *Manager) Branches(ctx context.Context, refresh bool) ([]GitBranch, string, error) {
	if err := m.lockNative(ctx); err != nil {
		return nil, "", err
	}
	defer m.mu.Unlock()

	warning := ""
	if refresh {
		if err := m.fetchAllLocked(ctx); err != nil {
			warning = err.Error()
		}
	}

	current, _, err := m.currentBranch()
	if err != nil {
		return nil, "", err
	}
	branches := make([]GitBranch, 0)
	references, err := m.repo.References()
	if err != nil {
		return nil, "", fmt.Errorf("list Git branches: %w", err)
	}
	err = references.ForEach(func(ref *plumbing.Reference) error {
		name := ref.Name()
		switch {
		case name.IsBranch():
			short := name.Short()
			branches = append(branches, GitBranch{Name: short, Current: short == current})
		case name.IsRemote():
			short := strings.TrimPrefix(name.String(), "refs/remotes/")
			remote, branch, ok := strings.Cut(short, "/")
			if !ok || branch == "" || branch == "HEAD" {
				return nil
			}
			branches = append(branches, GitBranch{Name: branch, Remote: remote})
		}
		return nil
	})
	if err != nil {
		return nil, "", fmt.Errorf("list Git branches: %w", err)
	}
	sort.Slice(branches, func(i, j int) bool {
		if branches[i].Remote != branches[j].Remote {
			return branches[i].Remote < branches[j].Remote
		}
		return branches[i].Name < branches[j].Name
	})
	return branches, warning, nil
}

func (m *Manager) CreateBranch(ctx context.Context, name string) error {
	if err := m.lockNative(ctx); err != nil {
		return err
	}
	defer m.mu.Unlock()
	branch, err := branchReference(name)
	if err != nil {
		return err
	}
	if _, err := m.repo.Head(); err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return errors.New("create the first commit before creating another branch")
		}
		return fmt.Errorf("read Git HEAD: %w", err)
	}
	if err := m.worktree.Checkout(&git.CheckoutOptions{Branch: branch, Create: true, Keep: true}); err != nil {
		return fmt.Errorf("create branch %s: %w", name, err)
	}
	return nil
}

func (m *Manager) CheckoutBranch(ctx context.Context, name, remote string) error {
	if err := m.lockNative(ctx); err != nil {
		return err
	}
	defer m.mu.Unlock()
	branch, err := branchReference(name)
	if err != nil {
		return err
	}
	if remote == "" {
		target, err := m.repo.Reference(branch, true)
		if err != nil {
			if errors.Is(err, plumbing.ErrReferenceNotFound) {
				return fmt.Errorf("branch %q does not exist", name)
			}
			return fmt.Errorf("read branch %s: %w", name, err)
		}
		keep, err := m.keepChangesForCheckout(target.Hash())
		if err != nil {
			return err
		}
		if err := m.worktree.Checkout(&git.CheckoutOptions{Branch: branch, Keep: keep}); err != nil {
			return fmt.Errorf("switch to branch %s: %w", name, err)
		}
		return nil
	}

	cfg, err := m.repo.Config()
	if err != nil {
		return fmt.Errorf("read Git config: %w", err)
	}
	if cfg.Remotes[remote] == nil {
		return fmt.Errorf("remote %q does not exist", remote)
	}
	remoteBranch := plumbing.NewRemoteReferenceName(remote, name)
	remoteRef, err := m.repo.Reference(remoteBranch, true)
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return fmt.Errorf("remote branch %s/%s does not exist; refresh branches and try again", remote, name)
		}
		return fmt.Errorf("read remote branch %s/%s: %w", remote, name, err)
	}
	keep, err := m.keepChangesForCheckout(remoteRef.Hash())
	if err != nil {
		return err
	}
	_, err = m.repo.Reference(branch, true)
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		err = m.worktree.Checkout(&git.CheckoutOptions{Branch: branch, Hash: remoteRef.Hash(), Create: true, Keep: keep})
	} else if err == nil {
		localRef, refErr := m.repo.Reference(branch, true)
		if refErr != nil {
			return fmt.Errorf("read local branch %s: %w", name, refErr)
		}
		if localRef.Hash() != remoteRef.Hash() {
			return fmt.Errorf("local branch %q differs from %s/%s; select the local branch or use another name", name, remote, name)
		}
		err = m.worktree.Checkout(&git.CheckoutOptions{Branch: branch, Keep: keep})
	}
	if err != nil {
		return fmt.Errorf("switch to branch %s: %w", name, err)
	}
	if cfg.Branches == nil {
		cfg.Branches = map[string]*config.Branch{}
	}
	cfg.Branches[name] = &config.Branch{
		Name:   name,
		Remote: remote,
		Merge:  plumbing.NewBranchReferenceName(name),
	}
	if err := m.repo.SetConfig(cfg); err != nil {
		return fmt.Errorf("configure upstream %s/%s: %w", remote, name, err)
	}
	return nil
}

func (m *Manager) Stage(ctx context.Context, paths []string) error {
	if err := m.lockNative(ctx); err != nil {
		return err
	}
	defer m.mu.Unlock()
	for _, path := range paths {
		path, err := m.gitPath(path)
		if err != nil {
			return err
		}
		if err := m.worktree.AddWithOptions(&git.AddOptions{Path: path}); err != nil {
			return fmt.Errorf("stage %s: %w", path, err)
		}
	}
	return nil
}

func (m *Manager) Unstage(ctx context.Context, paths []string) error {
	if err := m.lockNative(ctx); err != nil {
		return err
	}
	defer m.mu.Unlock()
	objectPaths := make([]string, 0, len(paths))
	for _, path := range paths {
		path, err := m.gitPath(path)
		if err != nil {
			return err
		}
		objectPaths = append(objectPaths, path)
	}
	if head, err := m.headTree(); err != nil {
		return err
	} else if head != nil {
		if err := m.worktree.Reset(&git.ResetOptions{Mode: git.MixedReset, Files: objectPaths}); err != nil {
			return fmt.Errorf("unstage changes: %w", err)
		}
		return nil
	}

	index, err := m.repo.Storer.Index()
	if err != nil {
		return fmt.Errorf("read Git index: %w", err)
	}
	for _, path := range objectPaths {
		for {
			if _, err := index.Remove(path); err != nil {
				break
			}
		}
	}
	if err := m.repo.Storer.SetIndex(index); err != nil {
		return fmt.Errorf("update Git index: %w", err)
	}
	return nil
}

func (m *Manager) Commit(ctx context.Context, message string) (string, error) {
	if err := m.lockNative(ctx); err != nil {
		return "", err
	}
	defer m.mu.Unlock()
	if err := m.ensureNoStagedChangesOutsideScope(); err != nil {
		return "", err
	}
	hash, err := m.worktree.Commit(message, &git.CommitOptions{})
	if err != nil {
		return "", fmt.Errorf("commit changes: %w", err)
	}
	return "Committed " + hash.String()[:7], nil
}

func (m *Manager) Pull(ctx context.Context) (string, error) {
	if err := m.lockNative(ctx); err != nil {
		return "", err
	}
	defer m.mu.Unlock()
	branch, _, err := m.currentBranch()
	if err != nil {
		return "", err
	}
	branchConfig, err := m.branchConfig(branch)
	if err != nil {
		return "", err
	}
	auth, err := m.authForRemote(ctx, branchConfig.Remote)
	if err != nil {
		return "", fmt.Errorf("authenticate %s: %w", branchConfig.Remote, err)
	}
	err = m.worktree.PullContext(ctx, &git.PullOptions{
		RemoteName:    branchConfig.Remote,
		ReferenceName: branchConfig.Merge,
		SingleBranch:  true,
		Auth:          auth,
	})
	if errors.Is(err, git.NoErrAlreadyUpToDate) {
		return "Already up to date", nil
	}
	if err != nil {
		return "", fmt.Errorf("pull %s: %w", branchConfig.Remote, err)
	}
	return "Pulled from " + branchConfig.Remote, nil
}

func (m *Manager) Push(ctx context.Context) (string, error) {
	if err := m.lockNative(ctx); err != nil {
		return "", err
	}
	defer m.mu.Unlock()
	branch, head, err := m.currentBranch()
	if err != nil {
		return "", err
	}
	if head == nil {
		return "", errors.New("cannot push an unborn branch")
	}

	cfg, err := m.repo.Config()
	if err != nil {
		return "", fmt.Errorf("read Git config: %w", err)
	}
	branchConfig := cfg.Branches[branch]
	remoteName := ""
	remoteBranch := plumbing.NewBranchReferenceName(branch)
	if branchConfig != nil && branchConfig.Remote != "" {
		remoteName = branchConfig.Remote
		if branchConfig.Merge != "" {
			remoteBranch = branchConfig.Merge
		}
	}
	if remoteName == "" {
		remoteName, err = defaultRemote(cfg)
		if err != nil {
			return "", err
		}
	}
	auth, err := m.authForRemote(ctx, remoteName)
	if err != nil {
		return "", fmt.Errorf("authenticate %s: %w", remoteName, err)
	}

	refspec := config.RefSpec(head.Name().String() + ":" + remoteBranch.String())
	err = m.repo.PushContext(ctx, &git.PushOptions{RemoteName: remoteName, RefSpecs: []config.RefSpec{refspec}, Auth: auth})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return "", fmt.Errorf("push %s: %w", remoteName, err)
	}

	if branchConfig == nil || branchConfig.Remote == "" || branchConfig.Merge == "" {
		if cfg.Branches == nil {
			cfg.Branches = map[string]*config.Branch{}
		}
		cfg.Branches[branch] = &config.Branch{Name: branch, Remote: remoteName, Merge: remoteBranch}
		if err := m.repo.SetConfig(cfg); err != nil {
			return "", fmt.Errorf("set upstream branch: %w", err)
		}
	}
	tracking := plumbing.NewRemoteReferenceName(remoteName, remoteBranch.Short())
	if err := m.repo.Storer.SetReference(plumbing.NewHashReference(tracking, head.Hash())); err != nil {
		return "", fmt.Errorf("update remote tracking branch: %w", err)
	}
	if errors.Is(err, git.NoErrAlreadyUpToDate) {
		return "Everything up to date", nil
	}
	return "Pushed to " + remoteName + "/" + remoteBranch.Short(), nil
}

func (m *Manager) fetchAllLocked(ctx context.Context) error {
	cfg, err := m.repo.Config()
	if err != nil {
		return fmt.Errorf("read Git config: %w", err)
	}
	names := make([]string, 0, len(cfg.Remotes))
	for name := range cfg.Remotes {
		names = append(names, name)
	}
	sort.Strings(names)
	var fetchErrors []error
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		auth, err := m.authForRemote(ctx, name)
		if err != nil {
			fetchErrors = append(fetchErrors, fmt.Errorf("refresh %s: %w", name, err))
			continue
		}
		err = m.repo.FetchContext(ctx, &git.FetchOptions{RemoteName: name, Auth: auth, Prune: true})
		if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
			fetchErrors = append(fetchErrors, fmt.Errorf("refresh %s: %w", name, err))
		}
	}
	return errors.Join(fetchErrors...)
}

func (m *Manager) ensureNoStagedChangesOutsideScope() error {
	if m.prefix == "" {
		return nil
	}
	status, err := m.worktree.Status()
	if err != nil {
		return fmt.Errorf("read Git status: %w", err)
	}
	outside := make([]string, 0)
	for path, fileStatus := range status {
		if fileStatus.Staging == git.Unmodified || fileStatus.Staging == git.Untracked {
			continue
		}
		paths := []string{path}
		if fileStatus.Staging == git.Renamed && fileStatus.Extra != "" {
			paths = append(paths, fileStatus.Extra)
		}
		for _, candidate := range paths {
			if _, ok := m.scopePath(candidate); !ok {
				outside = append(outside, cleanStatusPath(candidate))
			}
		}
	}
	if len(outside) == 0 {
		return nil
	}
	sort.Strings(outside)
	const maxExamples = 3
	examples := outside
	if len(examples) > maxExamples {
		examples = examples[:maxExamples]
	}
	detail := strings.Join(examples, ", ")
	if len(outside) > len(examples) {
		detail += fmt.Sprintf(" and %d more", len(outside)-len(examples))
	}
	return fmt.Errorf("cannot commit: staged changes outside this workspace (%s)", detail)
}

func (m *Manager) keepChangesForCheckout(target plumbing.Hash) (bool, error) {
	status, err := m.worktree.Status()
	if err != nil {
		return false, fmt.Errorf("read Git status: %w", err)
	}
	if status.IsClean() {
		return false, nil
	}
	head, err := m.repo.Head()
	if err != nil {
		return false, fmt.Errorf("read Git HEAD: %w", err)
	}
	if head.Hash() != target {
		return false, ErrDirtyWorktree
	}
	return true, nil
}

func branchReference(name string) (plumbing.ReferenceName, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("branch name is required")
	}
	branch := plumbing.NewBranchReferenceName(name)
	if err := branch.Validate(); err != nil {
		return "", fmt.Errorf("invalid branch name %q: %w", name, err)
	}
	return branch, nil
}

func (m *Manager) lockNative(ctx context.Context) error {
	if err := m.ready(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrClosed
	}
	if !m.native {
		m.mu.Unlock()
		return ErrNotGitRepository
	}
	return nil
}

func (m *Manager) gitStatusLocked() (GitStatus, error) {
	entries, err := m.status()
	if err != nil {
		return GitStatus{}, err
	}
	status := GitStatus{Files: make([]GitFileStatus, 0, len(entries))}
	for _, entry := range entries {
		status.Files = append(status.Files, GitFileStatus{
			Path:           entry.path,
			OriginalPath:   entry.originalPath,
			IndexStatus:    statusCode(entry.index),
			WorktreeStatus: statusCode(entry.worktree),
			Staged:         entry.index != git.Unmodified && entry.index != git.Untracked,
			Changed:        entry.worktree != git.Unmodified,
			Conflict:       entry.conflict,
		})
	}

	cfg, err := m.repo.Config()
	if err != nil {
		return GitStatus{}, fmt.Errorf("read Git config: %w", err)
	}
	status.HasRemote = len(cfg.Remotes) > 0
	branch, head, err := m.currentBranch()
	if err != nil {
		return GitStatus{}, err
	}
	status.Branch = branch
	branchConfig := cfg.Branches[branch]
	if branchConfig == nil || branchConfig.Remote == "" || branchConfig.Merge == "" {
		return status, nil
	}
	status.Upstream = branchConfig.Remote + "/" + branchConfig.Merge.Short()
	if head == nil {
		return status, nil
	}
	upstreamName := plumbing.NewRemoteReferenceName(branchConfig.Remote, branchConfig.Merge.Short())
	upstream, err := m.repo.Reference(upstreamName, true)
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return status, nil
	}
	if err != nil {
		return GitStatus{}, fmt.Errorf("read upstream branch: %w", err)
	}
	status.Ahead, status.Behind, err = m.aheadBehind(head.Hash(), upstream.Hash())
	if err != nil {
		return GitStatus{}, err
	}
	return status, nil
}

func (m *Manager) currentBranch() (string, *plumbing.Reference, error) {
	head, err := m.repo.Head()
	if err == nil {
		if !head.Name().IsBranch() {
			return "(detached)", head, nil
		}
		return head.Name().Short(), head, nil
	}
	if !errors.Is(err, plumbing.ErrReferenceNotFound) {
		return "", nil, fmt.Errorf("read Git HEAD: %w", err)
	}
	symbolic, err := m.repo.Reference(plumbing.HEAD, false)
	if err != nil {
		return "", nil, fmt.Errorf("read Git HEAD: %w", err)
	}
	if symbolic.Type() == plumbing.SymbolicReference && symbolic.Target().IsBranch() {
		return symbolic.Target().Short(), nil, nil
	}
	return "(detached)", nil, nil
}

func (m *Manager) branchConfig(branch string) (*config.Branch, error) {
	cfg, err := m.repo.Config()
	if err != nil {
		return nil, fmt.Errorf("read Git config: %w", err)
	}
	branchConfig := cfg.Branches[branch]
	if branchConfig == nil || branchConfig.Remote == "" || branchConfig.Merge == "" {
		return nil, errors.New("branch has no upstream")
	}
	return branchConfig, nil
}

func defaultRemote(cfg *config.Config) (string, error) {
	if _, ok := cfg.Remotes["origin"]; ok {
		return "origin", nil
	}
	if len(cfg.Remotes) == 1 {
		for name := range cfg.Remotes {
			return name, nil
		}
	}
	if len(cfg.Remotes) == 0 {
		return "", errors.New("no Git remote is configured")
	}
	return "", errors.New("branch has no upstream; configure one before pushing")
}

func statusCode(code git.StatusCode) string {
	if code == git.Unmodified {
		return "."
	}
	return string([]byte{byte(code)})
}

func (m *Manager) gitPath(path string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("invalid Git path")
	}
	return m.objectPath(filepath.ToSlash(clean)), nil
}

func (m *Manager) aheadBehind(localHash, upstreamHash plumbing.Hash) (int, int, error) {
	if localHash == upstreamHash {
		return 0, 0, nil
	}
	local, err := m.repo.CommitObject(localHash)
	if err != nil {
		return 0, 0, err
	}
	upstream, err := m.repo.CommitObject(upstreamHash)
	if err != nil {
		return 0, 0, err
	}
	bases, err := local.MergeBase(upstream)
	if err != nil {
		return 0, 0, err
	}
	stops := make(map[plumbing.Hash]bool, len(bases))
	for _, base := range bases {
		stops[base.Hash] = true
	}
	ahead, err := m.countCommitsUntil(localHash, stops)
	if err != nil {
		return 0, 0, err
	}
	behind, err := m.countCommitsUntil(upstreamHash, stops)
	return ahead, behind, err
}

func (m *Manager) countCommitsUntil(start plumbing.Hash, stops map[plumbing.Hash]bool) (int, error) {
	seen := map[plumbing.Hash]bool{}
	stack := []plumbing.Hash{start}
	count := 0
	for len(stack) > 0 {
		hash := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[hash] || stops[hash] {
			continue
		}
		seen[hash] = true
		commit, err := m.repo.CommitObject(hash)
		if err != nil {
			return 0, err
		}
		count++
		stack = append(stack, commit.ParentHashes...)
	}
	return count, nil
}
