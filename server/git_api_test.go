package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

	app, err := New(context.Background(), repoDir, &ServerOptions{NoBrowser: true})
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

func TestGitAPIRejectsInvalidPath(t *testing.T) {
	t.Setenv("WINGMAN_URL", "http://localhost:1")
	repoDir := t.TempDir()
	if _, err := git.PlainInit(repoDir, false); err != nil {
		t.Fatal(err)
	}
	app, err := New(context.Background(), repoDir, &ServerOptions{NoBrowser: true})
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
