package changes

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestNativeRepositoryFingerprintIncludesIndexAndHead(t *testing.T) {
	dir := t.TempDir()
	repo := initRepository(t, dir)
	writeFile(t, dir, "file.txt", "one\n")
	stage(t, repo, "file.txt")
	commit(t, repo, "initial")

	m := New(dir)
	defer m.Close()
	clean := m.Fingerprint(context.Background())

	writeFile(t, dir, "file.txt", "two\n")
	dirty := m.Fingerprint(context.Background())
	if dirty == clean {
		t.Fatal("working-tree edit did not change fingerprint")
	}
	stage(t, repo, "file.txt")
	staged := m.Fingerprint(context.Background())
	if staged == dirty {
		t.Fatal("index update did not change fingerprint")
	}
	commit(t, repo, "second")
	committed := m.Fingerprint(context.Background())
	if committed == staged || committed == clean {
		t.Fatal("HEAD update did not change fingerprint")
	}
}

func TestNativeRepositoryShowsStagedOnlyContent(t *testing.T) {
	dir := t.TempDir()
	repo := initRepository(t, dir)
	writeFile(t, dir, "file.txt", "one\n")
	stage(t, repo, "file.txt")
	commit(t, repo, "initial")

	writeFile(t, dir, "file.txt", "two\n")
	stage(t, repo, "file.txt")
	writeFile(t, dir, "file.txt", "one\n")

	m := New(dir)
	defer m.Close()
	diffs, err := m.Diffs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 1 || diffs[0].Original != "one\n" || diffs[0].Modified != "two\n" || !strings.Contains(diffs[0].Patch, "+two") {
		t.Fatalf("diffs = %+v", diffs)
	}
}

func TestNativeRepositoryDiffLayersAndRevertPreservesIndex(t *testing.T) {
	dir := t.TempDir()
	repo := initRepository(t, dir)
	writeFile(t, dir, "file.txt", "one\n")
	stage(t, repo, "file.txt")
	commit(t, repo, "initial")
	writeFile(t, dir, "file.txt", "two\n")
	stage(t, repo, "file.txt")
	writeFile(t, dir, "file.txt", "three\n")

	m := New(dir)
	defer m.Close()
	staged, err := m.Diff(context.Background(), "file.txt", DiffStaged)
	if err != nil || staged.Original != "one\n" || staged.Modified != "two\n" {
		t.Fatalf("staged diff = %+v, %v", staged, err)
	}
	unstaged, err := m.Diff(context.Background(), "file.txt", DiffUnstaged)
	if err != nil || unstaged.Original != "two\n" || unstaged.Modified != "three\n" {
		t.Fatalf("unstaged diff = %+v, %v", unstaged, err)
	}
	if err := m.Revert(context.Background(), "file.txt"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, dir, "file.txt"); got != "two\n" {
		t.Fatalf("worktree after revert = %q", got)
	}
	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	status, err := worktree.Status()
	if err != nil || status["file.txt"].Staging != git.Modified || status["file.txt"].Worktree != git.Unmodified {
		t.Fatalf("status = %q, %v", status, err)
	}
}

func TestNativeRepositoryRevertRejectsStagedOnlyChange(t *testing.T) {
	dir := t.TempDir()
	repo := initRepository(t, dir)
	writeFile(t, dir, "file.txt", "content\n")
	stage(t, repo, "file.txt")

	m := New(dir)
	defer m.Close()
	if err := m.Revert(context.Background(), "file.txt"); err == nil {
		t.Fatal("staged-only revert succeeded")
	}
	if got := readFile(t, dir, "file.txt"); got != "content\n" {
		t.Fatalf("staged file after rejected revert = %q", got)
	}
}

func TestNativeSubdirectoryCommitRejectsStagedChangesOutsideScope(t *testing.T) {
	dir := t.TempDir()
	repo := initRepository(t, dir)
	writeFile(t, dir, "sub/inside.txt", "one\n")
	writeFile(t, dir, "outside.txt", "one\n")
	stage(t, repo, "sub/inside.txt")
	stage(t, repo, "outside.txt")
	commit(t, repo, "initial")
	writeFile(t, dir, "sub/inside.txt", "two\n")
	writeFile(t, dir, "outside.txt", "two\n")
	stage(t, repo, "sub/inside.txt")
	stage(t, repo, "outside.txt")

	m := New(filepath.Join(dir, "sub"))
	defer m.Close()
	_, err := m.Commit(context.Background(), "scoped commit")
	if err == nil || !strings.Contains(err.Error(), "outside.txt") {
		t.Fatalf("commit error = %v", err)
	}
	status, statusErr := m.GitStatus(context.Background())
	if statusErr != nil || len(status.Files) != 1 || status.Files[0].Path != "inside.txt" || !status.Files[0].Staged {
		t.Fatalf("scoped status = %+v, %v", status, statusErr)
	}
}

func TestNativeStageAllIsScopedToWorkspace(t *testing.T) {
	dir := t.TempDir()
	repo := initRepository(t, dir)
	writeFile(t, dir, "sub/modified.txt", "one\n")
	writeFile(t, dir, "sub/deleted.txt", "one\n")
	writeFile(t, dir, "outside.txt", "one\n")
	stage(t, repo, "sub/modified.txt")
	stage(t, repo, "sub/deleted.txt")
	stage(t, repo, "outside.txt")
	commit(t, repo, "initial")

	writeFile(t, dir, "sub/modified.txt", "two\n")
	if err := os.Remove(filepath.Join(dir, "sub/deleted.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "sub/added.txt", "two\n")
	writeFile(t, dir, "outside.txt", "two\n")

	m := New(filepath.Join(dir, "sub"))
	defer m.Close()
	if err := m.Stage(context.Background(), nil); err != nil {
		t.Fatal(err)
	}

	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	files, err := worktree.Status()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"sub/modified.txt", "sub/deleted.txt", "sub/added.txt"} {
		file := files.File(path)
		if file.Staging == git.Unmodified || file.Worktree != git.Unmodified {
			t.Fatalf("stage-all status for %s = %c%c", path, file.Staging, file.Worktree)
		}
	}
	outside := files.File("outside.txt")
	if outside.Staging != git.Unmodified || outside.Worktree != git.Modified {
		t.Fatalf("outside status = %c%c", outside.Staging, outside.Worktree)
	}
}

func TestNativeRepositoryFromSubdirectory(t *testing.T) {
	dir := t.TempDir()
	repo := initRepository(t, dir)
	writeFile(t, dir, "sub/file.txt", "one\n")
	stage(t, repo, "sub/file.txt")
	commit(t, repo, "initial")

	subdir := filepath.Join(dir, "sub")
	m := New(subdir)
	defer m.Close()
	writeFile(t, dir, "sub/file.txt", "two\n")

	diffs, err := m.Diffs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 1 || diffs[0].Path != "file.txt" || diffs[0].Original != "one\n" || diffs[0].Modified != "two\n" {
		t.Fatalf("diffs = %+v", diffs)
	}
}

func TestNativeRepositoryCompareAndHistory(t *testing.T) {
	dir := t.TempDir()
	repo := initRepository(t, dir)
	writeFile(t, dir, "shared.txt", "base\n")
	stage(t, repo, "shared.txt")
	commit(t, repo, "initial")
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	mainBranch := head.Name()

	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	featureBranch := plumbing.NewBranchReferenceName("feature/compare")
	if err := worktree.Checkout(&git.CheckoutOptions{Branch: featureBranch, Create: true}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "feature.txt", "feature\n")
	stage(t, repo, "feature.txt")
	commit(t, repo, "feature commit\n\nDetails")
	if err := worktree.Checkout(&git.CheckoutOptions{Branch: mainBranch}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "main.txt", "main\n")
	stage(t, repo, "main.txt")
	commit(t, repo, "main commit")

	m := New(dir)
	defer m.Close()
	root, err := m.Compare(context.Background(), EmptyTreeRevision, head.Hash().String(), false)
	if err != nil {
		t.Fatal(err)
	}
	if root.BaseHash != "" || root.HeadHash != head.Hash().String() || len(root.Diffs) != 1 || root.Diffs[0].Path != "shared.txt" || root.Diffs[0].Status != StatusAdded {
		t.Fatalf("root comparison = %+v", root)
	}
	direct, err := m.Compare(context.Background(), mainBranch.Short(), featureBranch.Short(), false)
	if err != nil {
		t.Fatal(err)
	}
	if direct.MergeBaseHash != "" || len(direct.Diffs) != 2 {
		t.Fatalf("direct comparison = %+v", direct)
	}
	if direct.Diffs[0].Path != "feature.txt" || direct.Diffs[0].Status != StatusAdded || direct.Diffs[0].Modified != "feature\n" {
		t.Fatalf("feature diff = %+v", direct.Diffs[0])
	}
	if direct.Diffs[1].Path != "main.txt" || direct.Diffs[1].Status != StatusDeleted || direct.Diffs[1].Original != "main\n" {
		t.Fatalf("main diff = %+v", direct.Diffs[1])
	}

	pullRequest, err := m.Compare(context.Background(), mainBranch.Short(), featureBranch.Short(), true)
	if err != nil {
		t.Fatal(err)
	}
	if pullRequest.MergeBaseHash == "" || len(pullRequest.Diffs) != 1 || pullRequest.Diffs[0].Path != "feature.txt" {
		t.Fatalf("merge-base comparison = %+v", pullRequest)
	}

	writeFile(t, dir, "shared.txt", "working tree\n")
	writeFile(t, dir, "untracked.txt", "local\n")
	workingTree, err := m.Compare(context.Background(), mainBranch.Short(), WorktreeRevision, false)
	if err != nil {
		t.Fatal(err)
	}
	if workingTree.HeadHash == "" || len(workingTree.Diffs) != 2 {
		t.Fatalf("working tree comparison = %+v", workingTree)
	}
	workingDiffs := map[string]FileDiff{}
	for _, diff := range workingTree.Diffs {
		workingDiffs[diff.Path] = diff
	}
	if workingDiffs["shared.txt"].Modified != "working tree\n" || workingDiffs["untracked.txt"].Status != StatusAdded {
		t.Fatalf("working tree diffs = %+v", workingDiffs)
	}

	history, err := m.History(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 {
		t.Fatalf("history = %+v", history)
	}
	refs := map[string]bool{}
	for _, entry := range history {
		if entry.Refs == nil {
			t.Fatalf("history refs must encode as an empty list: %+v", entry)
		}
		if entry.Summary == "feature commit" && entry.Author == "test" {
			refs[strings.Join(entry.Refs, ",")] = true
		}
	}
	if !refs[featureBranch.Short()] {
		t.Fatalf("feature ref missing from history: %+v", history)
	}
	if _, err := m.Compare(context.Background(), "missing", featureBranch.Short(), false); err == nil {
		t.Fatal("comparison with a missing ref succeeded")
	}
}

func TestNativeRepositoryCompareWorktreeWithFileInNewDirectory(t *testing.T) {
	dir := t.TempDir()
	repo := initRepository(t, dir)
	writeFile(t, dir, "README.md", "base\n")
	stage(t, repo, "README.md")
	commit(t, repo, "initial")
	base, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "examples/coding-router.yaml", "router: coding\n")
	stage(t, repo, "examples/coding-router.yaml")
	commit(t, repo, "add coding router")

	m := New(dir)
	defer m.Close()
	comparison, err := m.Compare(context.Background(), base.Hash().String(), WorktreeRevision, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(comparison.Diffs) != 1 {
		t.Fatalf("working tree comparison = %+v", comparison)
	}
	diff := comparison.Diffs[0]
	if diff.Path != "examples/coding-router.yaml" || diff.Status != StatusAdded || diff.Modified != "router: coding\n" {
		t.Fatalf("nested added file diff = %+v", diff)
	}
}

func TestNativeRepositoryComparePathTypeChanges(t *testing.T) {
	t.Run("file to directory", func(t *testing.T) {
		dir := t.TempDir()
		repo := initRepository(t, dir)
		writeFile(t, dir, "entry", "file\n")
		stage(t, repo, "entry")
		commit(t, repo, "file")
		base, err := repo.Head()
		if err != nil {
			t.Fatal(err)
		}

		remove(t, repo, "entry")
		writeFile(t, dir, "entry/nested.txt", "nested\n")
		stage(t, repo, "entry/nested.txt")
		commit(t, repo, "directory")

		assertComparePaths(t, dir, base.Hash().String(),
			map[string]FileStatus{"entry": StatusDeleted, "entry/nested.txt": StatusAdded})
	})

	t.Run("directory to file", func(t *testing.T) {
		dir := t.TempDir()
		repo := initRepository(t, dir)
		writeFile(t, dir, "entry/nested.txt", "nested\n")
		stage(t, repo, "entry/nested.txt")
		commit(t, repo, "directory")
		base, err := repo.Head()
		if err != nil {
			t.Fatal(err)
		}

		remove(t, repo, "entry/nested.txt")
		if err := os.Remove(filepath.Join(dir, "entry")); err != nil {
			t.Fatal(err)
		}
		writeFile(t, dir, "entry", "file\n")
		stage(t, repo, "entry")
		commit(t, repo, "file")

		assertComparePaths(t, dir, base.Hash().String(),
			map[string]FileStatus{"entry": StatusAdded, "entry/nested.txt": StatusDeleted})
	})

	t.Run("directory to symlink", func(t *testing.T) {
		dir := t.TempDir()
		repo := initRepository(t, dir)
		writeFile(t, dir, "entry/nested.txt", "entry\n")
		writeFile(t, dir, "target/nested.txt", "target\n")
		stage(t, repo, "entry/nested.txt")
		stage(t, repo, "target/nested.txt")
		commit(t, repo, "directory")
		base, err := repo.Head()
		if err != nil {
			t.Fatal(err)
		}

		remove(t, repo, "entry/nested.txt")
		if err := os.Remove(filepath.Join(dir, "entry")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("target", filepath.Join(dir, "entry")); err != nil {
			t.Fatal(err)
		}
		stage(t, repo, "entry")
		commit(t, repo, "symlink")

		assertComparePaths(t, dir, base.Hash().String(),
			map[string]FileStatus{"entry": StatusAdded, "entry/nested.txt": StatusDeleted})
	})
}

func assertComparePaths(t *testing.T, dir, base string, want map[string]FileStatus) {
	t.Helper()
	m := New(dir)
	defer m.Close()
	for name, head := range map[string]string{"commit": "HEAD", "worktree": WorktreeRevision} {
		comparison, err := m.Compare(context.Background(), base, head, false)
		if err != nil {
			t.Fatalf("%s comparison: %v", name, err)
		}
		got := make(map[string]FileStatus, len(comparison.Diffs))
		for _, diff := range comparison.Diffs {
			got[diff.Path] = diff.Status
		}
		if len(got) != len(want) {
			t.Fatalf("%s comparison paths = %+v, want %+v", name, got, want)
		}
		for path, status := range want {
			if got[path] != status {
				t.Fatalf("%s comparison path %q status = %v, want %v", name, path, got[path], status)
			}
		}
	}
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, dir, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func initRepository(t *testing.T, dir string) *git.Repository {
	t.Helper()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func stage(t *testing.T, repo *git.Repository, path string) {
	t.Helper()
	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Add(path); err != nil {
		t.Fatal(err)
	}
}

func remove(t *testing.T, repo *git.Repository, path string) {
	t.Helper()
	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func commit(t *testing.T, repo *git.Repository, message string) {
	t.Helper()
	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	signature := &object.Signature{Name: "test", Email: "test@example.com", When: time.Now()}
	if _, err := worktree.Commit(message, &git.CommitOptions{Author: signature, Committer: signature}); err != nil {
		t.Fatal(err)
	}
}
