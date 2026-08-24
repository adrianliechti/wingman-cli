package changes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"

	"github.com/adrianliechti/wingman-agent/internal/process"
)

// authForRemote bridges go-git with the credential helpers already configured
// for the user's Git installation. Repository traffic still goes through
// go-git; only credential lookup is delegated to the configured helper.
func (m *Manager) authForRemote(ctx context.Context, remoteName string) (transport.AuthMethod, error) {
	cfg, err := m.repo.ConfigScoped(config.SystemScope)
	if err != nil {
		return nil, fmt.Errorf("read Git configuration: %w", err)
	}
	remote := cfg.Remotes[remoteName]
	if remote == nil || len(remote.URLs) == 0 {
		return nil, fmt.Errorf("remote %q has no URL", remoteName)
	}
	endpoint, err := transport.NewEndpoint(remote.URLs[0])
	if err != nil {
		return nil, fmt.Errorf("parse %s remote URL: %w", remoteName, err)
	}
	if endpoint.Protocol != "http" && endpoint.Protocol != "https" {
		// SSH transport uses go-git's default SSH-agent authentication and
		// known_hosts verification when Auth is nil.
		return nil, nil
	}
	if endpoint.Password != "" {
		username := endpoint.User
		if username == "" {
			username = "git"
		}
		return &githttp.BasicAuth{Username: username, Password: endpoint.Password}, nil
	}

	helperCfg := credentialConfigForEndpoint(cfg, endpoint)
	if len(helperCfg.helpers) == 0 {
		helperCfg.helpers = installedSystemCredentialHelpers()
	}
	if len(helperCfg.helpers) == 0 {
		return nil, nil
	}
	credential, err := fillCredential(ctx, endpoint, helperCfg)
	if err != nil {
		return nil, err
	}
	if credential.password == "" {
		return nil, nil
	}
	username := credential.username
	if username == "" {
		username = endpoint.User
	}
	if username == "" {
		username = "git"
	}
	return &githttp.BasicAuth{Username: username, Password: credential.password}, nil
}

type gitCredentialConfig struct {
	helpers     []string
	username    string
	useHTTPPath bool
}

type gitCredential struct {
	username string
	password string
}

func credentialConfigForEndpoint(cfg *config.Config, endpoint *transport.Endpoint) gitCredentialConfig {
	result := gitCredentialConfig{}
	if cfg.Raw == nil {
		return result
	}
	section := cfg.Raw.Section("credential")
	applyCredentialOptions(&result, section.Options.GetAll("helper"), section.Options.Get("username"), section.Options.Get("useHttpPath"))
	for _, subsection := range section.Subsections {
		if credentialScopeMatches(subsection.Name, endpoint) {
			applyCredentialOptions(&result, subsection.Options.GetAll("helper"), subsection.Options.Get("username"), subsection.Options.Get("useHttpPath"))
		}
	}
	return result
}

func applyCredentialOptions(result *gitCredentialConfig, helpers []string, username, useHTTPPath string) {
	for _, helper := range helpers {
		if helper == "" {
			result.helpers = nil
			continue
		}
		result.helpers = append(result.helpers, helper)
	}
	if username != "" {
		result.username = username
	}
	if value, err := strconv.ParseBool(useHTTPPath); err == nil {
		result.useHTTPPath = value
	}
}

func credentialScopeMatches(scope string, endpoint *transport.Endpoint) bool {
	wanted, err := transport.NewEndpoint(scope)
	if err != nil || wanted.Protocol != endpoint.Protocol || !strings.EqualFold(wanted.Host, endpoint.Host) {
		return false
	}
	if wanted.User != "" && wanted.User != endpoint.User {
		return false
	}
	if wanted.Port != 0 && wanted.Port != endpoint.Port {
		return false
	}
	if wanted.Path != "" {
		wantedPath := strings.Trim(strings.TrimPrefix(wanted.Path, "/"), "/")
		endpointPath := strings.Trim(strings.TrimPrefix(endpoint.Path, "/"), "/")
		if endpointPath != wantedPath && !strings.HasPrefix(endpointPath, wantedPath+"/") {
			return false
		}
	}
	return true
}

func fillCredential(ctx context.Context, endpoint *transport.Endpoint, cfg gitCredentialConfig) (gitCredential, error) {
	if len(cfg.helpers) == 0 {
		return gitCredential{}, nil
	}
	credential := gitCredential{username: cfg.username}
	var helperErrors []error
	for _, helper := range cfg.helpers {
		inputCfg := cfg
		inputCfg.username = credential.username
		input := credentialInput(endpoint, inputCfg)
		output, err := runCredentialHelper(ctx, helper, input)
		if err != nil {
			helperErrors = append(helperErrors, err)
			continue
		}
		result, quit := parseCredential(output)
		if result.username != "" {
			credential.username = result.username
		}
		if result.password != "" {
			credential.password = result.password
		}
		if credential.username != "" && credential.password != "" {
			return credential, nil
		}
		if quit {
			break
		}
	}
	if len(helperErrors) == len(cfg.helpers) {
		return gitCredential{}, fmt.Errorf("Git credential lookup failed: %w", errors.Join(helperErrors...))
	}
	return credential, nil
}

func installedSystemCredentialHelpers() []string {
	candidates := []string{}
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{"osxkeychain"}
	case "windows":
		candidates = []string{"manager-core", "manager"}
	case "linux":
		candidates = []string{"libsecret"}
	}
	for _, helper := range candidates {
		if path, err := findCredentialHelper("git-credential-" + helper); err == nil {
			return []string{"!" + shellQuote(path)}
		}
	}
	return nil
}

func credentialInput(endpoint *transport.Endpoint, cfg gitCredentialConfig) string {
	var input strings.Builder
	fmt.Fprintf(&input, "protocol=%s\n", endpoint.Protocol)
	host := endpoint.Host
	if endpoint.Port != 0 {
		host += ":" + strconv.Itoa(endpoint.Port)
	}
	fmt.Fprintf(&input, "host=%s\n", host)
	username := endpoint.User
	if username == "" {
		username = cfg.username
	}
	if username != "" {
		fmt.Fprintf(&input, "username=%s\n", username)
	}
	if cfg.useHTTPPath && endpoint.Path != "" {
		fmt.Fprintf(&input, "path=%s\n", strings.TrimPrefix(endpoint.Path, "/"))
	}
	input.WriteByte('\n')
	return input.String()
}

func parseCredential(output []byte) (gitCredential, bool) {
	credential := gitCredential{}
	quit := false
	for line := range strings.SplitSeq(string(output), "\n") {
		key, value, ok := strings.Cut(strings.TrimSuffix(line, "\r"), "=")
		if !ok {
			continue
		}
		switch key {
		case "username":
			credential.username = value
		case "password":
			credential.password = value
		case "quit":
			quit, _ = strconv.ParseBool(value)
		}
	}
	return credential, quit
}

func runCredentialHelper(ctx context.Context, helper, input string) ([]byte, error) {
	shell, err := findGitShell()
	if err != nil {
		return nil, errors.New("Git credential helpers require a POSIX-compatible shell")
	}
	helper = strings.TrimSpace(helper)
	if helper == "" {
		return nil, errors.New("empty Git credential helper")
	}
	command := helper
	switch {
	case strings.HasPrefix(helper, "!"):
		command = strings.TrimPrefix(helper, "!")
	case !filepath.IsAbs(helper):
		command = "git credential-" + helper
	}
	cmd := exec.CommandContext(ctx, shell, "-c", command+` "$@"`, "git-credential-helper", "get")
	process.Hide(cmd)
	cmd.Stdin = strings.NewReader(input)
	return credentialHelperOutput(cmd)
}

func findGitShell() (string, error) {
	if shell, err := exec.LookPath("sh"); err == nil {
		return shell, nil
	}
	if runtime.GOOS == "windows" {
		if gitPath, err := exec.LookPath("git"); err == nil {
			for _, candidate := range []string{
				filepath.Join(filepath.Dir(gitPath), "..", "bin", "sh.exe"),
				filepath.Join(filepath.Dir(gitPath), "..", "usr", "bin", "sh.exe"),
			} {
				if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
					return candidate, nil
				}
			}
		}
	}
	return "", errors.New("Git shell was not found")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func credentialHelperOutput(cmd *exec.Cmd) ([]byte, error) {
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("Git credential helper failed: %w", err)
	}
	return stdout.Bytes(), nil
}

func findCredentialHelper(program string) (string, error) {
	if strings.ContainsAny(program, `/\\`) {
		if info, err := os.Stat(program); err == nil && !info.IsDir() {
			return program, nil
		}
		return "", fmt.Errorf("Git credential helper %q was not found", program)
	}
	if path, err := exec.LookPath(program); err == nil {
		return path, nil
	}

	dirs := []string{
		"/usr/libexec/git-core",
		"/usr/local/libexec/git-core",
		"/opt/homebrew/libexec/git-core",
		"/Library/Developer/CommandLineTools/usr/libexec/git-core",
		"/Applications/Xcode.app/Contents/Developer/usr/libexec/git-core",
	}
	if gitPath, err := exec.LookPath("git"); err == nil {
		binDir := filepath.Dir(gitPath)
		dirs = append(dirs,
			filepath.Clean(filepath.Join(binDir, "..", "libexec", "git-core")),
			filepath.Clean(filepath.Join(binDir, "..", "mingw64", "libexec", "git-core")),
		)
	}
	for _, dir := range dirs {
		candidate := filepath.Join(dir, program)
		if runtime.GOOS == "windows" {
			candidate += ".exe"
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("Git credential helper %q was not found", program)
}
