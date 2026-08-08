package shell

import (
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

// maxClassifiableBytes bounds what the classifier will reason about; anything
// longer fails closed to a confirmation prompt.
const maxClassifiableBytes = 10_000

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
	if len(command) > maxClassifiableBytes {
		return true
	}
	// Heredoc bodies are data, not commands; substitutions inside unquoted
	// heredocs still execute and are kept for classification.
	command = stripHeredocBodies(command)
	if hasObfuscatingCharacters(command) {
		return true
	}
	if hasDangerousRedirectTarget(command) {
		return true
	}
	if hasDangerousCommandSubstitution(command) {
		return true
	}

	segments := splitCommandSegments(command)
	for i, seg := range segments {
		if isDangerousSingleCommand(seg) {
			return true
		}
		if isShellInterpreter(seg) {
			if i > 0 && isDownloadCommand(segments[i-1]) {
				return true
			}
			if slices.ContainsFunc(extractCommandSubstitutions(seg), isDownloadCommand) {
				return true
			}
		}
	}

	return false
}

// hasObfuscatingCharacters flags control and invisible formatting characters
// (except newline and tab): they can make the executed command differ from
// what any prompt or transcript displays.
func hasObfuscatingCharacters(command string) bool {
	for _, r := range command {
		if r == '\n' || r == '\t' {
			continue
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return true
		}
	}
	return false
}

func stripHeredocBodies(command string) string {
	if !strings.Contains(command, "<<") {
		return command
	}

	lines := strings.Split(command, "\n")
	var out []string

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		out = append(out, line)

		delim, quoted, ok := heredocDelimiter(line)
		if !ok {
			continue
		}

		j := i + 1
		for ; j < len(lines) && strings.TrimSpace(lines[j]) != delim; j++ {
			if !quoted {
				out = append(out, extractCommandSubstitutions(lines[j])...)
			}
		}
		i = j
	}

	return strings.Join(out, "\n")
}

// heredocDelimiter finds an unquoted `<<` (not `<<<`) redirect on the line
// and returns its delimiter word and whether it was quoted (a quoted
// delimiter makes the body fully inert).
func heredocDelimiter(line string) (string, bool, bool) {
	inSingle := false
	inDouble := false
	escaped := false

	for i := 0; i < len(line); i++ {
		ch := line[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && !inSingle {
			escaped = true
			continue
		}
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if inSingle || inDouble || ch != '<' || i+1 >= len(line) || line[i+1] != '<' {
			continue
		}

		j := i + 2
		if j < len(line) && line[j] == '<' {
			i = j
			continue
		}
		if j < len(line) && line[j] == '-' {
			j++
		}
		for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
			j++
		}

		if j < len(line) && (line[j] == '\'' || line[j] == '"') {
			quote := line[j]
			j++
			start := j
			for j < len(line) && line[j] != quote {
				j++
			}
			if word := line[start:j]; word != "" {
				return word, true, true
			}
			continue
		}

		start := j
		for j < len(line) && !strings.ContainsRune(" \t;|&<>()", rune(line[j])) {
			j++
		}
		if word := line[start:j]; word != "" {
			return word, false, true
		}
	}

	return "", false, false
}

func hasDangerousRedirectTarget(command string) bool {
	inSingle := false
	inDouble := false
	escaped := false

	for i := 0; i < len(command); i++ {
		ch := command[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && !inSingle {
			escaped = true
			continue
		}
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if inSingle || inDouble || ch != '>' {
			continue
		}

		j := i + 1
		for j < len(command) && (command[j] == '>' || command[j] == '|') {
			j++
		}
		if j < len(command) && command[j] == '(' {
			continue
		}
		// >&N and >&- duplicate or close descriptors; >&file writes the file.
		if j < len(command) && command[j] == '&' {
			j++
			if j < len(command) && ((command[j] >= '0' && command[j] <= '9') || command[j] == '-') {
				continue
			}
		}
		for j < len(command) && (command[j] == ' ' || command[j] == '\t') {
			j++
		}
		start := j
		for j < len(command) && !strings.ContainsRune(" \t\n;|&<>()`", rune(command[j])) {
			j++
		}
		if isProtectedRedirectTarget(command[start:j]) {
			return true
		}
		i = j - 1
	}

	return false
}

// isProtectedRedirectTarget flags redirect destinations whose overwrite is
// destructive or leads to later command execution: devices, system config,
// and shell/git startup files.
func isProtectedRedirectTarget(path string) bool {
	path = strings.ToLower(strings.Trim(path, `"'`))
	path = strings.ReplaceAll(path, `\`, "/")
	if path == "" {
		return false
	}

	if len(path) > 2 && path[1] == ':' && strings.HasPrefix(path[2:], "/windows/") {
		return true
	}

	if strings.HasPrefix(path, "/dev/") {
		switch path {
		case "/dev/null", "/dev/stdout", "/dev/stderr", "/dev/tty", "/dev/zero":
			return false
		}
		return !strings.HasPrefix(path, "/dev/fd/")
	}

	if strings.HasPrefix(path, "/etc/") || strings.HasPrefix(path, "/boot/") ||
		strings.HasPrefix(path, "/sys/") || strings.HasPrefix(path, "/proc/sys/") {
		return true
	}

	if strings.Contains(path, "/.config/git/") {
		return true
	}

	if strings.Contains(path, "/.ssh/") || strings.Contains(path, "/spool/cron/") ||
		strings.Contains(path, "/cron.d/") || strings.Contains(path, "/.config/systemd/user/") {
		return true
	}

	switch path[strings.LastIndexByte(path, '/')+1:] {
	case ".zshrc", ".zshenv", ".zprofile", ".zlogin", ".bashrc", ".bash_profile", ".bash_login", ".bash_logout", ".profile", ".gitconfig",
		"profile.ps1", "microsoft.powershell_profile.ps1", "microsoft.vscode_profile.ps1",
		"authorized_keys", "authorized_keys2", "crontab":
		return true
	}

	return false
}

func IsReadOnlyCommand(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	if hasShellCommandSubstitution(command) {
		return false
	}
	if hasMutationSyntax(command) {
		return false
	}

	for _, seg := range splitCommandSegments(command) {
		if !isSingleCommandReadOnly(seg) {
			return false
		}
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

func hasShellCommandSubstitution(command string) bool {
	return len(extractCommandSubstitutions(command)) > 0
}

func hasDangerousCommandSubstitution(command string) bool {
	return slices.ContainsFunc(extractCommandSubstitutions(command), IsDangerousCommand)
}

func extractCommandSubstitutions(command string) []string {
	var substitutions []string

	inSingle := false
	inDouble := false
	escaped := false

	for i := 0; i < len(command); i++ {
		ch := command[i]

		if escaped {
			escaped = false
			continue
		}

		if ch == '\\' && !inSingle {
			escaped = true
			continue
		}

		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}

		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}

		if inSingle {
			continue
		}

		if (ch == '$' || ch == '<' || ch == '>') && i+1 < len(command) && command[i+1] == '(' {
			if sub, end, ok := readParenSubstitution(command, i+2); ok {
				substitutions = append(substitutions, sub)
				i = end
			}
			continue
		}

		if ch == '`' {
			if sub, end, ok := readBacktickSubstitution(command, i+1); ok {
				substitutions = append(substitutions, sub)
				i = end
			}
		}
	}

	return substitutions
}

func readParenSubstitution(command string, start int) (string, int, bool) {
	depth := 1
	inSingle := false
	inDouble := false
	escaped := false

	for i := start; i < len(command); i++ {
		ch := command[i]

		if escaped {
			escaped = false
			continue
		}

		if ch == '\\' && !inSingle {
			escaped = true
			continue
		}

		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}

		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}

		if inSingle || inDouble {
			continue
		}

		if ch == '(' {
			depth++
			continue
		}

		if ch == ')' {
			depth--
			if depth == 0 {
				return command[start:i], i, true
			}
		}
	}

	return "", 0, false
}

func readBacktickSubstitution(command string, start int) (string, int, bool) {
	escaped := false

	for i := start; i < len(command); i++ {
		ch := command[i]

		if escaped {
			escaped = false
			continue
		}

		if ch == '\\' {
			escaped = true
			continue
		}

		if ch == '`' {
			return command[start:i], i, true
		}
	}

	return "", 0, false
}

func splitCommandSegments(command string) []string {
	var segments []string
	var current strings.Builder

	inSingle := false
	inDouble := false
	i := 0

	flush := func() {
		seg := strings.TrimSpace(current.String())
		if seg != "" {
			segments = append(segments, seg)
		}
		current.Reset()
	}

	for i < len(command) {
		ch := command[i]

		if ch == '\\' && i+1 < len(command) && !inSingle {
			current.WriteByte(ch)
			i++
			current.WriteByte(command[i])
			i++
			continue
		}

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

		if inSingle || inDouble {
			current.WriteByte(ch)
			i++
			continue
		}

		if i+1 < len(command) && ((ch == '&' && command[i+1] == '&') || (ch == '|' && command[i+1] == '|')) {
			flush()
			i += 2
			continue
		}

		if ch == '&' {
			var prev byte
			if i > 0 {
				prev = command[i-1]
			}
			next := byte(0)
			if i+1 < len(command) {
				next = command[i+1]
			}
			if prev != '>' && prev != '<' && next != '>' {
				flush()
				i++
				continue
			}
		}

		if ch == '|' || ch == ';' || ch == '\n' {
			flush()
			i++
			continue
		}

		current.WriteByte(ch)
		i++
	}

	flush()

	return segments
}

var commandRunners = map[string]bool{
	"env":     true,
	"exec":    true,
	"xargs":   true,
	"timeout": true,
	"nice":    true,
	"command": true,
	"time":    true,
	"nohup":   true,
	"stdbuf":  true,
	"setsid":  true,
	"ionice":  true,
	"taskset": true,
	"setarch": true,
}

func unwrapCommandWords(words []string) (resolved []string, cmd string, unresolved bool) {
	for {

		for len(words) > 0 && isEnvAssignment(words[0]) {
			words = words[1:]
		}
		if len(words) == 0 {
			return nil, "", true
		}

		name := strings.TrimPrefix(strings.Trim(words[0], `"'`), `\`)
		base := strings.ToLower(filepath.Base(name))

		if !commandRunners[base] {
			return words, base, false
		}

		rest := words[1:]
		rest = skipRunnerFlags(base, rest)
		if len(rest) == 0 {
			return nil, base, true
		}
		words = rest
	}
}

func skipRunnerFlags(runner string, args []string) []string {
	for len(args) > 0 {
		arg := args[0]
		if !strings.HasPrefix(arg, "-") {
			break
		}

		if arg == "--" {
			return args[1:]
		}

		switch runner {
		case "timeout":

			if arg == "-s" || arg == "--signal" || arg == "-k" || arg == "--kill-after" {
				args = args[2:]
				continue
			}
		case "nice", "ionice":
			if arg == "-n" || arg == "--adjustment" || arg == "-c" || arg == "-p" {
				args = args[2:]
				continue
			}
		case "env":
			if arg == "-u" || arg == "--unset" {
				args = args[2:]
				continue
			}
		case "stdbuf":
			if arg == "-i" || arg == "-o" || arg == "-e" {
				args = args[2:]
				continue
			}
		case "taskset", "setarch":
			args = args[1:]
			continue
		case "xargs":
			switch arg {
			case "-I", "-i", "-n", "-P", "-L", "-s", "-a", "-d", "-E",
				"--replace", "--max-args", "--max-procs", "--max-lines",
				"--max-chars", "--arg-file", "--delimiter", "--eof":
				if len(args) >= 2 {
					args = args[2:]
					continue
				}
			}
		}
		args = args[1:]
	}

	if runner == "env" {
		for len(args) > 0 && isEnvAssignment(args[0]) {
			args = args[1:]
		}
	}

	if runner == "timeout" && len(args) > 0 {
		args = args[1:]
	}
	return args
}

func isEnvAssignment(word string) bool {
	eq := strings.IndexByte(word, '=')
	if eq <= 0 {
		return false
	}
	name := word[:eq]
	for i, r := range name {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			continue
		}
		if i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func isSingleCommandReadOnly(command string) bool {
	command = strings.TrimSpace(command)

	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}

	words, cmd, unresolved := unwrapCommandWords(fields)
	if unresolved {
		return cmd == ""
	}

	subs, ok := normalizedReadOnlyCommands[cmd]
	if !ok {
		return false
	}

	args := words[1:]

	switch cmd {
	case "find":
		for _, arg := range args {
			switch arg {
			case "-exec", "-execdir", "-ok", "-okdir", "-delete", "-fls", "-fprint", "-fprint0", "-fprintf":
				return false
			}
		}
	case "sort":

		for _, arg := range args {
			if arg == "-o" || arg == "--output" || strings.HasPrefix(arg, "-o") || strings.HasPrefix(arg, "--output=") {
				return false
			}
		}
	case "jq", "yq", "xq":

		for _, arg := range args {
			if arg == "-i" || arg == "--in-place" || arg == "--inplace" {
				return false
			}
		}
	case "rg":
		for _, arg := range args {
			if arg == "-z" || arg == "--search-zip" ||
				arg == "--pre" || strings.HasPrefix(arg, "--pre=") ||
				arg == "--hostname-bin" || strings.HasPrefix(arg, "--hostname-bin=") {
				return false
			}
		}
	case "fd":
		for _, arg := range args {
			switch arg {
			case "-x", "--exec", "-X", "--exec-batch":
				return false
			}
		}
	case "sed":
		// Only the plain print form is read-only: sed -n '1,50p' file.
		if len(args) < 2 || len(args) > 3 || args[0] != "-n" || !isSedPrintScript(strings.Trim(args[1], `"'`)) {
			return false
		}
	case "base64":
		for _, arg := range args {
			if arg == "-o" || arg == "--output" || strings.HasPrefix(arg, "--output=") ||
				(strings.HasPrefix(arg, "-o") && arg != "-o") {
				return false
			}
		}
	case "date":
		for _, arg := range args {
			if arg == "-s" || arg == "--set" || strings.HasPrefix(arg, "--set=") {
				return false
			}
		}
	case "xxd":
		for _, arg := range args {
			if arg == "-r" || arg == "-revert" {
				return false
			}
		}
	case "file":
		for _, arg := range args {
			if arg == "-m" || arg == "-M" || arg == "-f" ||
				arg == "--magic-file" || strings.HasPrefix(arg, "--magic-file=") ||
				arg == "--files-from" || strings.HasPrefix(arg, "--files-from=") {
				return false
			}
		}
	case "man":
		for _, arg := range args {
			if strings.HasPrefix(arg, "-P") || strings.HasPrefix(arg, "-H") ||
				arg == "--pager" || strings.HasPrefix(arg, "--pager=") ||
				arg == "--html" || strings.HasPrefix(arg, "--html=") {
				return false
			}
		}
	case "docker", "docker-compose":
		for _, arg := range args {
			if strings.HasPrefix(arg, "-H") || arg == "-c" ||
				arg == "--host" || strings.HasPrefix(arg, "--host=") ||
				arg == "--context" || strings.HasPrefix(arg, "--context=") ||
				arg == "--config" || strings.HasPrefix(arg, "--config=") ||
				arg == "--url" || strings.HasPrefix(arg, "--url=") ||
				arg == "--connection" || strings.HasPrefix(arg, "--connection=") ||
				arg == "--identity" || strings.HasPrefix(arg, "--identity=") {
				return false
			}
		}
	case "git":
		if hasUnsafeGitOptions(args) {
			return false
		}
	}

	if len(subs) == 0 {
		return true
	}

	if len(args) == 0 {
		return false
	}

	rest := strings.ToLower(strings.Join(args, " "))
	for _, sub := range subs {
		if hasSubcommandPrefix(rest, sub) {
			switch {
			case cmd == "git":
				return gitSubcommandReadOnly(sub, strings.Fields(rest[len(sub):]))
			case (cmd == "npm" || cmd == "pnpm" || cmd == "yarn") && sub == "audit":
				return npmAuditReadOnly(strings.Fields(rest[len(sub):]))
			case cmd == "bun" && sub == "pm cache":
				return bunPmCacheReadOnly(strings.Fields(rest[len(sub):]))
			}
			return true
		}
	}

	return false
}

// npmAuditReadOnly gates `npm/pnpm/yarn audit`, which lists vulnerabilities
// but `audit fix`/`--fix` mutates package.json and the lockfile, and can run
// install scripts.
func npmAuditReadOnly(args []string) bool {
	for _, arg := range args {
		if arg == "fix" || arg == "--fix" || strings.HasPrefix(arg, "--fix=") {
			return false
		}
	}
	return true
}

// bunPmCacheReadOnly gates `bun pm cache`, which prints the cache path but
// `bun pm cache rm` deletes it.
func bunPmCacheReadOnly(args []string) bool {
	switch firstNonFlagWord(args) {
	case "", "dir":
		return true
	}
	return false
}

// gitSubcommandReadOnly gates git subcommands that both list and mutate:
// `git branch` lists but `git branch name` creates.
func gitSubcommandReadOnly(sub string, args []string) bool {
	switch sub {
	case "branch":
		for _, arg := range args {
			switch arg {
			case "--list", "-l", "--show-current", "-a", "--all", "-r", "--remotes", "-v", "-vv", "--verbose":
			default:
				if !strings.HasPrefix(arg, "--format=") && !strings.HasPrefix(arg, "--sort=") {
					return false
				}
			}
		}
		return true
	case "tag":
		listing := false
		positional := false
		for _, arg := range args {
			switch {
			case arg == "-l" || arg == "--list":
				listing = true
			case strings.HasPrefix(arg, "-"):
			default:
				positional = true
			}
		}
		return !positional || listing
	case "remote":
		switch firstNonFlagWord(args) {
		case "", "show", "get-url":
			return true
		}
		return false
	case "reflog":
		switch firstNonFlagWord(args) {
		case "", "show", "list":
			return true
		}
		return false
	}
	return true
}

func firstNonFlagWord(args []string) string {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	return ""
}

// isSedPrintScript matches `N p` or `N,M p` line-print scripts.
func isSedPrintScript(script string) bool {
	core, ok := strings.CutSuffix(script, "p")
	if !ok || core == "" {
		return false
	}
	for _, part := range strings.SplitN(core, ",", 3) {
		if part == "" {
			return false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return strings.Count(core, ",") <= 1
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

func hasUnsafeGitOptions(args []string) bool {
	// -p/--paginate spawn a pager only in the global-option position; after
	// the subcommand -p means patch output (git log -p).
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			break
		}
		if arg == "-p" || arg == "--paginate" {
			return true
		}
	}

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
	fields := strings.Fields(strings.TrimSpace(command))

	// Subshell, group, negation, and control-flow tokens must not mask the
	// command word: segment splitting turns `if x; then rm -rf y; fi` into a
	// segment led by `then`.
	for len(fields) > 0 && (fields[0] == "(" || fields[0] == "{" || fields[0] == "!" || shellKeywords[fields[0]]) {
		fields = fields[1:]
	}
	if len(fields) > 0 {
		fields = append([]string{strings.TrimLeft(fields[0], "({!")}, fields[1:]...)
	}

	if len(fields) == 0 {
		return false
	}

	words, cmd, unresolved := unwrapCommandWords(fields)
	if unresolved {
		return cmd != ""
	}
	if isUnresolvableCommandWord(words[0]) {
		return true
	}
	args := words[1:]

	switch cmd {
	case "sudo", "su", "doas":
		return true
	case "eval":
		return true
	case "trap":
		return IsDangerousCommand(trapAction(strings.Join(args, " ")))
	case "sh", "bash", "zsh", "fish", "dash", "ksh":
		return IsDangerousCommand(extractShellScript(args))
	case "find":
		return findHasDangerousAction(args)
	case "fd":
		return fdHasDangerousExec(args)
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
		return hasRecursiveRemoveArg(args)
	case "tee":
		for _, arg := range args {
			if !strings.HasPrefix(arg, "-") && isProtectedRedirectTarget(arg) {
				return true
			}
		}
		return false
	case "cp", "mv", "install":
		for _, arg := range slices.Backward(args) {
			if strings.HasPrefix(arg, "-") {
				continue
			}
			return isProtectedRedirectTarget(arg)
		}
		return false
	case "git":
		return isDangerousGitCommand(args)
	}

	return false
}

var shellKeywords = map[string]bool{
	"if": true, "then": true, "elif": true, "else": true, "fi": true,
	"while": true, "until": true, "do": true, "done": true, "esac": true,
}

// trapAction returns the trap's action operand: the first quoted string, or
// the first word when unquoted.
func trapAction(rest string) string {
	rest = strings.TrimSpace(rest)
	for strings.HasPrefix(rest, "-") {
		i := strings.IndexByte(rest, ' ')
		if i < 0 {
			return ""
		}
		rest = strings.TrimSpace(rest[i+1:])
	}
	if rest == "" {
		return ""
	}
	if rest[0] == '\'' || rest[0] == '"' {
		if end := strings.IndexByte(rest[1:], rest[0]); end >= 0 {
			return rest[1 : 1+end]
		}
		return rest[1:]
	}
	if before, _, ok := strings.Cut(rest, " "); ok {
		return before
	}
	return rest
}

// isUnresolvableCommandWord flags command words whose target cannot be read
// from the text: a bare variable, or a substitution executed as the command.
// Variable-prefixed paths ($HOME/bin/tool) stay classifiable and are allowed.
func isUnresolvableCommandWord(word string) bool {
	word = strings.Trim(word, `"'`)

	if strings.HasPrefix(word, "$(") || strings.HasPrefix(word, "`") {
		return true
	}
	if !strings.HasPrefix(word, "$") {
		return false
	}

	name := strings.TrimSuffix(strings.TrimPrefix(word[1:], "{"), "}")
	if name == "" {
		return true
	}
	for i, r := range name {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

// extractShellScript returns the script passed to a shell via -c (also in
// clusters like -lc); positional script files are left to normal
// classification.
func extractShellScript(args []string) string {
	for i, arg := range args {
		if arg == "--" || !strings.HasPrefix(arg, "-") {
			return ""
		}
		if strings.ContainsRune(strings.TrimLeft(arg, "-"), 'c') {
			if i+1 < len(args) {
				return trimOuterQuotes(strings.Join(args[i+1:], " "))
			}
			return ""
		}
	}
	return ""
}

// findHasDangerousAction classifies -exec payloads; -delete stays benign like
// a plain non-recursive rm — it is scoped by find's own filters.
func findHasDangerousAction(args []string) bool {
	for i, arg := range args {
		switch arg {
		case "-exec", "-execdir", "-ok", "-okdir":
			var payload []string
			for _, a := range args[i+1:] {
				if trimmed := strings.Trim(a, `"'`); trimmed == ";" || trimmed == `\;` || trimmed == "+" {
					break
				}
				payload = append(payload, a)
			}
			if isDangerousSingleCommand(strings.Join(payload, " ")) {
				return true
			}
		}
	}
	return false
}

func fdHasDangerousExec(args []string) bool {
	for i, arg := range args {
		switch arg {
		case "-x", "--exec", "-X", "--exec-batch":
			return isDangerousSingleCommand(strings.Join(args[i+1:], " "))
		}
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
	}
	return false
}

func isDangerousGitCommand(args []string) bool {
	switch firstNonFlagArg(args) {
	case "clean":
		return true
	case "reset":
		return hasAnyArg(args, "--hard") || hasAnyArgPrefix(args, "--hard=")
	case "checkout":
		return hasAnyArg(args, "-f", "--force")
	case "push":
		return hasAnyArg(args, "--force", "--force-with-lease", "-f") ||
			hasAnyArgPrefix(args, "--force-with-lease=")
	case "branch":
		return hasAnyArg(args, "-D")
	}

	return false
}

func hasRecursiveRemoveArg(args []string) bool {
	for _, arg := range args {
		if arg == "--recursive" || strings.HasPrefix(arg, "--recursive=") {
			return true
		}

		if !strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "--") {
			continue
		}

		flags := strings.TrimLeft(arg, "-")
		if strings.ContainsAny(flags, "rR") {
			return true
		}
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
	}
	return false
}

func isShellInterpreter(command string) bool {
	words := strings.Fields(strings.TrimSpace(command))
	if len(words) == 0 {
		return false
	}
	switch strings.ToLower(filepath.Base(words[0])) {
	case "sh", "bash", "zsh", "fish", "dash", "ksh":
		return true
	}
	return false
}

func hasAnyArg(args []string, values ...string) bool {
	for _, arg := range args {
		if slices.Contains(values, arg) {
			return true
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
