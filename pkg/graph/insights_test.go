package graph

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestGitInsights(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}

	commit := func(author string, when time.Time, files ...string) {
		t.Helper()
		for _, f := range files {
			if _, err := wt.Add(f); err != nil {
				t.Fatal(err)
			}
		}
		sig := &object.Signature{Name: author, Email: author + "@example.com", When: when}
		if _, err := wt.Commit("m", &git.CommitOptions{Author: sig, Committer: sig}); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now()
	writeFile(t, dir, "a.go", "package p\n")
	commit("alice", now.AddDate(0, 0, -14), "a.go")
	writeFile(t, dir, "a.go", "package p\n// v2\n")
	writeFile(t, dir, "b.go", "package p\n")
	commit("bob", now.AddDate(0, 0, -7), "a.go", "b.go")
	writeFile(t, dir, "a.go", "package p\n// v3\n")
	commit("alice", now, "a.go")

	e := New(dir, filepath.Join(t.TempDir(), "graph.json"))
	res, err := e.GitInsights(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if res.Commits != 3 {
		t.Fatalf("commits = %d, want 3", res.Commits)
	}
	if len(res.Weeks) != insightsWeeks {
		t.Fatalf("weeks = %d, want %d", len(res.Weeks), insightsWeeks)
	}
	total := 0
	for _, w := range res.Weeks {
		total += w.Commits
	}
	if total != 3 {
		t.Fatalf("weekly commit sum = %d, want 3", total)
	}

	if len(res.Authors) != 2 || res.Authors[0].Name != "alice" || res.Authors[0].Commits != 2 {
		t.Fatalf("authors = %+v, want alice first with 2 commits", res.Authors)
	}
	if res.TotalAuthors != 2 {
		t.Fatalf("total authors = %d, want 2", res.TotalAuthors)
	}

	if len(res.AuthorWeeks) != 2 || res.AuthorWeeks[0].Name != "alice" {
		t.Fatalf("author weeks = %+v, want alice first", res.AuthorWeeks)
	}
	seriesTotal := 0
	for _, s := range res.AuthorWeeks {
		if len(s.Weeks) != insightsWeeks {
			t.Fatalf("series %s has %d weeks, want %d", s.Name, len(s.Weeks), insightsWeeks)
		}
		for _, n := range s.Weeks {
			seriesTotal += n
		}
	}
	if seriesTotal != 3 {
		t.Fatalf("stacked series sum = %d, want 3", seriesTotal)
	}

	punchTotal := 0
	for _, day := range res.Punch {
		for _, n := range day {
			punchTotal += n
		}
	}
	if punchTotal != 3 {
		t.Fatalf("punch card sum = %d, want 3", punchTotal)
	}

	if len(res.Modules) != 1 || res.Modules[0].Module != "(root)" || res.Modules[0].Commits != 3 {
		t.Fatalf("modules = %+v, want (root) with 3 commits", res.Modules)
	}

	if len(res.Churn) == 0 || res.Churn[0].File != "a.go" || res.Churn[0].Commits != 3 || res.Churn[0].Authors != 2 {
		t.Fatalf("churn = %+v, want a.go with 3 commits by 2 authors", res.Churn)
	}
	if len(res.Churn[0].RecentAuthors) != 2 || res.Churn[0].RecentAuthors[0].Name != "alice" || res.Churn[0].RecentAuthors[0].Commits != 2 {
		t.Fatalf("recent authors = %+v, want alice first with 2 commits", res.Churn[0].RecentAuthors)
	}
}

func TestGitInsightsKeepsTotalAuthorCountWhenListIsCapped(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}

	when := time.Now()
	for i := 0; i < insightsTopAuthors+2; i++ {
		writeFile(t, dir, "owned.go", fmt.Sprintf("package p\n// revision %d\n", i))
		if _, err := wt.Add("owned.go"); err != nil {
			t.Fatal(err)
		}
		name := fmt.Sprintf("author-%02d", i)
		sig := &object.Signature{Name: name, Email: name + "@example.com", When: when}
		if _, err := wt.Commit("m", &git.CommitOptions{Author: sig, Committer: sig}); err != nil {
			t.Fatal(err)
		}
	}

	e := New(dir, filepath.Join(t.TempDir(), "graph.json"))
	res, err := e.GitInsights(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalAuthors != insightsTopAuthors+2 {
		t.Fatalf("total authors = %d, want %d", res.TotalAuthors, insightsTopAuthors+2)
	}
	if len(res.Authors) != insightsTopAuthors {
		t.Fatalf("displayed authors = %d, want %d", len(res.Authors), insightsTopAuthors)
	}
	if len(res.Churn) != 1 || len(res.Churn[0].RecentAuthors) != 3 {
		t.Fatalf("recent author preview = %+v, want three entries", res.Churn)
	}
}

func TestGitInsightsDoesNotStopAtAnOldDatedCommit(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	commit := func(name string, when time.Time, content string) {
		t.Helper()
		writeFile(t, dir, "dated.go", content)
		if _, err := wt.Add("dated.go"); err != nil {
			t.Fatal(err)
		}
		sig := &object.Signature{Name: name, Email: name + "@example.com", When: when}
		if _, err := wt.Commit("m", &git.CommitOptions{Author: sig, Committer: sig}); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now()
	commit("recent", now, "package p\n")
	commit("old-head", now.AddDate(-2, 0, 0), "package p\n// old timestamp\n")

	e := New(dir, filepath.Join(t.TempDir(), "graph.json"))
	res, err := e.GitInsights(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Commits != 1 || res.TotalAuthors != 1 || len(res.Authors) != 1 || res.Authors[0].Name != "recent" {
		t.Fatalf("insights = %+v, want the recent parent despite the old-dated head", res)
	}
}
