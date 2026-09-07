package clipboard

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

type Content struct {
	Text  string
	Image *string
}

func Read() ([]Content, error) {
	var fallback func() ([]Content, error)
	if isWSL() && !envSet("SSH_TTY") && !envSet("SSH_CONNECTION") {
		fallback = readWindowsClipboard
	}
	return readWithFallback(readText, readImage, fallback)
}

func readWithFallback(textReader, imageReader func() (string, error), fallback func() ([]Content, error)) ([]Content, error) {
	text, textErr := textReader()
	image, imageErr := imageReader()
	contents, err := readContents(text, textErr, image, imageErr)
	if len(contents) != 0 || fallback == nil {
		return contents, err
	}
	// WSL can have Linux clipboard tools installed without a clipboard owner.
	// Try the Windows clipboard on either an unavailable or an empty Linux read.
	contents, fallbackErr := fallback()
	if fallbackErr != nil {
		return nil, errors.Join(err, fmt.Errorf("Windows clipboard: %w", fallbackErr))
	}
	return contents, nil
}

func readContents(text string, textErr error, image string, imageErr error) ([]Content, error) {
	var contents []Content
	if textErr == nil && text != "" {
		contents = append(contents, Content{Text: text})
	}
	if imageErr == nil && image != "" {
		contents = append(contents, Content{Image: &image})
	}
	if len(contents) == 0 && textErr != nil && imageErr != nil {
		return nil, fmt.Errorf("clipboard text: %v; clipboard image: %w", textErr, imageErr)
	}
	return contents, nil
}

const osc52MaxRawBytes = 100_000

type copyEnvironment struct {
	ssh  bool
	wsl  bool
	tmux bool
}

type copyBackends struct {
	native func(string) error
	wsl    func(string) error
	tmux   func(string) error
	osc52  func(string) error
}

type copyAttempt struct {
	name  string
	write func(string) error
}

// WriteText copies text to the clipboard appropriate for the current session.
// Remote sessions intentionally avoid the remote machine's native clipboard.
func WriteText(text string) error {
	environment := detectCopyEnvironment()
	return writeTextWith(text, environment, copyBackends{
		native: writeNativeText,
		wsl:    writeWSLText,
		tmux:   writeTMUXText,
		osc52:  func(text string) error { return writeOSC52Text(text, environment.tmux) },
	})
}

func writeTextWith(text string, environment copyEnvironment, backends copyBackends) error {
	var attempts []copyAttempt
	if environment.ssh {
		if environment.tmux {
			attempts = append(attempts, copyAttempt{"tmux clipboard", backends.tmux})
		}
		attempts = append(attempts, copyAttempt{"OSC 52 fallback", backends.osc52})
	} else {
		attempts = append(attempts, copyAttempt{"native clipboard", backends.native})
		if environment.wsl {
			attempts = append(attempts, copyAttempt{"WSL fallback", backends.wsl})
		}
		if environment.tmux {
			attempts = append(attempts, copyAttempt{"tmux clipboard", backends.tmux})
		}
		attempts = append(attempts, copyAttempt{"OSC 52 fallback", backends.osc52})
	}

	var failures []string
	for _, attempt := range attempts {
		if err := attempt.write(text); err == nil {
			return nil
		} else {
			failures = append(failures, fmt.Sprintf("%s: %v", attempt.name, err))
		}
	}
	err := errors.New(strings.Join(failures, "; "))
	if environment.ssh {
		return fmt.Errorf("terminal clipboard copy over SSH: %w", err)
	}
	return err
}

func detectCopyEnvironment() copyEnvironment {
	return copyEnvironment{
		ssh:  envSet("SSH_TTY") || envSet("SSH_CONNECTION"),
		wsl:  isWSL(),
		tmux: envSet("TMUX") || envSet("TMUX_PANE"),
	}
}

func envSet(name string) bool {
	_, ok := os.LookupEnv(name)
	return ok
}

func isWSL() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if envSet("WSL_DISTRO_NAME") || envSet("WSL_INTEROP") {
		return true
	}
	version, err := os.ReadFile("/proc/version")
	versionText := strings.ToLower(string(version))
	return err == nil && (strings.Contains(versionText, "microsoft") || strings.Contains(versionText, "wsl"))
}

func writeWSLText(text string) error {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NoLogo", "-NonInteractive", "-Command", `[Console]::InputEncoding = [System.Text.Encoding]::UTF8; $ErrorActionPreference = 'Stop'; Set-Clipboard -Value ([Console]::In.ReadToEnd())`)
	cmd.Stdin = strings.NewReader(text)
	if output, err := cmd.CombinedOutput(); err != nil {
		return commandError("powershell.exe", err, output)
	}
	return nil
}

func writeTMUXText(text string) error {
	setClipboard, err := tmuxCommandOutput("show-options", "-gv", "set-clipboard")
	if err != nil {
		return err
	}
	if strings.TrimSpace(setClipboard) == "off" {
		return fmt.Errorf("tmux clipboard forwarding is disabled")
	}
	info, err := tmuxCommandOutput("info")
	if err != nil {
		return err
	}
	if strings.Contains(info, "Ms: [missing]") {
		return fmt.Errorf("tmux clipboard forwarding is unavailable: missing Ms capability")
	}

	cmd := exec.Command("tmux", "load-buffer", "-w", "-")
	cmd.Stdin = strings.NewReader(text)
	if output, err := cmd.CombinedOutput(); err != nil {
		return commandError("tmux", err, output)
	}
	return nil
}

func tmuxCommandOutput(args ...string) (string, error) {
	cmd := exec.Command("tmux", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", commandError("tmux", err, output)
	}
	return string(output), nil
}

func writeOSC52Text(text string, tmux bool) error {
	sequence, err := osc52Sequence(text, tmux)
	if err != nil {
		return err
	}

	if runtime.GOOS != "windows" {
		if tty, openErr := os.OpenFile("/dev/tty", os.O_WRONLY, 0); openErr == nil {
			_, writeErr := tty.WriteString(sequence)
			closeErr := tty.Close()
			if writeErr == nil {
				return closeErr
			}
		}
	}

	_, err = os.Stdout.WriteString(sequence)
	return err
}

func osc52Sequence(text string, tmux bool) (string, error) {
	if len(text) > osc52MaxRawBytes {
		return "", fmt.Errorf("OSC 52 payload too large (%d bytes; max %d)", len(text), osc52MaxRawBytes)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	sequence := "\x1b]52;c;" + encoded + "\x07"
	if tmux {
		sequence = "\x1bPtmux;\x1b" + sequence + "\x1b\\"
	}
	return sequence, nil
}

func commandError(name string, err error, output []byte) error {
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return fmt.Errorf("%s: %w", name, err)
	}
	return fmt.Errorf("%s: %w: %s", name, err, detail)
}
