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

type shellAnalysis struct {
	command  string
	syntax   shellSyntax
	empty    bool
	rejected bool
}

func analyzeShellCommand(command string, dialect shellDialect) shellAnalysis {
	original := command
	command = strings.Trim(command, " \t\n")
	if command == "" {
		return shellAnalysis{empty: true}
	}
	if len(original) > maxClassifiableBytes || hasObfuscatingCharacters(original) {
		return shellAnalysis{command: command, rejected: true}
	}

	syntax := parseShellSyntax(command, dialect)
	return shellAnalysis{
		command:  syntax.executableSource,
		syntax:   syntax,
		rejected: syntax.uncertain,
	}
}

func ClassifyEffect(args map[string]any) tool.Effect {
	if args == nil {
		return tool.EffectDynamic
	}

	command, _ := args["command"].(string)
	dialect := platformShellDialect()
	analysis := analyzeShellCommand(command, dialect)
	if isDangerousShellAnalysis(analysis, dialect) {
		return tool.EffectDangerous
	}
	if isReadOnlyShellAnalysis(analysis) {
		return tool.EffectReadOnly
	}

	return tool.EffectMutates
}

func IsDangerousCommand(command string) bool {
	return isDangerousCommandDialect(command, platformShellDialect())
}

func isDangerousCommandDialect(command string, dialect shellDialect) bool {
	return isDangerousShellAnalysis(analyzeShellCommand(command, dialect), dialect)
}

func isDangerousShellAnalysis(analysis shellAnalysis, dialect shellDialect) bool {
	if analysis.empty {
		return false
	}
	if analysis.rejected {
		return true
	}
	command := analysis.command
	syntax := analysis.syntax
	// Tree-sitter blanks literal heredoc data while retaining line boundaries
	// and executable expansions from unquoted bodies.
	statements := syntax.statements
	if hasVulnerableWindowsPath(command) || hasDangerousZshSyntax(command) ||
		hasDangerousBashParameterExpansion(command) || hasDynamicArithmeticEvaluation(command, statements) ||
		hasPowerShellStatePoisoning(command) || syntax.definesFunction || hasShellSessionPoisoning(statements) ||
		hasSensitiveEnvironmentRead(statements) || hasSensitiveCredentialRead(statements) ||
		hasAmbiguousUnicodeWhitespace(command) || hasUnquotedBraceExpansion(command) ||
		hasBackslashEscapedWhitespace(command) || hasExpansionHiddenRecursiveCommand(command) {
		return true
	}
	if decoded := decodePowerShellBackticks(command); decoded != command && isDangerousPowerShellScript(decoded) {
		return true
	}
	if syntax.protectedWrite || syntax.downloadToShell {
		return true
	}

	for _, statement := range statements {
		if isDangerousSingleCommandDialect(statement, dialect) {
			return true
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

// hasVulnerableWindowsPath detects network and NT-namespace spellings before
// shell parsing can erase their backslashes. Merely reading one of these paths
// on Windows may initiate SMB/WebDAV authentication and leak credentials.
func hasVulnerableWindowsPath(command string) bool {
	lower := strings.ToLower(command)
	for _, marker := range []string{
		`\??\`, `\\?\`, `\\.\`, `//?/`, `//./`, `davwwwroot`,
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	if strings.Contains(lower, "@ssl@") || strings.Contains(lower, "@ssl\\") || strings.Contains(lower, "@ssl/") {
		return true
	}

	for i := 0; i+2 < len(command); i++ {
		if command[i] == '\\' && command[i+1] == '\\' && windowsUNCShareFollows(command, i+2) {
			return true
		}
		if command[i] == '/' && command[i+1] == '/' && (i == 0 || command[i-1] != ':') && windowsUNCShareFollows(command, i+2) {
			return true
		}
	}
	return false
}

func windowsUNCShareFollows(command string, start int) bool {
	i := start
	for i < len(command) && command[i] != '\\' && command[i] != '/' &&
		command[i] != ' ' && command[i] != '\t' && command[i] != '\n' && command[i] != '\r' &&
		command[i] != '\'' && command[i] != '"' {
		i++
	}
	// Require both a host and a following share separator. This avoids treating
	// common quoted regular expressions such as '\\d+' as network paths.
	return i > start && i < len(command) && (command[i] == '\\' || command[i] == '/')
}

// These constructs are parsed and executed by zsh but are either invalid or
// have different boundaries in sh/bash-oriented parsers. The local classifier
// cannot safely inspect their payload, so they require explicit approval.
func hasDangerousZshSyntax(command string) bool {
	lower := strings.ToLower(textOutsideSingleQuotes(command))
	for _, marker := range []string{"~[", "(e:", "(+", "${("} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	if strings.Contains(lower, "always") && strings.Contains(lower, "}") && strings.Contains(lower, "{") {
		return true
	}

	for i := 0; i+1 < len(lower); i++ {
		if lower[i] != '=' {
			continue
		}
		atWordStart := i == 0 || strings.ContainsRune(" \t\n;&|(", rune(lower[i-1]))
		if lower[i+1] == '(' && atWordStart {
			return true
		}
		if isCommandNameStart(lower[i+1]) && atWordStart {
			return true
		}
	}
	return false
}

// ${name@P} expands name as Bash prompt text. Prompt expansion performs
// command substitution, so a value assembled as "$" + "(payload)" executes
// even though the original command never contains a literal $(payload).
func hasDangerousBashParameterExpansion(command string) bool {
	inSingle := false
	inDouble := false
	escaped := false

	for i := 0; i+1 < len(command); i++ {
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
		if inSingle || ch != '$' || command[i+1] != '{' {
			continue
		}

		content, end, ok := readParameterExpansion(command, i+2)
		if !ok {
			return true
		}
		if strings.HasSuffix(strings.TrimSpace(content), "@P") || hasDangerousBashParameterExpansion(content) {
			return true
		}
		i = end
	}
	return false
}

func readParameterExpansion(command string, start int) (string, int, bool) {
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
		if ch == '$' && i+1 < len(command) && command[i+1] == '{' {
			depth++
			i++
			continue
		}
		if ch == '}' {
			depth--
			if depth == 0 {
				return command[start:i], i, true
			}
		}
	}
	return "", 0, false
}

func isCommandNameStart(ch byte) bool {
	return ch == '_' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

func hasPowerShellStatePoisoning(command string) bool {
	return strings.Contains(strings.ToLower(command), "$psdefaultparametervalues")
}

// isProtectedRedirectTarget flags redirect destinations whose overwrite is
// destructive or leads to later command execution: devices, system config,
// and shell/git startup files.
func isProtectedRedirectTarget(path string) bool {
	candidates := []string{path}
	if words, ok := splitShellWords(path); ok && len(words) == 1 {
		candidates = append(candidates, words[0])
	}
	for _, candidate := range candidates {
		candidate = strings.ReplaceAll(candidate, `"`, "")
		candidate = strings.ReplaceAll(candidate, `'`, "")
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		candidate = strings.ReplaceAll(candidate, `\`, "/")
		if isProtectedNormalizedPath(candidate) {
			return true
		}
	}
	return false
}

func isProtectedNormalizedPath(path string) bool {
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
	return isReadOnlyCommandDialect(command, platformShellDialect())
}

func isReadOnlyCommandDialect(command string, dialect shellDialect) bool {
	return isReadOnlyShellAnalysis(analyzeShellCommand(command, dialect))
}

func isReadOnlyShellAnalysis(analysis shellAnalysis) bool {
	if analysis.empty || analysis.rejected {
		return false
	}
	syntax := analysis.syntax
	if syntax.hasSubstitution || syntax.hasRedirection || len(syntax.statements) == 0 {
		return false
	}
	if hasMutationSyntax(analysis.command) {
		return false
	}

	for _, statement := range syntax.statements {
		if !isSingleCommandReadOnly(statement) {
			return false
		}
	}

	return true
}

func hasMutationSyntax(command string) bool {
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

// splitShellWords performs the shell's quote-removal and simple backslash
// processing without expanding variables or globs. strings.Fields is unsafe
// here: it sees r\m, r""m, and r\<newline>m as different command names even
// though a POSIX shell executes all three as rm.
func splitShellWords(command string) ([]string, bool) {
	var words []string
	var current strings.Builder
	inSingle := false
	inDouble := false
	started := false

	flush := func() {
		if started {
			words = append(words, current.String())
			current.Reset()
			started = false
		}
	}

	for i := 0; i < len(command); i++ {
		ch := command[i]

		if !inSingle && (ch == '$' || ch == '<' || ch == '>') && i+1 < len(command) && command[i+1] == '(' {
			_, end, substitutionOK := readParenSubstitution(command, i+2)
			if !substitutionOK {
				return nil, false
			}
			current.WriteString(command[i : end+1])
			started = true
			i = end
			continue
		}
		if !inSingle && ch == '`' {
			_, end, substitutionOK := readBacktickSubstitution(command, i+1)
			if !substitutionOK {
				return nil, false
			}
			current.WriteString(command[i : end+1])
			started = true
			i = end
			continue
		}

		if ch == '\\' && !inSingle {
			if i+1 >= len(command) {
				return nil, false
			}
			next := command[i+1]
			if next == '\n' {
				i++
				continue
			}
			if inDouble && !strings.ContainsRune("$`\"\\", rune(next)) {
				current.WriteByte(ch)
				started = true
				continue
			}
			current.WriteByte(next)
			started = true
			i++
			continue
		}

		if ch == '\'' && !inDouble {
			if !inSingle && i > 0 && command[i-1] == '$' {
				return nil, false
			}
			inSingle = !inSingle
			started = true
			continue
		}
		if ch == '"' && !inSingle {
			if !inDouble && i > 0 && command[i-1] == '$' {
				return nil, false
			}
			inDouble = !inDouble
			started = true
			continue
		}

		if !inSingle && !inDouble {
			if ch == ' ' || ch == '\t' || ch == '\n' {
				flush()
				continue
			}
			if ch == '#' && !started {
				break
			}
		}

		current.WriteByte(ch)
		started = true
	}

	if inSingle || inDouble {
		return nil, false
	}
	flush()
	return words, true
}

var commandRunners = map[string]bool{
	"env":       true,
	"exec":      true,
	"xargs":     true,
	"timeout":   true,
	"nice":      true,
	"command":   true,
	"builtin":   true,
	"noglob":    true,
	"nocorrect": true,
	"time":      true,
	"nohup":     true,
	"stdbuf":    true,
	"setsid":    true,
	"ionice":    true,
	"taskset":   true,
	"setarch":   true,
}

func unwrapCommandWords(words []string) (resolved []string, cmd string, unresolved bool) {
	for {

		for len(words) > 0 && isEnvAssignment(words[0]) {
			words = words[1:]
		}
		if len(words) == 0 {
			return nil, "", true
		}

		base := commandBase(words[0])

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

func commandBase(name string) string {
	name = strings.Trim(name, `"'`)
	name = strings.ReplaceAll(name, `\`, "/")
	base := strings.ToLower(filepath.Base(name))
	if trimmed, ok := strings.CutSuffix(base, ".exe"); ok {
		base = trimmed
	}
	return base
}

func hasExecutablePath(name string) bool {
	name = strings.Trim(name, `"'`)
	return strings.ContainsAny(name, `/\`) || filepath.VolumeName(name) != ""
}

func skipRunnerFlags(runner string, args []string) []string {
	for len(args) > 0 {
		arg := args[0]
		if !strings.HasPrefix(arg, "-") {
			break
		}

		if arg == "--" {
			args = args[1:]
			break
		}

		switch runner {
		case "exec":
			if arg == "-a" || arg == "--argv0" {
				if len(args) < 2 {
					return nil
				}
				args = args[2:]
				continue
			}
		case "timeout":

			if arg == "-s" || arg == "--signal" || arg == "-k" || arg == "--kill-after" {
				if len(args) < 2 {
					return nil
				}
				args = args[2:]
				continue
			}
		case "nice", "ionice":
			if arg == "-n" || arg == "--adjustment" || arg == "-c" || arg == "-p" {
				if len(args) < 2 {
					return nil
				}
				args = args[2:]
				continue
			}
		case "env":
			// env -S re-tokenizes its operand into a new argv. A textual
			// classifier cannot safely reconstruct both BSD and GNU variants.
			if arg == "-S" || arg == "--split-string" || strings.HasPrefix(arg, "-S") || strings.HasPrefix(arg, "--split-string=") {
				return nil
			}
			if arg == "-u" || arg == "--unset" {
				if len(args) < 2 {
					return nil
				}
				args = args[2:]
				continue
			}
			if arg == "-C" || arg == "--chdir" || arg == "-P" || arg == "-a" || arg == "--argv0" {
				if len(args) < 2 {
					return nil
				}
				args = args[2:]
				continue
			}
		case "time":
			if arg == "-f" || arg == "--format" || arg == "-o" || arg == "--output" {
				if len(args) < 2 {
					return nil
				}
				args = args[2:]
				continue
			}
		case "stdbuf":
			if arg == "-i" || arg == "-o" || arg == "-e" {
				if len(args) < 2 {
					return nil
				}
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
				"--max-chars", "--arg-file", "--delimiter", "--eof", "--process-slot-var":
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
	if runner == "taskset" && len(args) > 0 {
		// taskset consumes a CPU mask/list before the command.
		args = args[1:]
	}
	if runner == "setarch" && len(args) > 1 && isArchitectureName(args[0]) {
		args = args[1:]
	}
	return args
}

func isArchitectureName(value string) bool {
	switch strings.ToLower(value) {
	case "linux32", "linux64", "i386", "i486", "i586", "i686", "x86_64", "amd64", "arm", "arm64", "aarch64", "ppc", "ppc64", "ppc64le", "s390", "s390x", "riscv64":
		return true
	}
	return false
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

	fields, ok := splitShellWords(command)
	if !ok || len(fields) == 0 {
		return false
	}
	if hasUnquotedDynamicFlag(command) || hasUnquotedBraceExpansion(command) {
		return false
	}
	if hasUnsafeReadOnlyEnvironment(fields) {
		return false
	}
	if !isEnvAssignment(fields[0]) && hasExecutablePath(fields[0]) {
		return false
	}

	words, cmd, unresolved := unwrapCommandWords(fields)
	if unresolved {
		return cmd == ""
	}
	if len(words) == 0 || hasExecutablePath(words[0]) {
		return false
	}

	subs, ok := normalizedReadOnlyCommands[cmd]
	if !ok {
		return false
	}

	args := words[1:]
	if commandHasRuntimeArguments(args) && commandNeedsStaticArguments(cmd) {
		return false
	}
	if hasPowerShellVariableWritingParameter(args) {
		return false
	}
	if hasPowerShellScriptBlockArgument(cmd, args) {
		return false
	}

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
			if arg == "-i" || arg == "--in-place" || arg == "--inplace" ||
				arg == "-f" || strings.HasPrefix(arg, "-f") || arg == "--from-file" || strings.HasPrefix(arg, "--from-file=") ||
				arg == "--argfile" || strings.HasPrefix(arg, "--argfile=") ||
				arg == "--rawfile" || strings.HasPrefix(arg, "--rawfile=") ||
				arg == "--slurpfile" || strings.HasPrefix(arg, "--slurpfile=") ||
				arg == "-L" || strings.HasPrefix(arg, "-L") || arg == "--library-path" || strings.HasPrefix(arg, "--library-path=") {
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
	case "less":
		for _, arg := range args {
			if arg == "-o" || arg == "-O" || strings.HasPrefix(arg, "-o") || strings.HasPrefix(arg, "-O") ||
				arg == "--log-file" || strings.HasPrefix(arg, "--log-file=") ||
				arg == "--LOG-FILE" || strings.HasPrefix(arg, "--LOG-FILE=") {
				return false
			}
		}
	case "tree":
		for _, arg := range args {
			if arg == "-o" || strings.HasPrefix(arg, "-o") || arg == "--output" || strings.HasPrefix(arg, "--output=") ||
				arg == "-H" || strings.HasPrefix(arg, "-H") || arg == "--html" || strings.HasPrefix(arg, "--html=") || arg == "-R" {
				return false
			}
		}
	case "printf":
		for _, arg := range args {
			if arg == "-v" || strings.HasPrefix(arg, "-v") || arg == "--var" || strings.HasPrefix(arg, "--var=") {
				return false
			}
		}
	case "set":
		if len(args) != 0 {
			return false
		}
	case "node", "python", "python3":
		// These are allowlisted only for a single version/help flag. Node, in
		// particular, processes --run even when -v appears first.
		if len(args) != 1 {
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

var safeReadOnlyEnvironment = map[string]bool{
	"GOEXPERIMENT": true, "GOOS": true, "GOARCH": true, "CGO_ENABLED": true, "GO111MODULE": true,
	"RUST_BACKTRACE": true, "RUST_LOG": true, "NODE_ENV": true,
	"PYTHONUNBUFFERED": true, "PYTHONDONTWRITEBYTECODE": true,
	"PYTEST_DISABLE_PLUGIN_AUTOLOAD": true, "PYTEST_DEBUG": true,
	"LANG": true, "LANGUAGE": true, "LC_ALL": true, "LC_CTYPE": true, "LC_TIME": true, "CHARSET": true,
	"TERM": true, "COLORTERM": true, "NO_COLOR": true, "FORCE_COLOR": true, "TZ": true,
	"LS_COLORS": true, "LSCOLORS": true, "GREP_COLOR": true, "GREP_COLORS": true, "GCC_COLORS": true,
	"TIME_STYLE": true, "BLOCK_SIZE": true, "BLOCKSIZE": true,
}

func hasUnsafeReadOnlyEnvironment(words []string) bool {
	i := 0
	for i < len(words) && isEnvAssignment(words[i]) {
		i++
	}
	if i == len(words) {
		for _, word := range words {
			if environmentAssignmentCanHijack(word) {
				return true
			}
		}
		return false
	}
	for _, word := range words[:i] {
		if !safeEnvironmentAssignment(word) {
			return true
		}
	}
	if i >= len(words) || commandBase(words[i]) != "env" {
		return false
	}

	for j := i + 1; j < len(words); j++ {
		word := words[j]
		if strings.HasPrefix(word, "-") {
			if (word == "-u" || word == "--unset") && j+1 < len(words) {
				j++
			}
			continue
		}
		if !isEnvAssignment(word) {
			break
		}
		if !safeEnvironmentAssignment(word) {
			return true
		}
	}
	return false
}

func environmentAssignmentCanHijack(word string) bool {
	name, _, _ := strings.Cut(word, "=")
	name = strings.ToUpper(name)
	if name == "PATH" || name == "SHELL" || name == "ENV" || name == "BASH_ENV" || name == "CDPATH" || name == "IFS" ||
		name == "PYTHONPATH" || name == "NODE_PATH" || name == "NODE_OPTIONS" || name == "RUBYLIB" || name == "RUBYOPT" ||
		name == "CLASSPATH" || name == "PERL5LIB" || name == "PERL5OPT" || name == "GOFLAGS" || name == "RUSTFLAGS" ||
		name == "HOME" || name == "TMPDIR" || name == "PAGER" || name == "MANPAGER" {
		return true
	}
	return strings.HasPrefix(name, "LD_") || strings.HasPrefix(name, "DYLD_") || strings.HasPrefix(name, "GIT_CONFIG_")
}

func safeEnvironmentAssignment(word string) bool {
	name, _, _ := strings.Cut(word, "=")
	return safeReadOnlyEnvironment[strings.ToUpper(name)]
}

func commandHasRuntimeArguments(args []string) bool {
	for _, arg := range args {
		if strings.Contains(arg, "$") {
			return true
		}
	}
	return false
}

func commandNeedsStaticArguments(cmd string) bool {
	switch cmd {
	case "find", "fd", "sort", "jq", "yq", "xq", "rg", "sed", "base64", "date", "xxd", "file", "man", "help", "docker", "docker-compose", "git", "ps", "uniq":
		return true
	}
	return false
}

func hasPowerShellVariableWritingParameter(args []string) bool {
	for _, arg := range args {
		name, _, _ := strings.Cut(strings.ToLower(strings.Trim(arg, `"'`)), ":")
		if name == "-ov" || name == "-pv" ||
			(len(name) >= len("-outv") && strings.HasPrefix("-outvariable", name)) ||
			(len(name) >= len("-pipelinev") && strings.HasPrefix("-pipelinevariable", name)) {
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
		name, _, _ := strings.Cut(arg, "=")
		if len(name) >= 4 {
			for _, unsafe := range []string{
				"--config-env", "--exec-path", "--git-dir", "--namespace", "--super-prefix", "--work-tree",
				"--output", "--upload-pack", "--ext-diff", "--textconv", "--exec", "--paginate",
			} {
				// Git accepts unambiguous long-option abbreviations. Treat a prefix
				// exactly like the full write/exec option.
				if strings.HasPrefix(unsafe, name) {
					return true
				}
			}
		}
		switch arg {
		case "-C", "-c", "--config-env", "--exec-path", "--git-dir", "--namespace", "--super-prefix", "--work-tree",
			"--output", "--upload-pack", "--ext-diff", "--textconv", "--exec", "--paginate":
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
			strings.HasPrefix(arg, "--upload-pack=") ||
			strings.HasPrefix(arg, "--exec=") {
			return true
		}
	}

	return false
}

func isDangerousSingleCommand(command string) bool {
	return isDangerousSingleCommandDialect(command, platformShellDialect())
}

func isDangerousSingleCommandDialect(command string, dialect shellDialect) bool {
	fields, ok := splitShellWords(strings.TrimSpace(command))
	if !ok {
		return true
	}

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
	if hasPowerShellDefaultParameterWrite(args) {
		return true
	}

	switch cmd {
	case "sudo", "su", "doas", "pkexec", "run0", "gosu":
		return true
	case "eval":
		return true
	case "let":
		for _, expression := range args {
			if !isStaticArithmetic(expression) {
				return true
			}
		}
		return false
	case "trap":
		return isDangerousCommandDialect(trapActionArgs(args), dialectBash)
	case "sh", "bash", "zsh", "fish", "dash", "ksh":
		return isDangerousCommandDialect(extractShellScript(args), dialectBash)
	case "zmodload", "emulate", "sysopen", "sysread", "syswrite", "sysseek", "zpty", "ztcp", "zsocket", "mapfile",
		"zf_rm", "zf_mv", "zf_ln", "zf_chmod", "zf_chown", "zf_mkdir", "zf_rmdir", "zf_chgrp":
		return true
	case "fc":
		for _, arg := range args {
			if strings.HasPrefix(arg, "-") && strings.ContainsRune(strings.TrimLeft(arg, "-"), 'e') {
				return true
			}
		}
		return false
	case "find":
		return findHasDangerousAction(args)
	case "fd":
		return fdHasDangerousExec(args)
	case "dd", "mkfs", "mount", "umount", "diskutil", "launchctl", "systemctl", "service":
		return true
	case "powershell", "pwsh":
		return isDangerousPowerShellInvocation(args)
	case "cmd":
		script := decodeCmdCarets(extractCmdScript(args))
		return hasCmdDynamicExpansion(script) || isDangerousCommandDialect(script, dialect)
	case "remove-item", "ri":
		return hasPowerShellForceOrRecursive(args)
	case "stop-process":
		return slices.ContainsFunc(args, func(arg string) bool { return powerShellParameterAbbreviates(arg, "-force", 1) })
	case "invoke-expression", "iex", "set-executionpolicy", "new-service", "sc", "reg":
		return true
	case "set-alias", "sal", "new-alias", "nal", "set-variable", "sv", "new-variable", "nv",
		"import-module", "ipmo", "install-module", "save-module", "invoke-wmimethod", "iwmi", "invoke-cimmethod":
		return true
	case "del", "erase":
		return hasAnyArgFold(args, "/f") || hasPowerShellForceOrRecursive(args)
	case "rd", "rmdir":
		return hasAnyArgFold(args, "/s") || hasPowerShellForceOrRecursive(args)
	case "start-process", "saps", "invoke-item", "ii", "start", "explorer", "explorer.exe", "mshta", "mshta.exe":
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

func trapActionArgs(args []string) string {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		args = args[1:]
	}
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func hasPowerShellDefaultParameterWrite(args []string) bool {
	for i, arg := range args {
		lower := strings.ToLower(strings.Trim(arg, `"'`))
		name, attached, hasAttached := strings.Cut(lower, ":")
		isWriter := name == "-ov" || name == "-pv" ||
			(len(name) >= len("-outv") && strings.HasPrefix("-outvariable", name)) ||
			(len(name) >= len("-pipelinev") && strings.HasPrefix("-pipelinevariable", name))
		if !isWriter {
			continue
		}
		target := attached
		if !hasAttached && i+1 < len(args) {
			target = strings.ToLower(strings.Trim(args[i+1], `"'`))
		}
		target = strings.TrimPrefix(target, "$")
		if colon := strings.LastIndexByte(target, ':'); colon >= 0 {
			target = target[colon+1:]
		}
		if target == "psdefaultparametervalues" {
			return true
		}
	}
	return false
}

var shellKeywords = map[string]bool{
	"if": true, "then": true, "elif": true, "else": true, "fi": true,
	"while": true, "until": true, "do": true, "done": true, "esac": true,
}

// isUnresolvableCommandWord flags command words whose target cannot be read
// from the text: a bare variable, or a substitution executed as the command.
// Variable-prefixed paths ($HOME/bin/tool) stay classifiable and are allowed.
func isUnresolvableCommandWord(word string) bool {
	word = strings.Trim(word, `"'`)

	if strings.HasPrefix(word, "$(") || strings.HasPrefix(word, "`") {
		return true
	}
	if strings.Contains(word, "`") {
		return true
	}
	if !strings.Contains(word, "$") {
		return false
	}
	if !strings.HasPrefix(word, "$") {
		return true
	}
	if variablePrefixedExecutablePath(word) {
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

func variablePrefixedExecutablePath(word string) bool {
	if strings.HasPrefix(word, "${") {
		end := strings.IndexByte(word, '}')
		return end > 2 && end+1 < len(word) && (word[end+1] == '/' || word[end+1] == '\\')
	}
	end := 1
	for end < len(word) {
		ch := word[end]
		if ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || end > 1 && ch >= '0' && ch <= '9' {
			end++
			continue
		}
		break
	}
	return end > 1 && end < len(word) && (word[end] == '/' || word[end] == '\\')
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
				return trimOuterQuotes(args[i+1])
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
		for _, prefix := range []string{"--exec=", "--exec-batch="} {
			if payload, ok := strings.CutPrefix(arg, prefix); ok {
				return isDangerousSingleCommand(strings.TrimSpace(payload + " " + strings.Join(args[i+1:], " ")))
			}
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
		name, _, _ := strings.Cut(strings.ToLower(strings.Trim(arg, `"'`)), ":")
		if name == "-e" || name == "-ec" || name == "-f" || name == "/file" || name == "-ep" ||
			(len(name) >= 3 && strings.HasPrefix("-encodedcommand", name)) ||
			(len(name) >= 3 && strings.HasPrefix("-executionpolicy", name)) {
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
	return isDangerousCommandDialect(decodePowerShellBackticks(script), dialectPowerShell)
}

func hasPowerShellForceOrRecursive(args []string) bool {
	for _, arg := range args {
		if argumentIsDynamic(arg) {
			return true
		}
		if powerShellParameterAbbreviates(arg, "-force", 2) ||
			powerShellParameterAbbreviates(arg, "-recurse", 1) ||
			powerShellParameterAbbreviates(arg, "-recursive", 3) {
			return true
		}
	}
	return false
}

func argumentIsDynamic(arg string) bool {
	return strings.ContainsAny(arg, "$`")
}

func hasDynamicArgument(args []string) bool {
	return slices.ContainsFunc(args, argumentIsDynamic)
}

func isPowerShellDownloadCommand(command string) bool {
	words, ok := splitShellWords(strings.TrimSpace(command))
	if !ok || len(words) == 0 {
		return false
	}
	words[0] = strings.Trim(words[0], `(){}[]`)
	_, cmd, unresolved := unwrapCommandWords(words)
	if unresolved {
		return false
	}
	switch cmd {
	case "invoke-webrequest", "iwr", "curl", "wget":
		return true
	}
	return false
}

func isDangerousGitCommand(args []string) bool {
	if gitGlobalOptionRunsCommand(args) {
		return true
	}

	subcommand := firstNonFlagArg(args)
	if argumentIsDynamic(subcommand) {
		return true
	}
	switch subcommand {
	case "clean":
		return true
	case "reset":
		return hasDynamicArgument(args) || hasAnyArg(args, "--hard") || hasAnyArgPrefix(args, "--hard=")
	case "checkout":
		return hasDynamicArgument(args) || hasAnyArg(args, "-f", "--force")
	case "push":
		return hasDynamicArgument(args) || hasAnyArg(args, "--force", "--force-with-lease", "-f") ||
			hasAnyArgPrefix(args, "--force-with-lease=")
	case "branch":
		return hasDynamicArgument(args) || hasAnyArg(args, "-D")
	case "rebase":
		// -x/--exec runs an arbitrary command for every rewritten commit.
		return hasAnyArg(args, "-x", "--exec") || hasAnyArgPrefix(args, "--exec=")
	case "filter-branch":
		// Every filter (--tree-filter, --index-filter, --commit-filter, ...)
		// is an arbitrary shell command run over the history.
		return true
	case "bisect":
		return firstNonFlagWord(gitOperands(args)) == "run"
	case "submodule":
		return firstNonFlagWord(gitOperands(args)) == "foreach"
	}

	return false
}

// gitOperands returns the arguments that follow the git subcommand, skipping
// the global-option region (including options such as -C that consume a value).
func gitOperands(args []string) []string {
	skipNext := false
	for i, arg := range args {
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
		return args[i+1:]
	}
	return nil
}

func hasRecursiveRemoveArg(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if argumentIsDynamic(arg) {
			return true
		}
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

func gitGlobalOptionRunsCommand(args []string) bool {
	skipNext := false
	for _, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if !strings.HasPrefix(arg, "-") {
			return false
		}
		name, _, _ := strings.Cut(arg, "=")
		if name == "-c" {
			return true
		}
		if len(name) >= 4 && (strings.HasPrefix("--config-env", name) || strings.HasPrefix("--exec-path", name)) {
			return true
		}
		switch name {
		case "-C", "--git-dir", "--namespace", "--super-prefix", "--work-tree":
			skipNext = true
		}
	}
	return false
}

func isDownloadCommand(command string) bool {
	words, ok := splitShellWords(strings.TrimSpace(command))
	if !ok || len(words) == 0 {
		return false
	}
	_, cmd, unresolved := unwrapCommandWords(words)
	if unresolved {
		return false
	}
	switch cmd {
	case "curl", "wget":
		return true
	}
	return false
}

func isShellInterpreter(command string) bool {
	words, ok := splitShellWords(strings.TrimSpace(command))
	if !ok || len(words) == 0 {
		return false
	}
	_, cmd, unresolved := unwrapCommandWords(words)
	if unresolved {
		return false
	}
	switch cmd {
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
