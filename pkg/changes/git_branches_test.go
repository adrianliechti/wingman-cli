package changes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

func TestNativeBranchesCreateCheckoutAndProtectChanges(t *testing.T) {
	dir := t.TempDir()
	repo := initRepository(t, dir)
	writeFile(t, dir, "file.txt", "main\n")
	stage(t, repo, "file.txt")
	commit(t, repo, "initial")

	m := New(dir, "", true)
	defer m.Close()
	branches, warning, err := m.Branches(context.Background(), false)
	if err != nil || warning != "" || len(branches) != 1 || !branches[0].Current {
		t.Fatalf("branches = %+v, warning = %q, err = %v", branches, warning, err)
	}
	mainBranch := branches[0].Name

	if err := m.CreateBranch(context.Background(), "feature/picker"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "feature.txt", "feature\n")
	stage(t, repo, "feature.txt")
	commit(t, repo, "feature commit")
	status, err := m.GitStatus(context.Background())
	if err != nil || status.Branch != "feature/picker" {
		t.Fatalf("status = %+v, err = %v", status, err)
	}
	if err := m.CheckoutBranch(context.Background(), mainBranch, ""); err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "local.txt", "dirty\n")
	if err := m.CreateBranch(context.Background(), "feature/dirty-work"); err != nil {
		t.Fatalf("create branch with changes: %v", err)
	}
	status, err = m.GitStatus(context.Background())
	if err != nil || status.Branch != "feature/dirty-work" || len(status.Files) != 1 || !status.Files[0].Changed {
		t.Fatalf("dirty branch status = %+v, err = %v", status, err)
	}
	if err := m.CheckoutBranch(context.Background(), mainBranch, ""); err != nil {
		t.Fatalf("switch between branches at the same commit: %v", err)
	}
	if got := readFile(t, dir, "local.txt"); got != "dirty\n" {
		t.Fatalf("local change after checkout = %q", got)
	}
	if err := m.CheckoutBranch(context.Background(), "feature/picker", ""); !errors.Is(err, ErrDirtyWorktree) {
		t.Fatalf("dirty checkout error = %v", err)
	}
}

func TestNativeBranchesFetchAndTrackRemoteBranch(t *testing.T) {
	dir := t.TempDir()
	repo := initRepository(t, dir)
	writeFile(t, dir, "file.txt", "main\n")
	stage(t, repo, "file.txt")
	commit(t, repo, "initial")
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}

	remoteDir := t.TempDir()
	if _, err := git.PlainInit(remoteDir, true); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{remoteDir}}); err != nil {
		t.Fatal(err)
	}
	refspec := config.RefSpec(head.Name().String() + ":refs/heads/remote-feature")
	if err := repo.Push(&git.PushOptions{RemoteName: "origin", RefSpecs: []config.RefSpec{refspec}}); err != nil {
		t.Fatal(err)
	}

	m := New(dir, "", true)
	defer m.Close()
	branches, warning, err := m.Branches(context.Background(), true)
	if err != nil || warning != "" {
		t.Fatalf("warning = %q, err = %v", warning, err)
	}
	found := false
	for _, branch := range branches {
		if branch.Remote == "origin" && branch.Name == "remote-feature" {
			found = true
		}
	}
	if !found {
		t.Fatalf("remote branch missing from %+v", branches)
	}
	if err := m.CheckoutBranch(context.Background(), "remote-feature", "origin"); err != nil {
		t.Fatal(err)
	}
	status, err := m.GitStatus(context.Background())
	if err != nil || status.Branch != "remote-feature" || status.Upstream != "origin/remote-feature" {
		t.Fatalf("status = %+v, err = %v", status, err)
	}
	if ref, err := repo.Reference(plumbing.NewBranchReferenceName("remote-feature"), true); err != nil || ref.Hash() != head.Hash() {
		t.Fatalf("local tracking ref = %v, err = %v", ref, err)
	}
}

func TestRemoteCheckoutRejectsDivergentExistingLocalBranch(t *testing.T) {
	dir := t.TempDir()
	repo := initRepository(t, dir)
	writeFile(t, dir, "file.txt", "base\n")
	stage(t, repo, "file.txt")
	commit(t, repo, "initial")
	base, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}

	remoteDir := t.TempDir()
	if _, err := git.PlainInit(remoteDir, true); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{remoteDir}}); err != nil {
		t.Fatal(err)
	}
	refspec := config.RefSpec(base.Name().String() + ":refs/heads/topic")
	if err := repo.Push(&git.PushOptions{RemoteName: "origin", RefSpecs: []config.RefSpec{refspec}}); err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "file.txt", "local\n")
	stage(t, repo, "file.txt")
	commit(t, repo, "local commit")
	local, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName("topic"), local.Hash())); err != nil {
		t.Fatal(err)
	}

	m := New(dir, "", true)
	defer m.Close()
	if _, _, err := m.Branches(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	err = m.CheckoutBranch(context.Background(), "topic", "origin")
	if err == nil || !strings.Contains(err.Error(), "differs from origin/topic") {
		t.Fatalf("checkout error = %v", err)
	}
	head, headErr := repo.Head()
	if headErr != nil || head.Name() != local.Name() || head.Hash() != local.Hash() {
		t.Fatalf("HEAD changed after rejected checkout: %v, %v", head, headErr)
	}
}

func TestCredentialHelperProvidesHTTPAuth(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX script")
	}
	helper := filepath.Join(t.TempDir(), "credential-helper")
	script := "#!/bin/sh\nprintf 'username=system-user\\npassword=system-token\\n\\n'\n"
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	endpoint, err := transport.NewEndpoint("https://example.com/owner/repo.git")
	if err != nil {
		t.Fatal(err)
	}
	credential, err := fillCredential(context.Background(), endpoint, gitCredentialConfig{helpers: []string{helper}, useHTTPPath: true})
	if err != nil {
		t.Fatal(err)
	}
	if credential.username != "system-user" || credential.password != "system-token" {
		t.Fatal("credential helper result was not used")
	}
}

func TestCredentialHelpersSupportQuotedPathsAndAccumulateUsername(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helpers use POSIX scripts")
	}
	dir := filepath.Join(t.TempDir(), "helpers with spaces")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	usernameHelper := filepath.Join(dir, "username helper")
	passwordHelper := filepath.Join(dir, "password helper")
	if err := os.WriteFile(usernameHelper, []byte("#!/bin/sh\nprintf 'username=system-user\\n\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(passwordHelper, []byte("#!/bin/sh\nprintf 'password=system-token\\n\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	endpoint, err := transport.NewEndpoint("https://example.com/owner/repo.git")
	if err != nil {
		t.Fatal(err)
	}
	credential, err := fillCredential(context.Background(), endpoint, gitCredentialConfig{
		helpers: []string{
			strings.ReplaceAll(usernameHelper, " ", `\ `),
			strings.ReplaceAll(passwordHelper, " ", `\ `),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if credential.username != "system-user" || credential.password != "system-token" {
		t.Fatalf("credential = %+v", credential)
	}
}

func TestCredentialHelperFailureDoesNotExposeCommandOrStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX shell")
	}
	const secret = "do-not-expose-this-token"
	endpoint, err := transport.NewEndpoint("https://example.com/owner/repo.git")
	if err != nil {
		t.Fatal(err)
	}
	_, err = fillCredential(context.Background(), endpoint, gitCredentialConfig{
		helpers: []string{"!echo " + secret + " >&2; exit 1"},
	})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("credential error = %v", err)
	}
}

func TestCredentialScopePathUsesDirectoryBoundary(t *testing.T) {
	endpoint, err := transport.NewEndpoint("https://example.com/barry/repo.git")
	if err != nil {
		t.Fatal(err)
	}
	if credentialScopeMatches("https://example.com/bar", endpoint) {
		t.Fatal("credential path prefix matched without a directory boundary")
	}
	if !credentialScopeMatches("https://example.com/barry", endpoint) {
		t.Fatal("credential path prefix did not match at a directory boundary")
	}
}
