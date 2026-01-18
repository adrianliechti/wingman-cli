//go:build linux

package clipboard

import (
	"encoding/base64"
	"os/exec"
)

// Read reads text and image content from the Linux clipboard.
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
	if output, err := exec.Command("wl-paste", "--no-newline").Output(); err == nil {
		return string(output), nil
	}

	if output, err := exec.Command("xclip", "-selection", "clipboard", "-o").Output(); err == nil {
		return string(output), nil
	}

	return "", nil
}

func readImage() (string, error) {
	var output []byte

	if data, err := exec.Command("wl-paste", "-t", "image/png").Output(); err == nil && len(data) > 0 {
		output = data
	} else if data, err := exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-o").Output(); err == nil && len(data) > 0 {
		output = data
	}

	if len(output) == 0 {
		return "", nil
	}

	encoded := base64.StdEncoding.EncodeToString(output)

	return "data:image/png;base64," + encoded, nil
}
