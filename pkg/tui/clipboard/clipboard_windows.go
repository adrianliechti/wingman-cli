//go:build windows

package clipboard

import (
	"encoding/base64"
	"os/exec"
	"strings"
)

const powerShellEncodingPrefix = "[Console]::InputEncoding = [System.Text.Encoding]::UTF8; [Console]::OutputEncoding = [System.Text.Encoding]::UTF8; "

func buildPowerShellArgs(script string, sta bool) []string {
	args := []string{"-NoProfile", "-NoLogo", "-NonInteractive"}

	if sta {
		args = append(args, "-Sta")
	}

	args = append(args, "-Command", powerShellEncodingPrefix+script)

	return args
}

func runPowerShell(script string, sta bool) ([]byte, error) {
	return exec.Command("powershell.exe", buildPowerShellArgs(script, sta)...).Output()
}

func buildWriteTextCommand(text string) *exec.Cmd {
	cmd := exec.Command("powershell.exe", buildPowerShellArgs(`Set-Clipboard -Value ([Console]::In.ReadToEnd())`, false)...)
	cmd.Stdin = strings.NewReader(text)

	return cmd
}

// Read reads text and image content from the Windows clipboard.
func Read() ([]Content, error) {
	var contents []Content

	if text, err := readText(); err == nil && text != "" {
		contents = append(contents, Content{Text: text})
	}

	if imageDataURL, err := readImage(); err == nil && imageDataURL != "" {
		contents = append(contents, Content{Image: &imageDataURL})
	}

	return contents, nil
}

func readText() (string, error) {
	output, err := runPowerShell(`$text = Get-Clipboard -Format Text -Raw -ErrorAction SilentlyContinue; if ($null -ne $text) { [Console]::Out.Write($text) }`, false)

	if err != nil {
		return "", err
	}

	return string(output), nil
}

func readImage() (string, error) {
	// Use the same approach as opencode-ai (TypeScript version)
	// Load System.Windows.Forms assembly and use in-memory stream
	script := `Add-Type -AssemblyName System.Windows.Forms; $img = [System.Windows.Forms.Clipboard]::GetImage(); if ($img) { $ms = New-Object System.IO.MemoryStream; $img.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png); [System.Convert]::ToBase64String($ms.ToArray()) }`

	output, err := runPowerShell(script, true)

	if err != nil {
		return "", err
	}

	data := strings.TrimSpace(string(output))

	if data == "" {
		return "", nil
	}

	// Verify it's valid base64 and has data
	if _, err := base64.StdEncoding.DecodeString(data); err != nil {
		return "", err
	}

	return "data:image/png;base64," + data, nil
}

// WriteText writes text to the Windows clipboard.
func WriteText(text string) error {
	cmd := buildWriteTextCommand(text)

	return cmd.Run()
}
