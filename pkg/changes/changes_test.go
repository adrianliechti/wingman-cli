package changes

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestShadowRepositoryDiffAndRevert(t *testing.T) {
	t.Setenv("PATH", "")
	dir := t.TempDir()
	writeFile(t, dir, "kept.txt", "before\n")
	writeFile(t, dir, "deleted.txt", "delete me\n")

	m := New(dir, filepath.Join(t.TempDir(), "changes.git"), false)
	defer m.Close()
	if diffs, err := m.Diffs(context.Background()); err != nil || len(diffs) != 0 {
		t.Fatalf("initial diffs = %+v, %v", diffs, err)
	}

	writeFile(t, dir, "kept.txt", "after\n")
	writeFile(t, dir, "added.txt", "added\n")
	if err := os.Remove(filepath.Join(dir, "deleted.txt")); err != nil {
		t.Fatal(err)
	}

	diffs, err := m.Diffs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]FileDiff{}
	for _, diff := range diffs {
		byPath[diff.Path] = diff
	}
	if len(byPath) != 3 {
		t.Fatalf("diffs = %+v", diffs)
	}
	if got := byPath["kept.txt"]; got.Status != StatusModified || got.Original != "before\n" || got.Modified != "after\n" {
		t.Fatalf("modified diff = %+v", got)
	}
	if got := byPath["added.txt"]; got.Status != StatusAdded || !strings.Contains(got.Patch, "+added") {
		t.Fatalf("added diff = %+v", got)
	}
	if got := byPath["deleted.txt"]; got.Status != StatusDeleted {
		t.Fatalf("deleted diff = %+v", got)
	}

	for _, path := range []string{"kept.txt", "added.txt", "deleted.txt"} {
		if err := m.Revert(context.Background(), path); err != nil {
			t.Fatalf("revert %s: %v", path, err)
		}
	}
	if got := readFile(t, dir, "kept.txt"); got != "before\n" {
		t.Fatalf("kept.txt = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "added.txt")); !os.IsNotExist(err) {
		t.Fatalf("added.txt still exists: %v", err)
	}
	if got := readFile(t, dir, "deleted.txt"); got != "delete me\n" {
		t.Fatalf("deleted.txt = %q", got)
	}
}

func TestNativeRepositoryFingerprintIncludesIndexAndHead(t *testing.T) {
	dir := t.TempDir()
	repo := initRepository(t, dir)
	writeFile(t, dir, "file.txt", "one\n")
	stage(t, repo, "file.txt")
	commit(t, repo, "initial")

	m := New(dir, "", true)
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

	m := New(dir, "", true)
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

	m := New(dir, "", true)
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

	m := New(dir, "", true)
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

	m := New(filepath.Join(dir, "sub"), "", true)
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

func TestNativeRepositoryFromSubdirectory(t *testing.T) {
	dir := t.TempDir()
	repo := initRepository(t, dir)
	writeFile(t, dir, "sub/file.txt", "one\n")
	stage(t, repo, "sub/file.txt")
	commit(t, repo, "initial")

	subdir := filepath.Join(dir, "sub")
	m := New(subdir, "", true)
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
