//go:build linux

package clipboard

import (
	"encoding/base64"
	"fmt"
	"os/exec"
	"strings"
)

func readText() (string, error) {
	output, waylandErr := exec.Command("wl-paste", "--no-newline").Output()
	if waylandErr == nil {
		return string(output), nil
	}

	output, x11Err := exec.Command("xclip", "-selection", "clipboard", "-o").Output()
	if x11Err == nil {
		return string(output), nil
	}

	return "", fmt.Errorf("wl-paste: %v; xclip: %w", waylandErr, x11Err)
}

func readImage() (string, error) {
	var output []byte

	data, waylandErr := exec.Command("wl-paste", "-t", "image/png").Output()
	if waylandErr == nil && len(data) > 0 {
		output = data
	} else if data, x11Err := exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-o").Output(); x11Err == nil && len(data) > 0 {
		output = data
	} else if waylandErr != nil && x11Err != nil {
		return "", fmt.Errorf("wl-paste: %v; xclip: %w", waylandErr, x11Err)
	}

	if len(output) == 0 {
		return "", nil
	}

	encoded := base64.StdEncoding.EncodeToString(output)

	return "data:image/png;base64," + encoded, nil
}

func writeNativeText(text string) error {

	cmd := exec.Command("wl-copy")
	cmd.Stdin = strings.NewReader(text)

	if err := cmd.Run(); err == nil {
		return nil
	}

	cmd = exec.Command("xclip", "-selection", "clipboard")
	cmd.Stdin = strings.NewReader(text)

	return cmd.Run()
}
