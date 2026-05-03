package shell

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

const (
	defaultTimeout = 120
	maxLines       = 2000
	maxBytes       = 50 * 1024
)

func Tools(workDir string, elicit *tool.Elicitation) []tool.Tool {
	description := strings.Join([]string{
		fmt.Sprintf("Execute a shell command and return its output. Default timeout: %ds, max: 600s.", defaultTimeout),
		"",
		"IMPORTANT: Prefer dedicated tools for routine file operations:",
		"- File search: `find` (NOT shell find/ls)",
		"- Content search: `grep` (NOT shell grep/rg)",
		"- Read files: `read` (NOT cat/head/tail)",
		"- Edit files: `edit` (NOT sed/awk)",
		"- Write files: `write` (NOT echo/cat with heredoc)",
		"Use shell for build, test, run, package-manager, git, formatter, generator, and project commands.",
		"",
		"Usage:",
		"- Always provide a brief description of what the command does.",
		"- For multiple independent commands, make multiple shell calls in parallel.",
		"- For dependent commands, chain with && in a single call (on Windows PowerShell 5.1, use `; if ($?) {` instead since && is not supported).",
		"- Use ; only when you need sequential execution but don't care if earlier commands fail.",
		"- For git: prefer new commits over amending; never use --no-verify, --force, or -i (interactive) unless explicitly asked.",
		"- If a command is long-running, increase the timeout instead of using sleep.",
	}, "\n")

	return []tool.Tool{{
		Name:        "shell",
		Description: description,
		Effect:      ClassifyEffect,

		Parameters: map[string]any{
			"type": "object",

			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The shell command to execute",
				},

				"description": map[string]any{
					"type":        "string",
					"description": "Brief description of what this command does (e.g., \"Run unit tests\", \"Install dependencies\")",
				},

				"timeout": map[string]any{
					"type":        "integer",
					"description": fmt.Sprintf("Timeout in seconds (default: %d, max: 600)", defaultTimeout),
				},
			},

			"required": []string{"command"},
		},

		Execute: func(ctx context.Context, args map[string]any) (string, error) {
			return executeShell(ctx, workDir, elicit, args)
		},
	}}
}

func ClassifyEffect(args map[string]any) tool.Effect {
	if args == nil {
		return tool.EffectDynamic
	}

	command, _ := args["command"].(string)
	if IsDangerousCommand(command) {
		return tool.EffectDangerous
	}
	if IsReadOnlyCommand(command) {
		return tool.EffectReadOnly
	}

	return tool.EffectMutates
}

func IsDangerousCommand(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	if strings.Contains(command, "$(") || strings.Contains(command, "`") {
		return true
	}

	segments := splitCommandSegments(command)
	for i, seg := range segments {
		if isDangerousSingleCommand(seg) {
			return true
		}
		if i > 0 && isShellInterpreter(seg) && isDownloadCommand(segments[i-1]) {
			return true
		}
	}

	return false
}

func IsReadOnlyCommand(command string) bool {
	return isAllowlistedReadCommand(command) && !hasMutationSyntax(command)
}

func isAllowlistedReadCommand(command string) bool {
	command = strings.TrimSpace(command)

	if command == "" {
		return false
	}

	// Split on pipes, chains, and semicolons; every segment must be read-only.
	segments := splitCommandSegments(command)

	for _, seg := range segments {
		if !isSingleCommandReadOnly(seg) {
			return false
		}
	}

	// Check for command substitution $(...) or `...` — treat as unsafe
	if strings.Contains(command, "$(") || strings.Contains(command, "`") {
		return false
	}

	return true
}

func hasMutationSyntax(command string) bool {
	if containsUnquotedShellRedirection(command) {
		return true
	}

	words := strings.Fields(strings.ToLower(command))
	for i, word := range words {
		if filepath.Base(word) != "sed" {
			continue
		}
		for _, arg := range words[i+1:] {
			if arg == "-i" || strings.HasPrefix(arg, "-i.") || arg == "--in-place" || strings.HasPrefix(arg, "--in-place=") {
				return true
			}
		}
	}

	return false
}

func containsUnquotedShellRedirection(command string) bool {
	inSingle := false
	inDouble := false
	escaped := false

	for _, r := range command {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && !inSingle {
			escaped = true
			continue
		}
		if r == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if r == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if !inSingle && !inDouble && (r == '>' || r == '<') {
			return true
		}
	}

	return false
}

// splitCommandSegments splits a command string on |, &&, ||, and ; boundaries.
// It respects quoted strings and parentheses.
func splitCommandSegments(command string) []string {
	var segments []string
	var current strings.Builder

	inSingle := false
	inDouble := false
	i := 0

	for i < len(command) {
		ch := command[i]

		// Handle escape characters
		if ch == '\\' && i+1 < len(command) && !inSingle {
			current.WriteByte(ch)
			i++
			current.WriteByte(command[i])
			i++
			continue
		}

		// Handle quotes
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			current.WriteByte(ch)
			i++
			continue
		}

		if ch == '"' && !inSingle {
			inDouble = !inDouble
			current.WriteByte(ch)
			i++
			continue
		}

		// Don't split inside quotes
		if inSingle || inDouble {
			current.WriteByte(ch)
			i++
			continue
		}

		// Check for && or ||
		if i+1 < len(command) && ((ch == '&' && command[i+1] == '&') || (ch == '|' && command[i+1] == '|')) {
			seg := strings.TrimSpace(current.String())
			if seg != "" {
				segments = append(segments, seg)
			}
			current.Reset()
			i += 2
			continue
		}

		// Check for pipe |
		if ch == '|' {
			seg := strings.TrimSpace(current.String())
			if seg != "" {
				segments = append(segments, seg)
			}
			current.Reset()
			i++
			continue
		}

		// Check for semicolon ;
		if ch == ';' || ch == '\n' {
			seg := strings.TrimSpace(current.String())
			if seg != "" {
				segments = append(segments, seg)
			}
			current.Reset()
			i++
			continue
		}

		current.WriteByte(ch)
		i++
	}

	seg := strings.TrimSpace(current.String())
	if seg != "" {
		segments = append(segments, seg)
	}

	return segments
}

// isSingleCommandReadOnly checks if a single command segment is read-only.
func isSingleCommandReadOnly(command string) bool {
	command = strings.TrimSpace(command)

	// Extract words from the command
	words := strings.Fields(command)

	if len(words) == 0 {
		return false
	}

	cmd := strings.ToLower(filepath.Base(words[0]))
	if _, ok := readOnlyCommandSet[cmd]; ok {
		return !hasUnsafeReadOnlyCommandOptions(cmd, words[1:])
	}

	allowedSubcmds, hasSubcmds := readOnlySubcommandPrefixes[cmd]

	if !hasSubcmds {
		return false
	}

	if len(words) < 2 {
		return false
	}

	restOfCommand := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(command, words[0])))

	for _, subCmd := range allowedSubcmds {
		if hasSubcommandPrefix(restOfCommand, subCmd) {
			return !hasUnsafeReadOnlySubcommandOptions(cmd, words[1:])
		}
	}

	return false
}

func hasSubcommandPrefix(command, prefix string) bool {
	if !strings.HasPrefix(command, prefix) {
		return false
	}
	if len(command) == len(prefix) {
		return true
	}

	return command[len(prefix)] == ' '
}

func hasUnsafeReadOnlyCommandOptions(cmd string, args []string) bool {
	switch cmd {
	case "find":
		for _, arg := range args {
			switch arg {
			case "-exec", "-execdir", "-ok", "-okdir", "-delete", "-fls", "-fprint", "-fprint0", "-fprintf":
				return true
			}
		}
	case "rg":
		for _, arg := range args {
			if arg == "-z" || arg == "--search-zip" ||
				arg == "--pre" || strings.HasPrefix(arg, "--pre=") ||
				arg == "--hostname-bin" || strings.HasPrefix(arg, "--hostname-bin=") {
				return true
			}
		}
	}

	return false
}

func hasUnsafeReadOnlySubcommandOptions(cmd string, args []string) bool {
	switch cmd {
	case "git":
		return hasUnsafeGitOptions(args)
	}

	return false
}

func hasUnsafeGitOptions(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "-C", "-c", "--config-env", "--exec-path", "--git-dir", "--namespace", "--super-prefix", "--work-tree",
			"--output", "--ext-diff", "--textconv", "--exec", "--paginate":
			return true
		}
		if strings.HasPrefix(arg, "-C") && arg != "-C" {
			return true
		}
		if strings.HasPrefix(arg, "-c") && arg != "-c" {
			return true
		}
		if strings.HasPrefix(arg, "--config-env=") ||
			strings.HasPrefix(arg, "--exec-path=") ||
			strings.HasPrefix(arg, "--git-dir=") ||
			strings.HasPrefix(arg, "--namespace=") ||
			strings.HasPrefix(arg, "--super-prefix=") ||
			strings.HasPrefix(arg, "--work-tree=") ||
			strings.HasPrefix(arg, "--output=") ||
			strings.HasPrefix(arg, "--exec=") {
			return true
		}
	}

	return false
}

func isDangerousSingleCommand(command string) bool {
	words := strings.Fields(strings.TrimSpace(command))
	if len(words) == 0 {
		return false
	}

	cmd := strings.ToLower(filepath.Base(words[0]))
	args := words[1:]

	switch cmd {
	case "sudo", "su", "doas":
		return true
	case "chmod", "chown", "chgrp":
		return true
	case "kill", "pkill", "killall":
		return true
	case "dd", "mkfs", "mount", "umount", "diskutil", "launchctl", "systemctl", "service":
		return true
	case "powershell", "powershell.exe", "pwsh", "pwsh.exe":
		return isDangerousPowerShellInvocation(args)
	case "cmd", "cmd.exe":
		return IsDangerousCommand(extractCmdScript(args))
	case "remove-item", "ri":
		return hasPowerShellForceOrRecursive(args)
	case "stop-process":
		return hasAnyArgFold(args, "-force")
	case "invoke-expression", "iex", "set-executionpolicy", "new-service", "sc.exe", "reg", "reg.exe":
		return true
	case "del", "erase":
		return hasAnyArgFold(args, "/f")
	case "rd", "rmdir":
		return hasAnyArgFold(args, "/s")
	case "start", "explorer", "explorer.exe", "mshta", "mshta.exe":
		return argsHaveURL(args)
	case "rundll32", "rundll32.exe":
		return argsHaveURL(args) && containsArgFold(args, "url.dll,fileprotocolhandler")
	case "rm":
		return hasAnyArg(args, "-r", "-R", "-rf", "-fr") || hasAnyArgPrefix(args, "--recursive")
	case "find":
		return hasAnyArg(args, "-exec", "-execdir", "-ok", "-okdir", "-delete")
	case "git":
		return isDangerousGitCommand(args)
	}

	return false
}

func extractCmdScript(args []string) string {
	for i, arg := range args {
		switch strings.ToLower(strings.Trim(arg, `"'`)) {
		case "/c", "/r", "-c":
			if i+1 < len(args) {
				return trimOuterQuotes(strings.Join(args[i+1:], " "))
			}
			return ""
		}
	}

	return ""
}

func isDangerousPowerShellInvocation(args []string) bool {
	for _, arg := range args {
		switch strings.ToLower(strings.Trim(arg, `"'`)) {
		case "-encodedcommand", "-ec", "-e", "-file", "/file", "-executionpolicy":
			return true
		}
	}

	return isDangerousPowerShellScript(extractPowerShellScript(args))
}

func extractPowerShellScript(args []string) string {
	for i, arg := range args {
		lower := strings.ToLower(strings.Trim(arg, `"'`))
		switch lower {
		case "-command", "/command", "-c":
			if i+1 < len(args) {
				return trimOuterQuotes(strings.Join(args[i+1:], " "))
			}
			return ""
		}
		if strings.HasPrefix(lower, "-command:") || strings.HasPrefix(lower, "/command:") {
			return trimOuterQuotes(arg[strings.Index(arg, ":")+1:])
		}
		if !strings.HasPrefix(lower, "-") && !strings.HasPrefix(lower, "/") {
			return trimOuterQuotes(strings.Join(args[i:], " "))
		}
	}

	return ""
}

func trimOuterQuotes(value string) string {
	value = strings.TrimSpace(value)
	if len(value) < 2 {
		return value
	}

	first := value[0]
	last := value[len(value)-1]
	if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
		return value[1 : len(value)-1]
	}

	return value
}

func isDangerousPowerShellScript(script string) bool {
	if strings.TrimSpace(script) == "" {
		return false
	}

	segments := splitCommandSegments(script)
	for i, segment := range segments {
		words := strings.Fields(segment)
		if len(words) == 0 {
			continue
		}

		cmd := strings.ToLower(strings.Trim(words[0], `"'(){}[]`))
		args := words[1:]

		switch cmd {
		case "remove-item", "ri", "rm", "del", "erase", "rd", "rmdir":
			if hasPowerShellForceOrRecursive(args) {
				return true
			}
		case "stop-process":
			if hasAnyArgFold(args, "-force") {
				return true
			}
		case "invoke-expression", "iex", "set-executionpolicy", "new-service", "sc.exe", "reg", "reg.exe":
			return true
		case "start-process", "start", "saps", "invoke-item", "ii", "explorer", "explorer.exe", "mshta", "mshta.exe":
			if argsHaveURL(args) {
				return true
			}
		case "rundll32", "rundll32.exe":
			if argsHaveURL(args) && containsArgFold(args, "url.dll,fileprotocolhandler") {
				return true
			}
		}

		if i > 0 && (cmd == "invoke-expression" || cmd == "iex") && isPowerShellDownloadCommand(segments[i-1]) {
			return true
		}
	}

	return false
}

func hasPowerShellForceOrRecursive(args []string) bool {
	return hasAnyArgFold(args, "-force", "-recurse", "-recursive") ||
		hasAnyArgPrefixFold(args, "-force:", "-recurse:", "-recursive:")
}

func isPowerShellDownloadCommand(command string) bool {
	words := strings.Fields(strings.TrimSpace(command))
	if len(words) == 0 {
		return false
	}
	switch strings.ToLower(strings.Trim(words[0], `"'(){}[]`)) {
	case "invoke-webrequest", "iwr", "curl", "wget":
		return true
	default:
		return false
	}
}

func isDangerousGitCommand(args []string) bool {
	subcommand := firstNonFlagArg(args)
	switch subcommand {
	case "clean":
		return true
	case "reset":
		return hasAnyArg(args, "--hard")
	case "checkout":
		return hasAnyArg(args, "-f", "--force")
	case "push":
		return hasAnyArg(args, "--force", "--force-with-lease", "-f")
	case "branch":
		return hasAnyArg(args, "-D")
	}

	return false
}

func firstNonFlagArg(args []string) string {
	skipNext := false
	for _, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		switch arg {
		case "-C", "-c", "--config-env", "--exec-path", "--git-dir", "--namespace", "--super-prefix", "--work-tree":
			skipNext = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return arg
	}

	return ""
}

func isDownloadCommand(command string) bool {
	words := strings.Fields(strings.TrimSpace(command))
	if len(words) == 0 {
		return false
	}
	switch strings.ToLower(filepath.Base(words[0])) {
	case "curl", "wget":
		return true
	default:
		return false
	}
}

func isShellInterpreter(command string) bool {
	words := strings.Fields(strings.TrimSpace(command))
	if len(words) == 0 {
		return false
	}
	switch strings.ToLower(filepath.Base(words[0])) {
	case "sh", "bash", "zsh", "fish":
		return true
	default:
		return false
	}
}

func hasAnyArg(args []string, values ...string) bool {
	for _, arg := range args {
		for _, value := range values {
			if arg == value {
				return true
			}
		}
	}

	return false
}

func hasAnyArgFold(args []string, values ...string) bool {
	for _, arg := range args {
		arg = strings.ToLower(strings.Trim(arg, `"'`))
		for _, value := range values {
			if arg == strings.ToLower(value) {
				return true
			}
		}
	}

	return false
}

func hasAnyArgPrefix(args []string, prefixes ...string) bool {
	for _, arg := range args {
		for _, prefix := range prefixes {
			if strings.HasPrefix(arg, prefix) {
				return true
			}
		}
	}

	return false
}

func hasAnyArgPrefixFold(args []string, prefixes ...string) bool {
	for _, arg := range args {
		arg = strings.ToLower(strings.Trim(arg, `"'`))
		for _, prefix := range prefixes {
			if strings.HasPrefix(arg, strings.ToLower(prefix)) {
				return true
			}
		}
	}

	return false
}

func containsArgFold(args []string, needle string) bool {
	needle = strings.ToLower(needle)
	for _, arg := range args {
		if strings.Contains(strings.ToLower(strings.Trim(arg, `"'`)), needle) {
			return true
		}
	}

	return false
}

func argsHaveURL(args []string) bool {
	for _, arg := range args {
		lower := strings.ToLower(strings.Trim(arg, `"'`))
		if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
			return true
		}
	}

	return false
}

func executeShell(ctx context.Context, workDir string, elicit *tool.Elicitation, args map[string]any) (string, error) {
	command, ok := args["command"].(string)

	if !ok || command == "" {
		return "", fmt.Errorf("command is required")
	}

	timeout := defaultTimeout

	if t, ok := args["timeout"].(float64); ok {
		timeout = int(t)
	}

	// Cap timeout at 600 seconds
	if timeout > 600 {
		timeout = 600
	}

	if elicit != nil && elicit.Confirm != nil && ClassifyEffect(args) == tool.EffectDangerous {
		approved, err := elicit.Confirm(ctx, "\u276f "+command)

		if err != nil {
			return "", fmt.Errorf("failed to get user approval: %w", err)
		}

		if !approved {
			return "", fmt.Errorf("command execution denied by user")
		}
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := buildCommand(ctx, command, workDir)

	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	err := cmd.Start()

	if err != nil {
		return "", fmt.Errorf("failed to start command: %w", err)
	}

	done := make(chan error, 1)

	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		killProcessGroup(cmd)

		return "", fmt.Errorf("command timed out after %d seconds", timeout)
	case err := <-done:
		truncated := truncateOutput(output.String())

		if err != nil {
			exitCode := -1

			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}

			truncated += fmt.Sprintf("\n\nCommand exited with code %d", exitCode)

			return truncated, nil
		}

		return truncated, nil
	}
}

func buildCommand(ctx context.Context, command, workingDir string) *exec.Cmd {
	var cmd *exec.Cmd

	if runtime.GOOS == "windows" {
		ps := findPowerShell()
		// Force UTF-8 output to avoid PowerShell 5.1's UTF-16 default
		wrapped := "[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; " + command
		cmd = exec.CommandContext(ctx, ps, "-NoProfile", "-NoLogo", "-NonInteractive", "-Command", wrapped)
	} else {
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
		cmd = exec.CommandContext(ctx, shell, "-c", command)
	}

	cmd.Dir = workingDir
	cmd.Env = append(os.Environ(),
		"GIT_EDITOR=true", // Prevent git from opening interactive editors
		"WINGMAN=1",       // Marker so scripts can detect agent context
	)

	setupProcessGroup(cmd)

	return cmd
}

// findPowerShell returns the path to PowerShell. Prefers pwsh (PowerShell 7+)
// which supports && and || operators. Falls back to powershell (5.1).
func findPowerShell() string {
	if ps, err := exec.LookPath("pwsh"); err == nil {
		return ps
	}
	return "powershell"
}

func truncateOutput(output string) string {
	totalLines := strings.Count(output, "\n") + 1
	totalBytes := len(output)

	needsTruncation := totalLines > maxLines || totalBytes > maxBytes

	if !needsTruncation {
		return output
	}

	lines := strings.Split(output, "\n")

	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}

	truncated := strings.Join(lines, "\n")

	if len(truncated) > maxBytes {
		truncated = truncated[len(truncated)-maxBytes:]
	}

	shownLines := strings.Count(truncated, "\n") + 1
	shownBytes := len(truncated)

	notice := fmt.Sprintf("[Output truncated: showing last %d of %d lines (%d of %d bytes)]\n\n",
		shownLines, totalLines, shownBytes, totalBytes)

	return notice + truncated
}
