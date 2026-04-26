//go:build windows

package clipboard

import (
	"io"
	"strings"
	"testing"
)

func TestBuildPowerShellArgs_DefaultFlags(t *testing.T) {
	args := buildPowerShellArgs("Get-Clipboard", false)
	joined := strings.Join(args, " ")

	for _, flag := range []string{"-NoProfile", "-NoLogo", "-NonInteractive", "-Command"} {
		if !strings.Contains(joined, flag) {
			t.Fatalf("missing flag %q in args: %v", flag, args)
		}
	}

	if strings.Contains(joined, " -Sta ") {
		t.Fatalf("did not expect -Sta flag in args: %v", args)
	}

	lastArg := args[len(args)-1]
	if !strings.HasPrefix(lastArg, powerShellEncodingPrefix) {
		t.Fatalf("expected UTF-8 encoding prefix in command, got %q", lastArg)
	}
}

func TestBuildPowerShellArgs_STA(t *testing.T) {
	args := buildPowerShellArgs("Get-Clipboard", true)
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "-Sta") {
		t.Fatalf("expected -Sta flag in args: %v", args)
	}
}

func TestBuildWriteTextCommand_UsesSTDIN(t *testing.T) {
	text := "line 1\nline 2"
	cmd := buildWriteTextCommand(text)

	if !strings.Contains(cmd.Path, "powershell") {
		t.Fatalf("expected powershell executable, got %q", cmd.Path)
	}

	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "Set-Clipboard -Value ([Console]::In.ReadToEnd())") {
		t.Fatalf("expected stdin-based Set-Clipboard command, got args: %v", cmd.Args)
	}

	stdin, ok := cmd.Stdin.(io.Reader)
	if !ok {
		t.Fatal("expected stdin reader to be configured")
	}

	data, err := io.ReadAll(stdin)
	if err != nil {
		t.Fatalf("failed reading stdin payload: %v", err)
	}

	if string(data) != text {
		t.Fatalf("expected stdin payload %q, got %q", text, string(data))
	}
}