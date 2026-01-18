//go:build windows

package clipboard

import (
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
	script := `
$img = Get-Clipboard -Format Image
if ($img -ne $null) {
    $ms = New-Object System.IO.MemoryStream
    $img.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png)
    $bytes = $ms.ToArray()
    $ms.Close()
    [Convert]::ToBase64String($bytes)
}
`
	output, err := exec.Command("powershell", "-NoProfile", "-Command", script).Output()
	if err != nil {
		return "", nil
	}

	encoded := strings.TrimSpace(string(output))
	if encoded == "" {
		return "", nil
	}

	return "data:image/png;base64," + encoded, nil
}
