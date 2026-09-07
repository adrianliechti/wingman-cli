//go:build windows

package clipboard

import (
	"os/exec"
	"strings"

	"github.com/adrianliechti/wingman-agent/internal/process"
)

func buildWriteTextCommand(text string) *exec.Cmd {
	cmd := exec.Command("powershell.exe", buildPowerShellArgs(`$ErrorActionPreference = 'Stop'; Set-Clipboard -Value ([Console]::In.ReadToEnd())`, false)...)
	process.Hide(cmd)
	cmd.Stdin = strings.NewReader(text)

	return cmd
}

func readText() (string, error) {
	return readPowerShellText()
}

func readImage() (string, error) {
	return readPowerShellImage()
}

func writeNativeText(text string) error {
	cmd := buildWriteTextCommand(text)

	return cmd.Run()
}
