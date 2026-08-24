//go:build windows

package clipboard

import (
	"encoding/base64"
	"os/exec"
	"strings"

	"github.com/adrianliechti/wingman-agent/internal/process"
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
	cmd := exec.Command("powershell.exe", buildPowerShellArgs(script, sta)...)
	process.Hide(cmd)
	return cmd.Output()
}

func buildWriteTextCommand(text string) *exec.Cmd {
	cmd := exec.Command("powershell.exe", buildPowerShellArgs(`$ErrorActionPreference = 'Stop'; Set-Clipboard -Value ([Console]::In.ReadToEnd())`, false)...)
	process.Hide(cmd)
	cmd.Stdin = strings.NewReader(text)

	return cmd
}

func Read() ([]Content, error) {
	text, textErr := readText()
	imageDataURL, imageErr := readImage()
	return readContents(text, textErr, imageDataURL, imageErr)
}

func readText() (string, error) {
	output, err := runPowerShell(`$text = Get-Clipboard -Format Text -Raw -ErrorAction SilentlyContinue; if ($null -ne $text) { [Console]::Out.Write($text) }`, false)

	if err != nil {
		return "", err
	}

	return string(output), nil
}

func readImage() (string, error) {
	script := `Add-Type -AssemblyName System.Windows.Forms; $img = [System.Windows.Forms.Clipboard]::GetImage(); if ($img) { $ms = New-Object System.IO.MemoryStream; $img.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png); [System.Convert]::ToBase64String($ms.ToArray()) }`

	output, err := runPowerShell(script, true)

	if err != nil {
		return "", err
	}

	data := strings.TrimSpace(string(output))

	if data == "" {
		return "", nil
	}

	if _, err := base64.StdEncoding.DecodeString(data); err != nil {
		return "", err
	}

	return "data:image/png;base64," + data, nil
}

func writeNativeText(text string) error {
	cmd := buildWriteTextCommand(text)

	return cmd.Run()
}
