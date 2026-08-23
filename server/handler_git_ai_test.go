package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/adrianliechti/wingman-agent/pkg/changes"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestGitCommitMessagePromptIsBoundedAndUsesRecentStyle(t *testing.T) {
	diffs := []changes.FileDiff{{
		Path:  "src/example.go",
		Patch: "diff --git a/src/example.go b/src/example.go\n" + strings.Repeat("+😀 change\n", gitCommitMessageMaxPrompt),
	}}
	history := []changes.GitCommit{
		{Summary: "  Add focused behavior\nwith whitespace  "},
		{Summary: strings.Repeat("x", gitCommitSubjectMaxBytes+100)},
	}
	prompt, err := buildGitCommitMessagePrompt(diffs, history)
	if err != nil {
		t.Fatal(err)
	}
	if len(prompt) > gitCommitMessageMaxPrompt {
		t.Fatalf("prompt length = %d, max = %d", len(prompt), gitCommitMessageMaxPrompt)
	}
	if !utf8.ValidString(prompt) {
		t.Fatal("bounded prompt split a UTF-8 sequence")
	}
	for _, want := range []string{
		"- Add focused behavior with whitespace\n",
		"<STAGED_CHANGES>\n",
		"--- src/example.go ---",
		"[diff truncated]",
		"</STAGED_CHANGES>",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}
}

func TestGitCommitMessageServiceValidatesModelOutput(t *testing.T) {
	service := &gitCommitMessageService{
		complete: func(context.Context, string) (gitCommitMessageCompletion, error) {
			return gitCommitMessageCompletion{Message: "  Improve staged workflow\n\nExplain why.  "}, nil
		},
	}
	message, err := service.generate(context.Background(), []changes.FileDiff{{Path: "a.go", Patch: "+change"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if message != "Improve staged workflow\n\nExplain why." {
		t.Fatalf("message = %q", message)
	}
	service.complete = func(context.Context, string) (gitCommitMessageCompletion, error) {
		return gitCommitMessageCompletion{Message: " \n\t "}, nil
	}
	if _, err := service.generate(context.Background(), []changes.FileDiff{{Path: "a.go", Patch: "+change"}}, nil); err == nil {
		t.Fatal("empty generated message was accepted")
	}
}

func TestHandleGitCommitMessageUsesOnlyStagedChanges(t *testing.T) {
	dir, repo := commitMessageRepository(t)
	writeCommitMessageFile(t, dir, "example.txt", "staged content\n")
	stageCommitMessageFile(t, repo, "example.txt")
	writeCommitMessageFile(t, dir, "example.txt", "worktree-only content\n")

	manager := changes.New(dir)
	defer manager.Close()
	server := &Server{
		workspace: &code.Workspace{Changes: manager},
		commitMessages: &gitCommitMessageService{
			complete: func(_ context.Context, prompt string) (gitCommitMessageCompletion, error) {
				for _, want := range []string{"initial style", "+staged content"} {
					if !strings.Contains(prompt, want) {
						t.Fatalf("prompt missing %q:\n%s", want, prompt)
					}
				}
				if strings.Contains(prompt, "worktree-only content") {
					t.Fatalf("unstaged content leaked into prompt:\n%s", prompt)
				}
				return gitCommitMessageCompletion{Message: "Describe staged content"}, nil
			},
		},
	}

	request := httptest.NewRequest(http.MethodPost, "/api/git/commit-message", nil)
	response := httptest.NewRecorder()
	server.handleGitCommitMessage(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var result gitCommitMessageResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Message != "Describe staged content" {
		t.Fatalf("message = %q", result.Message)
	}
	data, err := os.ReadFile(filepath.Join(dir, "example.txt"))
	if err != nil || string(data) != "worktree-only content\n" {
		t.Fatalf("worktree changed = %q, %v", data, err)
	}
}

func TestHandleGitCommitMessageRejectsMissingOrChangingStage(t *testing.T) {
	t.Run("no staged changes", func(t *testing.T) {
		dir, _ := commitMessageRepository(t)
		writeCommitMessageFile(t, dir, "example.txt", "unstaged\n")
		manager := changes.New(dir)
		defer manager.Close()
		called := false
		server := &Server{
			workspace: &code.Workspace{Changes: manager},
			commitMessages: &gitCommitMessageService{complete: func(context.Context, string) (gitCommitMessageCompletion, error) {
				called = true
				return gitCommitMessageCompletion{Message: "unused"}, nil
			}},
		}
		response := httptest.NewRecorder()
		server.handleGitCommitMessage(response, httptest.NewRequest(http.MethodPost, "/api/git/commit-message", nil))
		if response.Code != http.StatusConflict || called {
			t.Fatalf("status = %d, model called = %t, body = %s", response.Code, called, response.Body.String())
		}
	})

	t.Run("stage changes during generation", func(t *testing.T) {
		dir, repo := commitMessageRepository(t)
		writeCommitMessageFile(t, dir, "example.txt", "first staged\n")
		stageCommitMessageFile(t, repo, "example.txt")
		manager := changes.New(dir)
		defer manager.Close()
		server := &Server{
			workspace: &code.Workspace{Changes: manager},
			commitMessages: &gitCommitMessageService{complete: func(context.Context, string) (gitCommitMessageCompletion, error) {
				writeCommitMessageFile(t, dir, "example.txt", "second staged\n")
				stageCommitMessageFile(t, repo, "example.txt")
				return gitCommitMessageCompletion{Message: "Stale message"}, nil
			}},
		}
		response := httptest.NewRecorder()
		server.handleGitCommitMessage(response, httptest.NewRequest(http.MethodPost, "/api/git/commit-message", nil))
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "changed while") {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
	})
}

func commitMessageRepository(t *testing.T) (string, *git.Repository) {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	writeCommitMessageFile(t, dir, "example.txt", "initial\n")
	stageCommitMessageFile(t, repo, "example.txt")
	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	signature := &object.Signature{
		Name: "test", Email: "test@example.com", When: time.Unix(1_700_000_000, 0),
	}
	if _, err := worktree.Commit("initial style", &git.CommitOptions{Author: signature, Committer: signature}); err != nil {
		t.Fatal(err)
	}
	return dir, repo
}

func writeCommitMessageFile(t *testing.T, dir, path, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func stageCommitMessageFile(t *testing.T, repo *git.Repository, path string) {
	t.Helper()
	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Add(path); err != nil {
		t.Fatal(err)
	}
}
