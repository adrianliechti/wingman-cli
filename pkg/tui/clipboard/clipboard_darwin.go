//go:build darwin

package clipboard

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func Read() ([]Content, error) {
	text, textErr := readText()
	imageDataURL, imageErr := readImage()
	return readContents(text, textErr, imageDataURL, imageErr)
}

func readText() (string, error) {
	output, err := exec.Command("pbpaste").Output()

	if err != nil {
		return "", err
	}

	return string(output), nil
}

func readImage() (string, error) {
	tmpFile, err := os.CreateTemp("", "clipboard-*.png")

	if err != nil {
		return "", err
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	script := fmt.Sprintf(`
		set tmpFile to POSIX file "%s"
		try
			set imageData to the clipboard as «class PNGf»
			set fileRef to open for access tmpFile with write permission
			write imageData to fileRef
			close access fileRef

			return "ok"
		on error

			return "no image"
		end try
	`, tmpPath)

	output, err := exec.Command("osascript", "-e", script).Output()

	if err != nil {
		return "", err
	}
	if string(output) != "ok\n" {
		return "", nil
	}

	data, err := os.ReadFile(tmpPath)

	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", nil
	}

	encoded := base64.StdEncoding.EncodeToString(data)

	return "data:image/png;base64," + encoded, nil
}

func writeNativeText(text string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)

	return cmd.Run()
}
