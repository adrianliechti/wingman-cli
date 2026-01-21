//go:build windows

package clipboard

import (
	"encoding/base64"
	"os/exec"
	"strings"
)

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
	output, err := exec.Command("powershell", "-NoProfile", "-Command", "Get-Clipboard").Output()

	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(output)), nil
}

func readImage() (string, error) {
	// Use the same approach as opencode-ai (TypeScript version)
	// Load System.Windows.Forms assembly and use in-memory stream
	script := `Add-Type -AssemblyName System.Windows.Forms; $img = [System.Windows.Forms.Clipboard]::GetImage(); if ($img) { $ms = New-Object System.IO.MemoryStream; $img.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png); [System.Convert]::ToBase64String($ms.ToArray()) }`

	output, err := exec.Command("powershell.exe", "-NonInteractive", "-NoProfile", "-Command", script).Output()

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
	cmd := exec.Command("powershell", "-NoProfile", "-Command", "Set-Clipboard", "-Value", text)
	return cmd.Run()
}
