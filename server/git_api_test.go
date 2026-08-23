package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
)

func TestGitAPIStageCommitPushAndPull(t *testing.T) {
	t.Setenv("WINGMAN_URL", "http://localhost:1")
	repoDir := t.TempDir()
	repo, err := git.PlainInit(repoDir, false)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := repo.Config()
	if err != nil {
		t.Fatal(err)
	}
	cfg.User.Name = "Wingman Test"
	cfg.User.Email = "wingman@test.local"
	if err := repo.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	app, err := New(context.Background(), repoDir, &ServerOptions{NoBrowser: true, disableManagedTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	web := httptest.NewServer(app)
	defer web.Close()

	status := getGitStatus(t, web.URL)
	if len(status.Files) != 1 || status.Files[0].Path != "hello.txt" || !status.Files[0].Changed {
		t.Fatalf("initial status = %+v", status)
	}
	postGit(t, web.URL, "stage", `{"paths":["hello.txt"]}`)
	status = getGitStatus(t, web.URL)
	if len(status.Files) != 1 || !status.Files[0].Staged {
		t.Fatalf("staged status = %+v", status)
	}
	postGit(t, web.URL, "unstage", `{"paths":["hello.txt"]}`)
	status = getGitStatus(t, web.URL)
	if len(status.Files) != 1 || status.Files[0].Staged || !status.Files[0].Changed {
		t.Fatalf("unstaged status = %+v", status)
	}
	postGit(t, web.URL, "stage", `{"paths":["hello.txt"]}`)
	postGit(t, web.URL, "commit", `{"message":"initial commit"}`)
	if status = getGitStatus(t, web.URL); len(status.Files) != 0 {
		t.Fatalf("status after commit = %+v", status)
	}
	mainBranch := status.Branch
	postGit(t, web.URL, "branches", `{"name":"feature/web-picker"}`)
	if status = getGitStatus(t, web.URL); status.Branch != "feature/web-picker" {
		t.Fatalf("status after branch creation = %+v", status)
	}
	branches := getGitBranches(t, web.URL)
	if len(branches.Branches) != 2 || !branches.Branches[0].Current && !branches.Branches[1].Current {
		t.Fatalf("branches = %+v", branches)
	}
	postGit(t, web.URL, "checkout", `{"name":"`+mainBranch+`"}`)
	if status = getGitStatus(t, web.URL); status.Branch != mainBranch {
		t.Fatalf("status after checkout = %+v", status)
	}

	remoteDir := t.TempDir()
	if _, err := git.PlainInit(remoteDir, true); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{remoteDir}}); err != nil {
		t.Fatal(err)
	}
	postGit(t, web.URL, "push", "")
	status = getGitStatus(t, web.URL)
	if status.Upstream == "" {
		t.Fatalf("push did not establish upstream: %+v", status)
	}
	postGit(t, web.URL, "pull", "")
}

func getGitBranches(t *testing.T, baseURL string) GitBranches {
	t.Helper()
	res, err := http.Get(baseURL + "/api/git/branches?refresh=0")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("branches endpoint = %d", res.StatusCode)
	}
	var branches GitBranches
	if err := json.NewDecoder(res.Body).Decode(&branches); err != nil {
		t.Fatal(err)
	}
	return branches
}

func TestGitAPIInitCreatesRepository(t *testing.T) {
	t.Setenv("WINGMAN_URL", "http://localhost:1")
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	app, err := New(context.Background(), workDir, &ServerOptions{NoBrowser: true, disableManagedTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	web := httptest.NewServer(app)
	defer web.Close()

	res, err := http.Get(web.URL + "/api/git/status")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status before init = %d, want %d", res.StatusCode, http.StatusNotFound)
	}
	caps := getCapabilities(t, web.URL)
	assertRequiredCapabilityTypes(t, caps)
	if caps["git"] != false || caps["diffs"] != false || caps["git_init"] != true {
		t.Fatalf("capabilities before init = %v", caps)
	}

	postGit(t, web.URL, "init", "")
	caps = getCapabilities(t, web.URL)
	if caps["git"] != true || caps["diffs"] != true || caps["git_init"] != false {
		t.Fatalf("capabilities after init = %v", caps)
	}
	if _, err := os.Stat(filepath.Join(workDir, ".git")); err != nil {
		t.Fatalf("missing .git directory: %v", err)
	}
	status := getGitStatus(t, web.URL)
	if status.Branch != "main" {
		t.Fatalf("branch after init = %q, want main", status.Branch)
	}
	if len(status.Files) != 1 || status.Files[0].Path != "hello.txt" || !status.Files[0].Changed {
		t.Fatalf("status after init = %+v", status)
	}

	res, err = http.Post(web.URL+"/api/git/init", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("second init = %d, want %d", res.StatusCode, http.StatusConflict)
	}
}

func TestGitAPIInitRemovesDanglingShadowPointer(t *testing.T) {
	t.Setenv("WINGMAN_URL", "http://localhost:1")
	workDir := t.TempDir()
	stale := "gitdir: " + filepath.Join(t.TempDir(), "gone", "changes-old.git") + "\n"
	if err := os.WriteFile(filepath.Join(workDir, ".git"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	app, err := New(context.Background(), workDir, &ServerOptions{NoBrowser: true, disableManagedTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	web := httptest.NewServer(app)
	defer web.Close()

	caps := getCapabilities(t, web.URL)
	if caps["git_init"] != true {
		t.Fatalf("capabilities = %v", caps)
	}
	postGit(t, web.URL, "init", "")
	info, err := os.Stat(filepath.Join(workDir, ".git"))
	if err != nil || !info.IsDir() {
		t.Fatalf(".git after init: info=%v err=%v", info, err)
	}
	if status := getGitStatus(t, web.URL); status.Branch != "main" {
		t.Fatalf("status after init = %+v", status)
	}
}

func TestGitAPIRejectsInvalidPath(t *testing.T) {
	t.Setenv("WINGMAN_URL", "http://localhost:1")
	repoDir := t.TempDir()
	if _, err := git.PlainInit(repoDir, false); err != nil {
		t.Fatal(err)
	}
	app, err := New(context.Background(), repoDir, &ServerOptions{NoBrowser: true, disableManagedTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	web := httptest.NewServer(app)
	defer web.Close()

	res, err := http.Post(web.URL+"/api/git/stage", "application/json", strings.NewReader(`{"paths":["../outside"]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusBadRequest)
	}
}

func TestGitAPIStageAll(t *testing.T) {
	t.Setenv("WINGMAN_URL", "http://localhost:1")
	repoDir := t.TempDir()
	if _, err := git.PlainInit(repoDir, false); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"one.txt", "two.txt"} {
		if err := os.WriteFile(filepath.Join(repoDir, path), []byte(path+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	app, err := New(context.Background(), repoDir, &ServerOptions{NoBrowser: true, disableManagedTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	web := httptest.NewServer(app)
	defer web.Close()

	postGit(t, web.URL, "stage", `{"paths":[]}`)
	status := getGitStatus(t, web.URL)
	if len(status.Files) != 2 || !status.Files[0].Staged || !status.Files[1].Staged {
		t.Fatalf("stage-all status = %+v", status)
	}
}

func TestGitAPIHistoryAndCompare(t *testing.T) {
	t.Setenv("WINGMAN_URL", "http://localhost:1")
	repoDir := t.TempDir()
	repo, err := git.PlainInit(repoDir, false)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := repo.Config()
	if err != nil {
		t.Fatal(err)
	}
	cfg.User.Name = "Wingman Test"
	cfg.User.Email = "wingman@test.local"
	if err := repo.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	app, err := New(context.Background(), repoDir, &ServerOptions{NoBrowser: true, disableManagedTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	web := httptest.NewServer(app)
	defer web.Close()

	postGit(t, web.URL, "stage", `{"paths":["base.txt"]}`)
	postGit(t, web.URL, "commit", `{"message":"initial"}`)
	mainBranch := getGitStatus(t, web.URL).Branch
	postGit(t, web.URL, "branches", `{"name":"feature/compare"}`)
	if err := os.WriteFile(filepath.Join(repoDir, "feature.txt"), []byte("draft\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	postGit(t, web.URL, "stage", `{"paths":["feature.txt"]}`)
	postGit(t, web.URL, "commit", `{"message":"draft feature"}`)
	if err := os.WriteFile(filepath.Join(repoDir, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	postGit(t, web.URL, "stage", `{"paths":["feature.txt"]}`)
	postGit(t, web.URL, "commit", `{"message":"feature commit"}`)

	res, err := http.Get(web.URL + "/api/git/history")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("history endpoint = %d", res.StatusCode)
	}
	var history []GitCommit
	if err := json.NewDecoder(res.Body).Decode(&history); err != nil {
		t.Fatal(err)
	}
	foundFeature := false
	foundUnreferenced := false
	initialHash := ""
	for _, commit := range history {
		foundFeature = foundFeature || commit.Summary == "feature commit"
		foundUnreferenced = foundUnreferenced || commit.Refs != nil && len(commit.Refs) == 0
		if commit.Summary == "initial" {
			initialHash = commit.Hash
		}
	}
	if len(history) != 3 || !foundFeature || !foundUnreferenced || initialHash == "" {
		t.Fatalf("history = %+v", history)
	}

	rootURL := web.URL + "/api/git/compare?base=%3Aempty&head=" + url.QueryEscape(initialHash) + "&mode=direct"
	res, err = http.Get(rootURL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(res.Body)
		t.Fatalf("root comparison endpoint = %d: %s", res.StatusCode, message)
	}
	var rootComparison GitCompare
	if err := json.NewDecoder(res.Body).Decode(&rootComparison); err != nil {
		t.Fatal(err)
	}
	if len(rootComparison.Files) != 1 || rootComparison.Files[0].Path != "base.txt" || rootComparison.Files[0].Status != "added" {
		t.Fatalf("root comparison = %+v", rootComparison)
	}

	compareURL := web.URL + "/api/git/compare?base=" + url.QueryEscape(mainBranch) + "&head=feature%2Fcompare&mode=merge-base"
	res, err = http.Get(compareURL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(res.Body)
		t.Fatalf("compare endpoint = %d: %s", res.StatusCode, message)
	}
	var comparison GitCompare
	if err := json.NewDecoder(res.Body).Decode(&comparison); err != nil {
		t.Fatal(err)
	}
	if comparison.MergeBaseHash == "" || len(comparison.Files) != 1 || comparison.Files[0].Path != "feature.txt" || !strings.Contains(comparison.Files[0].Patch, "+feature") {
		t.Fatalf("comparison = %+v", comparison)
	}
	if comparison.Files[0].Original != "" || comparison.Files[0].Modified != "" {
		t.Fatalf("comparison should omit file contents, got %+v", comparison.Files[0])
	}

	if err := os.WriteFile(filepath.Join(repoDir, "feature.txt"), []byte("working feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "local.txt"), []byte("untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	worktreeURL := web.URL + "/api/git/compare?base=" + url.QueryEscape(mainBranch) + "&head=" + url.QueryEscape(":worktree") + "&mode=merge-base"
	res, err = http.Get(worktreeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(res.Body)
		t.Fatalf("worktree compare endpoint = %d: %s", res.StatusCode, message)
	}
	comparison = GitCompare{}
	if err := json.NewDecoder(res.Body).Decode(&comparison); err != nil {
		t.Fatal(err)
	}
	if comparison.Head != ":worktree" || comparison.MergeBaseHash == "" || len(comparison.Files) != 2 {
		t.Fatalf("worktree comparison = %+v", comparison)
	}
	files := map[string]DiffEntry{}
	for _, file := range comparison.Files {
		files[file.Path] = file
	}
	if !strings.Contains(files["feature.txt"].Patch, "+working feature") || files["local.txt"].Status != "added" {
		t.Fatalf("worktree files = %+v", files)
	}
}

func getCapabilities(t *testing.T, baseURL string) map[string]any {
	t.Helper()
	res, err := http.Get(baseURL + "/api/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("capabilities endpoint = %d", res.StatusCode)
	}
	var caps map[string]any
	if err := json.NewDecoder(res.Body).Decode(&caps); err != nil {
		t.Fatal(err)
	}
	return caps
}

func assertRequiredCapabilityTypes(t *testing.T, caps map[string]any) {
	t.Helper()
	for _, name := range []string{"git", "git_init", "lsp", "debug", "diffs", "tasks", "terminal"} {
		if _, ok := caps[name].(bool); !ok {
			t.Errorf("capability %q = %#v, want non-null boolean", name, caps[name])
		}
	}
	for _, name := range []string{"platform", "workspace_name"} {
		if _, ok := caps[name].(string); !ok {
			t.Errorf("capability %q = %#v, want non-null string", name, caps[name])
		}
	}
}

func getGitStatus(t *testing.T, baseURL string) GitStatus {
	t.Helper()
	res, err := http.Get(baseURL + "/api/git/status")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status endpoint = %d", res.StatusCode)
	}
	var status GitStatus
	if err := json.NewDecoder(res.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

func postGit(t *testing.T, baseURL, action, body string) {
	t.Helper()
	res, err := http.Post(baseURL+"/api/git/"+action, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		message, _ := io.ReadAll(res.Body)
		t.Fatalf("%s status = %d: %s", action, res.StatusCode, message)
	}
}
