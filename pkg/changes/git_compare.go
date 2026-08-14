package changes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
)

// WorktreeRevision is the API-only revision used to compare a commit with the
// current index and filesystem state. A colon cannot appear in a Git ref name,
// so it cannot shadow a real branch or tag.
const WorktreeRevision = ":worktree"

// EmptyTreeRevision is the API-only base used to show the contents introduced
// by a repository's root commit.
const EmptyTreeRevision = ":empty"

type CompareResult struct {
	BaseHash      string
	HeadHash      string
	MergeBaseHash string
	Diffs         []FileDiff
}

type GitCommit struct {
	Hash       string
	Parents    []string
	Summary    string
	Author     string
	AuthoredAt time.Time
	Refs       []string
}

// Compare returns committed file changes from base to head. When mergeBase is
// true, the base tree is replaced with the commits' common ancestor, matching
// the three-dot comparison used for pull request file views.
func (m *Manager) Compare(ctx context.Context, base, head string, mergeBase bool) (CompareResult, error) {
	if err := m.lock(ctx); err != nil {
		return CompareResult{}, err
	}
	defer m.mu.Unlock()
	if base == EmptyTreeRevision {
		if mergeBase {
			return CompareResult{}, errors.New("the empty tree cannot be used for a merge-base comparison")
		}
		if head == WorktreeRevision {
			return CompareResult{}, errors.New("the empty tree can only be compared with a commit")
		}
		return m.compareEmptyTree(ctx, head)
	}

	baseCommit, err := m.resolveCommit(base)
	if err != nil {
		return CompareResult{}, fmt.Errorf("resolve base %q: %w", base, err)
	}
	if head == WorktreeRevision {
		return m.compareWorktree(ctx, baseCommit, mergeBase)
	}
	headCommit, err := m.resolveCommit(head)
	if err != nil {
		return CompareResult{}, fmt.Errorf("resolve compare ref %q: %w", head, err)
	}
	result := CompareResult{
		BaseHash: baseCommit.Hash.String(),
		HeadHash: headCommit.Hash.String(),
		Diffs:    []FileDiff{},
	}
	comparisonBase := baseCommit
	if mergeBase {
		bases, err := baseCommit.MergeBase(headCommit)
		if err != nil {
			return CompareResult{}, fmt.Errorf("find merge base: %w", err)
		}
		if len(bases) == 0 {
			return CompareResult{}, errors.New("the selected refs have no common ancestor")
		}
		comparisonBase = bases[0]
		result.MergeBaseHash = comparisonBase.Hash.String()
	}

	baseTree, err := comparisonBase.Tree()
	if err != nil {
		return CompareResult{}, fmt.Errorf("read base tree: %w", err)
	}
	headTree, err := headCommit.Tree()
	if err != nil {
		return CompareResult{}, fmt.Errorf("read compare tree: %w", err)
	}
	result.Diffs, err = m.committedTreeDiffs(ctx, baseTree, headTree)
	if err != nil {
		return CompareResult{}, err
	}
	return result, nil
}

func (m *Manager) compareEmptyTree(ctx context.Context, head string) (CompareResult, error) {
	headCommit, err := m.resolveCommit(head)
	if err != nil {
		return CompareResult{}, fmt.Errorf("resolve compare ref %q: %w", head, err)
	}
	headTree, err := headCommit.Tree()
	if err != nil {
		return CompareResult{}, fmt.Errorf("read compare tree: %w", err)
	}
	diffs, err := m.committedTreeDiffs(ctx, nil, headTree)
	if err != nil {
		return CompareResult{}, err
	}
	return CompareResult{
		HeadHash: headCommit.Hash.String(),
		Diffs:    diffs,
	}, nil
}

func (m *Manager) committedTreeDiffs(ctx context.Context, baseTree, headTree *object.Tree) ([]FileDiff, error) {
	changes, err := object.DiffTreeWithOptions(ctx, baseTree, headTree, object.DefaultDiffTreeOptions)
	if err != nil {
		return nil, fmt.Errorf("compare trees: %w", err)
	}
	diffs := make([]FileDiff, 0, len(changes))
	for _, change := range changes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		diff, ok, err := m.committedFileDiff(ctx, baseTree, headTree, change)
		if err != nil {
			return nil, err
		}
		if ok {
			diffs = append(diffs, diff)
		}
	}
	return diffs, nil
}

type worktreeCandidate struct {
	path         string
	originalPath string
	status       *statusEntry
}

// compareWorktree compares a selected commit (or its merge base with HEAD) to
// the combined index/filesystem state. The candidate set is the union of
// committed changes since the comparison base and local Git status, which also
// includes untracked files.
func (m *Manager) compareWorktree(ctx context.Context, baseCommit *object.Commit, mergeBase bool) (CompareResult, error) {
	headCommit, err := m.resolveCommit("HEAD")
	if err != nil {
		return CompareResult{}, fmt.Errorf("resolve HEAD: %w", err)
	}
	result := CompareResult{
		BaseHash: baseCommit.Hash.String(),
		HeadHash: headCommit.Hash.String(),
		Diffs:    []FileDiff{},
	}
	comparisonBase := baseCommit
	if mergeBase {
		bases, err := baseCommit.MergeBase(headCommit)
		if err != nil {
			return CompareResult{}, fmt.Errorf("find merge base: %w", err)
		}
		if len(bases) == 0 {
			return CompareResult{}, errors.New("the selected ref and HEAD have no common ancestor")
		}
		comparisonBase = bases[0]
		result.MergeBaseHash = comparisonBase.Hash.String()
	}

	baseTree, err := comparisonBase.Tree()
	if err != nil {
		return CompareResult{}, fmt.Errorf("read base tree: %w", err)
	}
	headTree, err := headCommit.Tree()
	if err != nil {
		return CompareResult{}, fmt.Errorf("read HEAD tree: %w", err)
	}
	candidates, err := m.worktreeCandidates(ctx, baseTree, headTree)
	if err != nil {
		return CompareResult{}, err
	}
	paths := make([]string, 0, len(candidates))
	for path := range candidates {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return CompareResult{}, err
		}
		candidate := candidates[path]
		originalPath := candidate.originalPath
		if originalPath == "" {
			originalPath = path
		}
		original, originalMode, originalExists, err := treeFile(m.repo, baseTree, m.objectPath(originalPath))
		if err != nil {
			return CompareResult{}, err
		}
		modified, modifiedMode, modifiedExists, err := workingFile(filepath.Join(m.workingDir, filepath.FromSlash(path)))
		if err != nil {
			return CompareResult{}, fmt.Errorf("read working tree file %s: %w", path, err)
		}

		// Preserve staged-only changes when the filesystem has been restored to
		// HEAD after staging, matching the combined Changes view.
		if entry := candidate.status; entry != nil && !entry.conflict && entry.index != git.Unmodified && entry.index != git.Untracked {
			headPath := entry.path
			if entry.originalPath != "" {
				headPath = entry.originalPath
			}
			headData, headMode, headExists, err := treeFile(m.repo, headTree, m.objectPath(headPath))
			if err != nil {
				return CompareResult{}, err
			}
			if headExists == modifiedExists && headMode == modifiedMode && bytes.Equal(headData, modified) {
				modified, modifiedMode, modifiedExists, err = m.indexFile(m.objectPath(path))
				if err != nil {
					return CompareResult{}, err
				}
			}
		}
		if originalPath == path && originalExists == modifiedExists && originalMode == modifiedMode && bytes.Equal(original, modified) {
			continue
		}

		status := StatusModified
		if !originalExists {
			status = StatusAdded
		}
		if !modifiedExists {
			status = StatusDeleted
		}
		patch, err := makePatch(ctx, originalPath, path, status, original, originalMode, modified, modifiedMode)
		if err != nil {
			return CompareResult{}, err
		}
		diff := FileDiff{Path: path, Status: status, Patch: patch}
		if originalPath != path {
			diff.OriginalPath = originalPath
		}
		if !isBinary(original) && !isBinary(modified) {
			diff.Original = string(original)
			diff.Modified = string(modified)
		}
		result.Diffs = append(result.Diffs, diff)
	}
	return result, nil
}

func (m *Manager) worktreeCandidates(ctx context.Context, baseTree, headTree *object.Tree) (map[string]worktreeCandidate, error) {
	candidates := map[string]worktreeCandidate{}
	changes, err := baseTree.DiffContext(ctx, headTree)
	if err != nil {
		return nil, fmt.Errorf("compare base and HEAD trees: %w", err)
	}
	for _, change := range changes {
		action, err := change.Action()
		if err != nil {
			return nil, err
		}
		fromPath, fromInScope := m.scopePath(change.From.Name)
		toPath, toInScope := m.scopePath(change.To.Name)
		switch action {
		case merkletrie.Insert:
			if toInScope {
				candidates[toPath] = worktreeCandidate{path: toPath}
			}
		case merkletrie.Delete:
			if fromInScope {
				candidates[fromPath] = worktreeCandidate{path: fromPath}
			}
		case merkletrie.Modify:
			switch {
			case fromInScope && toInScope:
				candidate := worktreeCandidate{path: toPath}
				if fromPath != toPath {
					candidate.originalPath = fromPath
				}
				candidates[toPath] = candidate
			case !fromInScope && toInScope:
				candidates[toPath] = worktreeCandidate{path: toPath}
			case fromInScope && !toInScope:
				candidates[fromPath] = worktreeCandidate{path: fromPath}
			}
		}
	}

	entries, err := m.status()
	if err != nil {
		return nil, err
	}
	for i := range entries {
		entry := entries[i]
		candidate := candidates[entry.path]
		candidate.path = entry.path
		if entry.originalPath != "" {
			originalPath := entry.originalPath
			if previous, ok := candidates[entry.originalPath]; ok {
				if previous.originalPath != "" {
					originalPath = previous.originalPath
				}
				delete(candidates, entry.originalPath)
			}
			candidate.originalPath = originalPath
		}
		candidate.status = &entries[i]
		candidates[entry.path] = candidate
	}
	return candidates, nil
}

// History returns commits reachable from all local and remote refs. Ref labels
// are attached to their tip commits so the UI can render a compact history tree.
func (m *Manager) History(ctx context.Context) ([]GitCommit, error) {
	if err := m.lock(ctx); err != nil {
		return nil, err
	}
	defer m.mu.Unlock()

	refsByHash := map[plumbing.Hash][]string{}
	references, err := m.repo.References()
	if err != nil {
		return nil, fmt.Errorf("list Git references: %w", err)
	}
	err = references.ForEach(func(ref *plumbing.Reference) error {
		name := ref.Name()
		label := ""
		switch {
		case name.IsBranch():
			label = name.Short()
		case name.IsRemote():
			label = strings.TrimPrefix(name.String(), "refs/remotes/")
			if strings.HasSuffix(label, "/HEAD") {
				return nil
			}
		case name.IsTag():
			label = "tag: " + name.Short()
		}
		if label != "" {
			refsByHash[ref.Hash()] = append(refsByHash[ref.Hash()], label)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list Git references: %w", err)
	}
	for hash := range refsByHash {
		sort.Strings(refsByHash[hash])
	}

	iter, err := m.repo.Log(&git.LogOptions{All: true, Order: git.LogOrderCommitterTime})
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return []GitCommit{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Git history: %w", err)
	}
	defer iter.Close()

	commits := make([]GitCommit, 0, 256)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		commit, err := iter.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read Git history: %w", err)
		}
		parents := make([]string, len(commit.ParentHashes))
		for i, hash := range commit.ParentHashes {
			parents[i] = hash.String()
		}
		commits = append(commits, GitCommit{
			Hash:       commit.Hash.String(),
			Parents:    parents,
			Summary:    commitSummary(commit.Message),
			Author:     commit.Author.Name,
			AuthoredAt: commit.Author.When,
			Refs:       append([]string{}, refsByHash[commit.Hash]...),
		})
	}
	return commits, nil
}

func (m *Manager) resolveCommit(revision string) (*object.Commit, error) {
	revision = strings.TrimSpace(revision)
	if revision == "" {
		return nil, errors.New("revision is required")
	}
	if len(revision) > 256 {
		return nil, errors.New("revision is too long")
	}
	hash, err := m.repo.ResolveRevision(plumbing.Revision(revision))
	if err != nil {
		return nil, err
	}
	commit, err := m.repo.CommitObject(*hash)
	if err != nil {
		return nil, err
	}
	return commit, nil
}

func (m *Manager) committedFileDiff(ctx context.Context, baseTree, headTree *object.Tree, change *object.Change) (FileDiff, bool, error) {
	action, err := change.Action()
	if err != nil {
		return FileDiff{}, false, err
	}

	fromObjectPath := change.From.Name
	toObjectPath := change.To.Name
	fromPath, fromInScope := m.scopePath(fromObjectPath)
	toPath, toInScope := m.scopePath(toObjectPath)

	status := StatusModified
	originalPath := fromPath
	path := toPath
	switch action {
	case merkletrie.Insert:
		if !toInScope {
			return FileDiff{}, false, nil
		}
		status = StatusAdded
		originalPath = ""
	case merkletrie.Delete:
		if !fromInScope {
			return FileDiff{}, false, nil
		}
		status = StatusDeleted
		path = fromPath
	case merkletrie.Modify:
		switch {
		case fromInScope && toInScope:
			// A normal modification or a rename contained by the workspace.
		case !fromInScope && toInScope:
			status = StatusAdded
			originalPath = ""
		case fromInScope && !toInScope:
			status = StatusDeleted
			path = fromPath
		default:
			return FileDiff{}, false, nil
		}
	default:
		return FileDiff{}, false, fmt.Errorf("unsupported tree change %s", action)
	}

	var original, modified []byte
	var originalMode, modifiedMode = change.From.TreeEntry.Mode, change.To.TreeEntry.Mode
	if status != StatusAdded {
		original, originalMode, _, err = treeFile(m.repo, baseTree, fromObjectPath)
		if err != nil {
			return FileDiff{}, false, err
		}
	}
	if status != StatusDeleted {
		modified, modifiedMode, _, err = treeFile(m.repo, headTree, toObjectPath)
		if err != nil {
			return FileDiff{}, false, err
		}
	}
	patchOriginalPath := originalPath
	if patchOriginalPath == "" {
		patchOriginalPath = path
	}
	patch, err := makePatch(ctx, patchOriginalPath, path, status, original, originalMode, modified, modifiedMode)
	if err != nil {
		return FileDiff{}, false, err
	}
	diff := FileDiff{Path: path, OriginalPath: originalPath, Status: status, Patch: patch}
	if !isBinary(original) && !isBinary(modified) {
		diff.Original = string(original)
		diff.Modified = string(modified)
	}
	return diff, true, nil
}

func commitSummary(message string) string {
	message = strings.TrimSpace(message)
	if line, _, ok := strings.Cut(message, "\n"); ok {
		return line
	}
	return message
}
