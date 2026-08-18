package main

import (
	"bytes"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRemoteBootstrapScriptIsValidShell(t *testing.T) {
	remote := RemoteWorkspace{
		Kind: remoteKindSSH,
		Host: "dev@example.com",
		Path: "/srv/team's project",
	}
	settings := Settings{
		WingmanURL:   "https://gateway.example.com/team's",
		WingmanToken: "secret'value",
		LargeContext: true,
	}
	script := remoteBootstrapScript(remote, settings, 43123, 43124)

	cmd := exec.Command("sh", "-n")
	cmd.Stdin = strings.NewReader(script)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bootstrap script is invalid: %v\n%s\n%s", err, output, script)
	}

	checks := []string{
		"export WINGMAN_URL='https://gateway.example.com/team'\"'\"'s'",
		"export WINGMAN_TOKEN='secret'\"'\"'value'",
		"export WINGMAN_LARGE_CONTEXT=1",
		`managed_root="$HOME/.wingman/bin"`,
		`wingman_bin="$managed_root/wingman"`,
		"--port 43123",
		"--preview-port 43124",
		"--cd '/srv/team'\"'\"'s project'",
		"cd '/srv/team'\"'\"'s project'",
		"server --port 43123 --no-browser",
	}
	for _, want := range checks {
		if !strings.Contains(script, want) {
			t.Errorf("bootstrap script does not contain %q", want)
		}
	}
}

func TestRemoteBootstrapFallsBackToInstalledLegacyServer(t *testing.T) {
	binDir := t.TempDir()
	argumentsFile := filepath.Join(t.TempDir(), "arguments")
	workingDirFile := filepath.Join(t.TempDir(), "working-directory")
	fakeWingman := filepath.Join(binDir, "wingman")
	fakeScript := `#!/bin/sh
if [ "$1" = server ] && [ "${2:-}" = --help ]; then
  echo "Usage: wingman server [--port N]"
  exit 0
fi
printf '%s\n' "$*" > "$FAKE_WINGMAN_ARGUMENTS"
pwd > "$FAKE_WINGMAN_WORKING_DIR"
`
	if err := os.WriteFile(fakeWingman, []byte(fakeScript), 0o755); err != nil {
		t.Fatal(err)
	}

	workspace := t.TempDir()
	remoteHome := t.TempDir()
	script := remoteBootstrapScript(RemoteWorkspace{Path: workspace}, Settings{}, 43123, 43124)
	cmd := exec.Command("sh", "-s")
	cmd.Stdin = strings.NewReader(script)
	cmd.Env = append(os.Environ(),
		"HOME="+remoteHome,
		"PATH="+binDir+":"+os.Getenv("PATH"),
		"FAKE_WINGMAN_ARGUMENTS="+argumentsFile,
		"FAKE_WINGMAN_WORKING_DIR="+workingDirFile,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("legacy bootstrap failed: %v\n%s", err, output)
	}

	arguments, err := os.ReadFile(argumentsFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(arguments)) != "server --port 43123 --no-browser" {
		t.Fatalf("legacy arguments = %q", arguments)
	}
	workingDir, err := os.ReadFile(workingDirFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(workingDir)) != workspace {
		t.Fatalf("legacy working directory = %q, want %q", workingDir, workspace)
	}
	if _, err := os.Stat(filepath.Join(remoteHome, ".wingman", "bin", "wingman")); err != nil {
		t.Fatalf("managed remote executable was not created: %v", err)
	}
}

func TestValidateRemote(t *testing.T) {
	valid := RemoteWorkspace{Host: "production", Path: "/srv/project"}.normalized()
	if err := validateRemote(valid); err != nil {
		t.Fatalf("valid remote rejected: %v", err)
	}
	withUser := RemoteWorkspace{Host: "root@llm.dihei.io", Path: "/srv/project"}.normalized()
	if err := validateRemote(withUser); err != nil {
		t.Fatalf("remote with SSH username rejected: %v", err)
	}

	invalid := []RemoteWorkspace{
		{Kind: remoteKindSSH, Path: "/srv/project"},
		{Kind: remoteKindSSH, Host: "-oProxyCommand=bad", Path: "/srv/project"},
		{Kind: remoteKindSSH, Host: "bad host", Path: "/srv/project"},
		{Kind: remoteKindSSH, Host: "production"},
		{Kind: remoteKindSSH, Host: "production", Path: "relative/project"},
		{Kind: "docker", Host: "production", Path: "/srv/project"},
	}
	for _, remote := range invalid {
		if err := validateRemote(remote); err == nil {
			t.Errorf("invalid remote accepted: %+v", remote)
		}
	}
}

func TestRemotePasswordRejectsLineBreaksBeforeConnecting(t *testing.T) {
	remote := RemoteWorkspace{Host: "production", Path: "/srv/project"}
	_, err := startRemoteWorkspace(remote, Settings{}, remoteCredentials{Password: "bad\npassword"})
	if err == nil || !strings.Contains(err.Error(), "line break") {
		t.Fatalf("invalid password error = %v", err)
	}
}

func TestSettingsRemoteRecencyAndRemoval(t *testing.T) {
	settings := Settings{}
	first := RemoteWorkspace{Host: "one", Path: "/srv/project"}
	second := RemoteWorkspace{Host: "two", Path: "/srv/project"}
	settings.AddRemote(first)
	settings.AddRemote(second)
	settings.AddRemote(RemoteWorkspace{Host: "one", Path: "/srv/project", Name: "Renamed"})

	if len(settings.Remotes) != 2 || settings.Remotes[0].Name != "Renamed" {
		t.Fatalf("unexpected remote recency: %+v", settings.Remotes)
	}
	settings.RemoveRemote(second.key())
	if len(settings.Remotes) != 1 || settings.Remotes[0].Host != "one" {
		t.Fatalf("unexpected remotes after removal: %+v", settings.Remotes)
	}
}

func TestBoundedLogKeepsTail(t *testing.T) {
	log := &boundedLog{limit: 5}
	_, _ = log.Write([]byte("1234"))
	_, _ = log.Write([]byte("5678"))
	if got := log.String(); got != "45678" {
		t.Fatalf("bounded log = %q, want %q", got, "45678")
	}
}

func TestFormatRemoteLogHidesReadinessProbeNoise(t *testing.T) {
	output := "bootstrap failed\nchannel 3: open failed: connect failed: Connection refused\n"
	formatted := formatRemoteLog(output)
	if !strings.Contains(formatted, "bootstrap failed") {
		t.Fatalf("startup error was lost: %q", formatted)
	}
	if strings.Contains(formatted, "Connection refused") {
		t.Fatalf("probe noise was retained: %q", formatted)
	}
}

func TestForwardedGatewaySettings(t *testing.T) {
	settings := Settings{WingmanURL: "https://localhost:4242/gateway", WingmanToken: "token"}
	remote, forward, err := forwardedGatewaySettings(settings, 4242)
	if err != nil {
		t.Fatal(err)
	}
	if forward == "" || !strings.HasSuffix(forward, ":localhost:4242") {
		t.Fatalf("forward = %q", forward)
	}

	parsed, err := url.Parse(remote.WingmanURL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port == 4242 || parsed.Scheme != "https" || parsed.Path != "/gateway" {
		t.Fatalf("rewritten gateway = %q", remote.WingmanURL)
	}
	if remote.WingmanToken != settings.WingmanToken {
		t.Fatal("gateway token changed")
	}

	external := Settings{WingmanURL: "https://gateway.example.com"}
	unchanged, externalForward, err := forwardedGatewaySettings(external)
	if err != nil || externalForward != "" || unchanged.WingmanURL != external.WingmanURL {
		t.Fatalf("external gateway changed: %+v %q %v", unchanged, externalForward, err)
	}
}

func TestSSHAskpassCredentialIsTemporary(t *testing.T) {
	credential, err := newSSHAskpassCredential("one-time-secret")
	if err != nil {
		t.Fatal(err)
	}
	secretFile := credential.file
	t.Cleanup(credential.Close)

	info, err := os.Stat(secretFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential mode = %o, want 600", info.Mode().Perm())
	}

	var output bytes.Buffer
	if err := writeSSHAskpass(&output, secretFile); err != nil {
		t.Fatal(err)
	}
	if output.String() != "one-time-secret\n" {
		t.Fatalf("askpass output = %q", output.String())
	}

	cmd := exec.Command("ssh")
	if err := credential.Apply(cmd); err != nil {
		t.Fatal(err)
	}
	environment := strings.Join(cmd.Env, "\n")
	if !strings.Contains(environment, "SSH_ASKPASS_REQUIRE=force") ||
		!strings.Contains(environment, sshAskpassSecretEnv+"="+secretFile) ||
		strings.Contains(environment, "one-time-secret") {
		t.Fatal("unexpected askpass environment")
	}

	credential.Close()
	if _, err := os.Stat(secretFile); !os.IsNotExist(err) {
		t.Fatalf("credential file still exists: %v", err)
	}
}
