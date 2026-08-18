package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	remoteStartTimeout  = 90 * time.Second
	remoteLogLimit      = 64 << 10
	remoteErrorLogLimit = 4 << 10
)

const sshAskpassSecretEnv = "WINGMAN_SSH_ASKPASS_SECRET_FILE"

type remoteCredentials struct {
	Password string
}

type workspaceServer interface {
	http.Handler
	Close()
}

type remoteServer struct {
	proxy http.Handler

	cancel context.CancelFunc
	cmd    *exec.Cmd
	done   <-chan error
	close  sync.Once
}

func (s *remoteServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.proxy.ServeHTTP(w, r)
}

func (s *remoteServer) Close() {
	s.close.Do(func() {
		s.cancel()

		select {
		case <-s.done:
		case <-time.After(3 * time.Second):
			if s.cmd.Process != nil {
				_ = s.cmd.Process.Kill()
			}
			select {
			case <-s.done:
			case <-time.After(time.Second):
			}
		}
	})
}

func startRemoteWorkspace(remote RemoteWorkspace, settings Settings, credentials remoteCredentials) (workspaceServer, error) {
	remote = remote.normalized()
	if err := validateRemote(remote); err != nil {
		return nil, err
	}
	if strings.ContainsAny(credentials.Password, "\x00\r\n") {
		return nil, errors.New("SSH password cannot contain a line break")
	}

	switch remote.Kind {
	case remoteKindSSH:
		return startSSHWorkspace(remote, settings, credentials)
	default:
		return nil, fmt.Errorf("unsupported remote kind %q", remote.Kind)
	}
}

func validateRemote(remote RemoteWorkspace) error {
	if remote.Kind != remoteKindSSH {
		return fmt.Errorf("unsupported remote kind %q", remote.Kind)
	}
	if remote.Host == "" {
		return errors.New("SSH host is required")
	}
	if strings.HasPrefix(remote.Host, "-") || strings.ContainsAny(remote.Host, "\x00\r\n\t ") {
		return errors.New("invalid SSH host")
	}
	if remote.Path == "" {
		return errors.New("workspace path is required")
	}
	if !path.IsAbs(remote.Path) {
		return errors.New("workspace path must be absolute")
	}
	if strings.ContainsAny(remote.Path, "\x00\r\n") {
		return errors.New("invalid workspace path")
	}
	return nil
}

func startSSHWorkspace(remote RemoteWorkspace, settings Settings, credentials remoteCredentials) (*remoteServer, error) {
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return nil, errors.New("ssh was not found in PATH")
	}

	port, err := availableLoopbackPort()
	if err != nil {
		return nil, fmt.Errorf("select local port: %w", err)
	}
	previewPort, err := availableLoopbackPort()
	if err != nil {
		return nil, fmt.Errorf("select local preview port: %w", err)
	}
	for previewPort == port {
		previewPort, err = availableLoopbackPort()
		if err != nil {
			return nil, fmt.Errorf("select local preview port: %w", err)
		}
	}
	remoteSettings, gatewayForward, err := forwardedGatewaySettings(settings, port, previewPort)
	if err != nil {
		return nil, err
	}

	runtimeCtx, cancel := context.WithCancel(context.Background())
	forward := fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", port, port)
	previewForward := fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", previewPort, previewPort)
	batchMode := "yes"
	if credentials.Password != "" {
		batchMode = "no"
	}
	sshArgs := []string{
		"-T",
		"-o", "BatchMode=" + batchMode,
		"-o", "ConnectTimeout=10",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "NumberOfPasswordPrompts=1",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-L", forward,
		"-L", previewForward,
	}
	if gatewayForward != "" {
		sshArgs = append(sshArgs, "-R", gatewayForward)
	}
	sshArgs = append(sshArgs,
		"--", remote.Host,
		"sh", "-s",
	)
	cmd := exec.CommandContext(runtimeCtx, sshPath, sshArgs...)
	askpass, err := newSSHAskpassCredential(credentials.Password)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("prepare SSH password prompt: %w", err)
	}
	defer askpass.Close()
	if err := askpass.Apply(cmd); err != nil {
		cancel()
		return nil, fmt.Errorf("prepare SSH password prompt: %w", err)
	}

	logs := &boundedLog{limit: remoteLogLimit}
	cmd.Stdin = strings.NewReader(remoteBootstrapScript(remote, remoteSettings, port, previewPort))
	cmd.Stdout = logs
	cmd.Stderr = logs

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start ssh: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	if err := waitForRemote(target, done, logs); err != nil {
		cancel()
		if cmd.ProcessState == nil {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
			}
		}
		return nil, err
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		http.Error(w, "remote workspace unavailable: "+err.Error(), http.StatusBadGateway)
	}

	return &remoteServer{
		proxy:  proxy,
		cancel: cancel,
		cmd:    cmd,
		done:   done,
	}, nil
}

// sshAskpassCredential lets OpenSSH authenticate while stdin remains reserved
// for the remote bootstrap script. The password is kept in a private temporary
// file, referenced only through the SSH process environment, and deleted as
// soon as the connection either becomes ready or fails.
type sshAskpassCredential struct {
	dir  string
	file string
}

func newSSHAskpassCredential(password string) (*sshAskpassCredential, error) {
	credential := &sshAskpassCredential{}
	if password == "" {
		return credential, nil
	}

	dir, err := os.MkdirTemp("", "wingman-ssh-askpass-")
	if err != nil {
		return nil, err
	}
	credential.dir = dir
	credential.file = filepath.Join(dir, "secret")
	if err := os.WriteFile(credential.file, []byte(password), 0o600); err != nil {
		credential.Close()
		return nil, err
	}
	return credential, nil
}

func (c *sshAskpassCredential) Apply(cmd *exec.Cmd) error {
	if c == nil || c.file == "" {
		return nil
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	cmd.Env = append(os.Environ(),
		"SSH_ASKPASS="+executable,
		"SSH_ASKPASS_REQUIRE=force",
		"DISPLAY=wingman:0",
		sshAskpassSecretEnv+"="+c.file,
	)
	return nil
}

func (c *sshAskpassCredential) Close() {
	if c == nil || c.dir == "" {
		return
	}
	_ = os.RemoveAll(c.dir)
	c.dir = ""
	c.file = ""
}

func runSSHAskpass() bool {
	secretFile := os.Getenv(sshAskpassSecretEnv)
	if secretFile == "" {
		return false
	}
	_ = writeSSHAskpass(os.Stdout, secretFile)
	return true
}

func writeSSHAskpass(w io.Writer, secretFile string) error {
	secret, err := os.ReadFile(secretFile)
	if err != nil {
		return err
	}
	if _, err := w.Write(secret); err != nil {
		return err
	}
	_, err = io.WriteString(w, "\n")
	return err
}

func forwardedGatewaySettings(settings Settings, unavailablePorts ...int) (Settings, string, error) {
	rawURL := strings.TrimSpace(settings.WingmanURL)
	if rawURL == "" {
		return settings, "", nil
	}

	gateway, err := url.Parse(rawURL)
	if err != nil || gateway.Hostname() == "" {
		return settings, "", nil
	}
	host := gateway.Hostname()
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return settings, "", nil
	}

	localPort := gateway.Port()
	if localPort == "" {
		switch strings.ToLower(gateway.Scheme) {
		case "http":
			localPort = "80"
		case "https":
			localPort = "443"
		default:
			return settings, "", nil
		}
	}
	remotePort, err := availableLoopbackPort()
	if err != nil {
		return settings, "", fmt.Errorf("select remote gateway port: %w", err)
	}
	for containsPort(unavailablePorts, remotePort) {
		remotePort, err = availableLoopbackPort()
		if err != nil {
			return settings, "", fmt.Errorf("select remote gateway port: %w", err)
		}
	}

	destinationHost := host
	if strings.Contains(destinationHost, ":") {
		destinationHost = "[" + destinationHost + "]"
	}
	forward := fmt.Sprintf("127.0.0.1:%d:%s:%s", remotePort, destinationHost, localPort)
	gateway.Host = net.JoinHostPort(host, fmt.Sprint(remotePort))
	settings.WingmanURL = gateway.String()
	return settings, forward, nil
}

func containsPort(ports []int, candidate int) bool {
	for _, port := range ports {
		if port == candidate {
			return true
		}
	}
	return false
}

func availableLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func waitForRemote(target *url.URL, done <-chan error, logs *boundedLog) error {
	ctx, cancel := context.WithTimeout(context.Background(), remoteStartTimeout)
	defer cancel()

	client := &http.Client{
		Timeout: 750 * time.Millisecond,
		Transport: &http.Transport{
			Proxy: nil,
		},
	}
	health := target.ResolveReference(&url.URL{Path: "/healthz"}).String()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, health, nil)
		response, err := client.Do(request)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}

		select {
		case err := <-done:
			return remoteProcessError(err, logs.String())
		case <-ctx.Done():
			return fmt.Errorf("remote server did not become ready: %w%s", ctx.Err(), formatRemoteLog(logs.String()))
		case <-ticker.C:
		}
	}
}

func remoteProcessError(err error, output string) error {
	if err == nil {
		return errors.New("remote SSH session ended before the server became ready" + formatRemoteLog(output))
	}
	return fmt.Errorf("remote SSH session ended before the server became ready: %w%s", err, formatRemoteLog(output))
}

func formatRemoteLog(output string) string {
	lines := strings.Split(output, "\n")
	lines = slices.DeleteFunc(lines, func(line string) bool {
		return strings.Contains(line, "open failed: connect failed: Connection refused")
	})
	output = strings.Join(lines, "\n")
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	if len(output) > remoteErrorLogLimit {
		output = "…\n" + output[len(output)-remoteErrorLogLimit:]
	}
	return "\n\n" + output
}

func remoteBootstrapScript(remote RemoteWorkspace, settings Settings, port, previewPort int) string {
	var script strings.Builder
	script.WriteString("set -eu\n")

	if settings.WingmanURL != "" {
		fmt.Fprintf(&script, "export WINGMAN_URL=%s\n", shellQuote(settings.WingmanURL))
	}
	if settings.WingmanToken != "" {
		fmt.Fprintf(&script, "export WINGMAN_TOKEN=%s\n", shellQuote(settings.WingmanToken))
	}
	if settings.LargeContext {
		script.WriteString("export WINGMAN_LARGE_CONTEXT=1\n")
	}

	script.WriteString(`supports_remote_server() {
  case "$("$1" server --help 2>&1)" in
    *--preview-port*) return 0 ;;
    *) return 1 ;;
  esac
}

managed_root="$HOME/.wingman/bin"
wingman_bin="$managed_root/wingman"
installed_wingman="$(command -v wingman 2>/dev/null || true)"

copy_installed=0
if [ -n "$installed_wingman" ] && [ "$installed_wingman" != "$wingman_bin" ]; then
  if [ ! -x "$wingman_bin" ] || { ! supports_remote_server "$wingman_bin" && supports_remote_server "$installed_wingman"; }; then
    copy_installed=1
  fi
fi
if [ "$copy_installed" = 1 ]; then
  mkdir -p "$managed_root"
  staged_bin="$wingman_bin.tmp.$$"
  cp "$installed_wingman" "$staged_bin"
  chmod 0755 "$staged_bin"
  mv "$staged_bin" "$wingman_bin"
fi

if [ ! -x "$wingman_bin" ]; then
    case "$(uname -s)" in
      Linux) asset_os=linux ;;
      Darwin) asset_os=darwin ;;
      *) echo "Wingman remote supports Linux and macOS hosts" >&2; exit 1 ;;
    esac
    case "$(uname -m)" in
      x86_64|amd64) asset_arch=amd64 ;;
      arm64|aarch64) asset_arch=arm64 ;;
      *) echo "Unsupported remote architecture: $(uname -m)" >&2; exit 1 ;;
    esac

    release_api="https://api.github.com/repos/adrianliechti/wingman-agent/releases/latest"
    asset_suffix="_${asset_os}_${asset_arch}.tar.gz"
    temp_dir="$(mktemp -d)"
    trap 'rm -rf "$temp_dir"' EXIT HUP INT TERM

    if command -v curl >/dev/null 2>&1; then
      downloader=curl
      release_json="$(curl -fsSL --retry 2 "$release_api")"
    elif command -v wget >/dev/null 2>&1; then
      downloader=wget
      release_json="$(wget -q -O - "$release_api")"
    else
      echo "Wingman is not installed and neither curl nor wget is available" >&2
      exit 1
    fi

    asset_url="$(printf '%s' "$release_json" | tr ',' '\n' | sed -n 's/^[[:space:]]*"browser_download_url":[[:space:]]*"\([^"]*\)".*/\1/p' | grep "${asset_suffix}$" | head -n 1)"
    if [ -z "$asset_url" ]; then
      echo "The latest Wingman release has no ${asset_os}/${asset_arch} executable" >&2
      exit 1
    fi

    if [ "$downloader" = curl ]; then
      curl -fsSL --retry 2 -o "$temp_dir/wingman.tar.gz" "$asset_url"
    else
      wget -q -O "$temp_dir/wingman.tar.gz" "$asset_url"
    fi

    tar -xzf "$temp_dir/wingman.tar.gz" -C "$temp_dir"
    if [ ! -f "$temp_dir/wingman" ]; then
      echo "Downloaded Wingman archive did not contain the executable" >&2
      exit 1
    fi
    mkdir -p "$managed_root"
    staged_bin="$wingman_bin.tmp.$$"
    cp "$temp_dir/wingman" "$staged_bin"
    chmod 0755 "$staged_bin"
    mv "$staged_bin" "$wingman_bin"
    rm -rf "$temp_dir"
    trap - EXIT HUP INT TERM
fi
`)

	fmt.Fprintf(&script, `if supports_remote_server "$wingman_bin"; then
  exec "$wingman_bin" server --host 127.0.0.1 --port %d --preview-port %d --no-browser --cd %s
fi

echo "Wingman remote: using legacy server mode; isolated HTML preview is unavailable" >&2
cd %s
exec "$wingman_bin" server --port %d --no-browser
`,
		port,
		previewPort,
		shellQuote(remote.Path),
		shellQuote(remote.Path),
		port,
	)
	return script.String()
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

type boundedLog struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func (b *boundedLog) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.data = append(b.data, p...)
	if len(b.data) > b.limit {
		b.data = append([]byte(nil), b.data[len(b.data)-b.limit:]...)
	}
	return len(p), nil
}

func (b *boundedLog) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(bytes.Clone(b.data))
}
