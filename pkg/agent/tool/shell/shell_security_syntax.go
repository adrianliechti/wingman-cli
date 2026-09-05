package shell

import (
	"strings"
	"unicode"
)

// textOutsideSingleQuotes preserves offsets while blanking syntax that the
// shell treats as literal. Double-quoted parameter expansions remain visible.
func textOutsideSingleQuotes(command string) string {
	out := []byte(command)
	inSingle := false
	inDouble := false
	escaped := false
	for i := 0; i < len(out); i++ {
		ch := out[i]
		if escaped {
			if inSingle {
				out[i] = ' '
			}
			escaped = false
			continue
		}
		if ch == '\\' && !inSingle {
			escaped = true
			continue
		}
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			out[i] = ' '
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if inSingle {
			out[i] = ' '
		}
	}
	return string(out)
}

// hasAmbiguousUnicodeWhitespace reports spacing characters whose visual form
// is easy to confuse with ASCII shell separators. Bash does not consistently
// split on these characters, and parsers implemented in other languages often
// do. Quoted occurrences are inert data and are deliberately ignored.
func hasAmbiguousUnicodeWhitespace(command string) bool {
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
		if !inSingle && !inDouble && r != ' ' && r != '\t' && r != '\n' && unicode.IsSpace(r) {
			return true
		}
	}
	return false
}

// hasBackslashEscapedWhitespace catches a parser differential where a shell
// keeps an escaped space inside one executable path while a secondary parser
// may split it into an allowlisted command plus harmless-looking arguments.
func hasBackslashEscapedWhitespace(command string) bool {
	inSingle := false
	inDouble := false

	for i := 0; i < len(command); i++ {
		switch command[i] {
		case '\\':
			if inSingle || i+1 >= len(command) {
				continue
			}
			if !inDouble && (command[i+1] == ' ' || command[i+1] == '\t') {
				return true
			}
			i++
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		}
	}
	return false
}

// Arithmetic expressions recursively resolve variable values in Bash. Array
// subscripts in those values are expansion contexts, so an apparently literal
// assignment can defer a command substitution until $((name)) or $[name].
// Numeric-only expressions are kept quiet.
func hasDynamicArithmeticEvaluation(command string, statements []string) bool {
	if hasDynamicParameterArithmetic(command) || hasDynamicArrayTargetEvaluation(statements) ||
		hasDynamicDoubleBracketArithmetic(statements) || hasIntegerVariableArithmetic(statements) {
		return true
	}

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
		if inSingle || ch != '$' || i+1 >= len(command) {
			if !inSingle && !inDouble && ch == '(' && i+1 < len(command) && command[i+1] == '(' {
				content, end, ok := readArithmeticExpansion(command, i+2)
				if !ok || !isStaticArithmetic(content) {
					return true
				}
				i = end
			}
			continue
		}

		switch {
		case strings.HasPrefix(command[i:], "$(("):
			content, end, ok := readArithmeticExpansion(command, i+3)
			if !ok || !isStaticArithmetic(content) {
				return true
			}
			i = end
		case strings.HasPrefix(command[i:], "$["):
			end := findUnescapedByte(command, i+2, ']')
			if end < 0 || !isStaticArithmetic(command[i+2:end]) {
				return true
			}
			i = end
		}
	}
	return false
}

// Bash evaluates array subscripts and parameter-expansion offsets as
// arithmetic expressions. Names inside those expressions are recursively
// resolved, so a value such as a[$(payload)0] can execute later even when the
// original assignment was single-quoted.
func hasDynamicParameterArithmetic(command string) bool {
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
		if !ok || parameterExpansionHasDynamicArithmetic(content) {
			return true
		}
		i = end
	}
	return false
}

func parameterExpansionHasDynamicArithmetic(content string) bool {
	if content == "" {
		return true
	}
	if hasDynamicParameterArithmetic(content) {
		return true
	}
	if content[0] == '!' && len(content) > 1 {
		// Indirect expansion treats the referenced value as another parameter
		// name; an array subscript in that value is evaluated arithmetically.
		return true
	}
	i := 0
	if content[i] == '!' || content[i] == '#' {
		i++
		if i == len(content) {
			return false
		}
	}
	if isShellVariableSpecial(content[i]) {
		i++
	} else {
		start := i
		for i < len(content) && isShellVariableByte(content[i], i-start) {
			i++
		}
		if i == start {
			return false
		}
	}

	if i < len(content) && content[i] == '[' {
		end := matchingBracket(content, i)
		if end < 0 {
			return true
		}
		subscript := strings.TrimSpace(content[i+1 : end])
		if subscript != "@" && subscript != "*" && !isStaticArithmetic(subscript) {
			return true
		}
		i = end + 1
	}

	rest := content[i:]
	if !strings.HasPrefix(rest, ":") {
		return false
	}
	if len(rest) > 1 && strings.ContainsRune("-=+?", rune(rest[1])) {
		// :-, :=, :+, and :? are value/default operators, not substring
		// arithmetic.
		return false
	}
	return !isStaticArithmetic(rest[1:])
}

func isShellVariableSpecial(ch byte) bool {
	return ch >= '0' && ch <= '9' || strings.ContainsRune("*@#?$!-", rune(ch))
}

func isShellVariableByte(ch byte, offset int) bool {
	return ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || offset > 0 && ch >= '0' && ch <= '9'
}

func matchingBracket(value string, start int) int {
	depth := 0
	escaped := false
	for i := start; i < len(value); i++ {
		if escaped {
			escaped = false
			continue
		}
		if value[i] == '\\' {
			escaped = true
			continue
		}
		switch value[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func hasDynamicArrayTargetEvaluation(statements []string) bool {
	for _, segment := range statements {
		words, ok := splitShellWords(strings.TrimSpace(segment))
		if !ok {
			continue
		}
		for _, word := range words {
			if eq := strings.IndexByte(word, '='); eq > 0 {
				target := strings.TrimSuffix(word[:eq], "+")
				if arrayReferenceHasDynamicArithmetic(target) || compoundAssignmentHasDynamicSubscript(word[eq+1:]) {
					return true
				}
			}
		}
		if len(words) == 0 {
			continue
		}
		resolved, cmd, unresolved := unwrapCommandWords(words)
		if unresolved || len(resolved) < 2 {
			continue
		}
		args := resolved[1:]
		switch cmd {
		case "integer":
			return true
		case "declare", "typeset", "local":
			for _, arg := range args {
				if arg == "--integer" || strings.HasPrefix(arg, "-") && strings.Contains(strings.TrimLeft(arg, "-"), "i") {
					return true
				}
			}
			fallthrough
		case "unset", "read", "readonly", "export":
			for _, arg := range args {
				if !strings.HasPrefix(arg, "-") && variableTargetArgumentIsDynamic(arg) {
					return true
				}
			}
		case "printf":
			for i, arg := range args {
				if (arg == "-v" || arg == "--var") && i+1 < len(args) &&
					(argumentIsDynamic(args[i+1]) || arrayReferenceHasDynamicArithmetic(args[i+1])) {
					return true
				}
			}
		}
	}
	return false
}

// Bash and zsh give these variables the integer attribute, so an assignment
// is evaluated as arithmetic immediately: a single-quoted `a[$(payload)]`
// value expands and `1/0` aborts the line. Plain integer literals stay quiet.
var integerShellVariables = map[string]bool{
	"OPTIND": true, "OPTERR": true, "RANDOM": true, "SECONDS": true, "LINENO": true, "SHLVL": true, "HISTCMD": true,
	"TMOUT": true, "COLUMNS": true, "LINES": true, "HISTSIZE": true, "HISTFILESIZE": true, "SAVEHIST": true, "MAILCHECK": true,
	"REPORTTIME": true, "REPORTMEMORY": true, "DIRSTACKSIZE": true, "KEYTIMEOUT": true, "LISTMAX": true, "PERIOD": true,
	"LOGCHECK": true, "BAUD": true, "UID": true, "EUID": true, "GID": true, "EGID": true, "PPID": true,
}

func hasIntegerVariableArithmetic(statements []string) bool {
	for _, segment := range statements {
		words, ok := splitShellWords(strings.TrimSpace(segment))
		if !ok || len(words) == 0 {
			continue
		}
		for _, word := range words {
			if !isEnvAssignment(word) {
				break
			}
			if integerAssignmentIsDynamic(word) {
				return true
			}
		}
		resolved, cmd, unresolved := unwrapCommandWords(words)
		if unresolved || len(resolved) < 2 {
			continue
		}
		switch cmd {
		case "export", "readonly", "declare", "typeset", "local":
			for _, arg := range resolved[1:] {
				if !strings.HasPrefix(arg, "-") && integerAssignmentIsDynamic(arg) {
					return true
				}
			}
		}
	}
	return false
}

func integerAssignmentIsDynamic(word string) bool {
	eq := strings.IndexByte(word, '=')
	if eq <= 0 {
		return false
	}
	name := strings.TrimSuffix(word[:eq], "+")
	if bracket := strings.IndexByte(name, '['); bracket >= 0 {
		name = name[:bracket]
	}
	return integerShellVariables[name] && !isIntegerLiteral(word[eq+1:])
}

func isIntegerLiteral(value string) bool {
	value = strings.Trim(value, `"'`)
	if value == "" {
		return true
	}
	if value[0] == '+' || value[0] == '-' {
		value = value[1:]
	}
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func variableTargetArgumentIsDynamic(arg string) bool {
	target := arg
	if eq := strings.IndexByte(target, '='); eq >= 0 {
		target = strings.TrimSuffix(target[:eq], "+")
	}
	return argumentIsDynamic(target) || arrayReferenceHasDynamicArithmetic(target)
}

func arrayReferenceHasDynamicArithmetic(value string) bool {
	open := strings.IndexByte(value, '[')
	if open < 0 {
		return false
	}
	name := value[:open]
	if name != "" {
		for i := 0; i < len(name); i++ {
			if !isShellVariableByte(name[i], i) {
				return false
			}
		}
	}
	close := matchingBracket(value, open)
	if close < 0 || close != len(value)-1 {
		return false
	}
	subscript := strings.TrimSpace(value[open+1 : close])
	return subscript != "@" && subscript != "*" && !isStaticArithmetic(subscript)
}

func compoundAssignmentHasDynamicSubscript(value string) bool {
	if !strings.HasPrefix(value, "(") {
		return false
	}
	for i := 1; i < len(value); i++ {
		if value[i] != '[' {
			continue
		}
		end := matchingBracket(value, i)
		if end < 0 {
			return true
		}
		if end+1 < len(value) && value[end+1] == '=' {
			subscript := strings.TrimSpace(value[i+1 : end])
			if subscript != "@" && subscript != "*" && !isStaticArithmetic(subscript) {
				return true
			}
		}
		i = end
	}
	return false
}

func hasDynamicDoubleBracketArithmetic(statements []string) bool {
	for _, segment := range statements {
		words, ok := splitShellWords(strings.TrimSpace(segment))
		if !ok || len(words) < 4 {
			continue
		}
		for len(words) > 0 && (words[0] == "(" || words[0] == "!" || shellKeywords[words[0]]) {
			words = words[1:]
		}
		if len(words) < 4 || strings.TrimLeft(words[0], "(!") != "[[" {
			continue
		}
		for i, word := range words {
			switch word {
			case "-eq", "-ne", "-lt", "-le", "-gt", "-ge":
				if i == 0 || i+1 >= len(words) || !isStaticArithmetic(strings.Trim(words[i-1], `"'`)) || !isStaticArithmetic(strings.Trim(words[i+1], `"'`)) {
					return true
				}
			}
		}
	}
	return false
}

func readArithmeticExpansion(command string, start int) (string, int, bool) {
	depth := 1
	for i := start; i+1 < len(command); i++ {
		if command[i] == '\\' {
			i++
			continue
		}
		if command[i] == '(' {
			depth++
			continue
		}
		if command[i] == ')' {
			if depth > 1 {
				depth--
				continue
			}
			if command[i+1] == ')' {
				return command[start:i], i + 1, true
			}
		}
	}
	return "", 0, false
}

func isStaticArithmetic(expression string) bool {
	if strings.TrimSpace(expression) == "" {
		return false
	}
	for _, r := range expression {
		if r >= '0' && r <= '9' || unicode.IsSpace(r) || strings.ContainsRune("+-*/%<>=!&|^~?:(),", r) {
			continue
		}
		return false
	}
	return true
}

func findUnescapedByte(value string, start int, want byte) int {
	for i := start; i < len(value); i++ {
		if value[i] == '\\' {
			i++
			continue
		}
		if value[i] == want {
			return i
		}
	}
	return -1
}

// hasUnquotedBraceExpansion detects Bash's comma and sequence expansion forms.
// A quoted brace is literal. This intentionally fails closed on nested forms;
// bounded expansion belongs in the future AST-based classifier.
func hasUnquotedBraceExpansion(command string) bool {
	if hasQuotedBraceInsideUnquotedBrace(command) {
		return true
	}

	inSingle := false
	inDouble := false
	escaped := false
	start := -1
	depth := 0
	hasSeparator := false

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
		if inSingle || inDouble {
			continue
		}
		switch ch {
		case '{':
			if depth == 0 {
				start = i
				hasSeparator = false
			}
			depth++
		case ',':
			if depth == 1 {
				hasSeparator = true
			}
		case '.':
			if depth == 1 && i+1 < len(command) && command[i+1] == '.' {
				hasSeparator = true
			}
		case '}':
			if depth == 0 {
				continue
			}
			depth--
			if depth == 0 && start >= 0 && hasSeparator {
				return true
			}
		}
	}
	return false
}

// Bash's brace matcher can continue past a quoted brace that simpler scanners
// count as a structural close. For example, {@'{'0},--output=x} expands into
// two arguments even though quote-stripped brace counts suggest otherwise.
func hasQuotedBraceInsideUnquotedBrace(command string) bool {
	depth := 0
	inSingle := false
	inDouble := false
	escaped := false
	quotedBrace := false

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
		if inSingle || inDouble {
			if depth > 0 && (r == '{' || r == '}') {
				quotedBrace = true
			}
			continue
		}
		switch r {
		case '{':
			depth++
		case '}':
			if depth > 0 {
				depth--
				if depth == 0 && quotedBrace {
					return true
				}
			}
		}
	}
	return depth > 0 && quotedBrace
}

func firstWordHasUnquotedExpansion(command string) bool {
	inSingle := false
	inDouble := false
	escaped := false
	started := false

	for i := 0; i < len(command); i++ {
		ch := command[i]
		if escaped {
			escaped = false
			started = true
			continue
		}
		if ch == '\\' && !inSingle {
			escaped = true
			started = true
			continue
		}
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			started = true
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			started = true
			continue
		}
		if !inSingle && !inDouble && (ch == ' ' || ch == '\t' || ch == '\n') {
			if started {
				return false
			}
			continue
		}
		if !inSingle && !inDouble && strings.ContainsRune("*?[", rune(ch)) {
			return true
		}
		if !inSingle && !inDouble && ch == '{' {
			end := strings.IndexByte(command[i+1:], '}')
			if end >= 0 {
				body := command[i+1 : i+1+end]
				if strings.Contains(body, ",") || strings.Contains(body, "..") {
					return true
				}
			}
		}
		started = true
	}
	return false
}

func hasUnquotedGlobExpansion(command string) bool {
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
		if !inSingle && !inDouble && strings.ContainsRune("*?[", rune(ch)) {
			return true
		}
	}
	return false
}

func hasExpansionHiddenRecursiveCommand(command string) bool {
	if !firstWordHasUnquotedExpansion(command) && !hasUnquotedBraceExpansion(command) && !hasUnquotedGlobExpansion(command) {
		return false
	}
	words, ok := splitShellWords(command)
	if !ok || len(words) < 2 {
		return false
	}
	resolved, _, unresolved := unwrapCommandWords(words)
	if unresolved || len(resolved) < 2 || !strings.ContainsAny(resolved[0], "*?[{") {
		return false
	}
	return hasRecursiveRemoveArg(resolved[1:]) || hasAnyArgFold(resolved[1:], "/s")
}

func hasUnquotedDynamicFlag(command string) bool {
	inSingle := false
	inDouble := false
	escaped := false
	wordStart := true
	flag := false
	dynamic := false

	flush := func() bool {
		matched := flag && dynamic
		wordStart = true
		flag = false
		dynamic = false
		return matched
	}

	for i := 0; i < len(command); i++ {
		ch := command[i]
		if escaped {
			escaped = false
			wordStart = false
			continue
		}
		if ch == '\\' && !inSingle {
			escaped = true
			continue
		}
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			wordStart = false
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			wordStart = false
			continue
		}
		if !inSingle && !inDouble && (ch == ' ' || ch == '\t' || ch == '\n') {
			if flush() {
				return true
			}
			continue
		}
		if wordStart {
			flag = ch == '-'
			wordStart = false
		}
		if !inSingle && (ch == '$' || (!inDouble && strings.ContainsRune("*?[", rune(ch)))) {
			dynamic = true
		}
	}
	return flag && dynamic
}

var dangerousSessionAssignments = map[string]bool{
	"BASH_ENV": true, "ENV": true, "ZDOTDIR": true,
	"PROMPT_COMMAND": true, "PS0": true, "PS1": true, "PS2": true, "PS4": true,
	"LD_PRELOAD": true, "LD_AUDIT": true, "DYLD_INSERT_LIBRARIES": true, "DYLD_LIBRARY_PATH": true,
	"NODE_OPTIONS": true, "PYTHONSTARTUP": true, "PYTHONPATH": true, "RUBYOPT": true, "RUBYLIB": true,
	"PERL5OPT": true, "PERL5LIB": true, "CLASSPATH": true,
	"PAGER": true, "MANPAGER": true, "GIT_PAGER": true, "GIT_EDITOR": true, "GIT_SEQUENCE_EDITOR": true,
	"GIT_EXTERNAL_DIFF": true, "GIT_SSH": true, "GIT_SSH_COMMAND": true, "GIT_ASKPASS": true, "SSH_ASKPASS": true,
	"LESSOPEN": true, "LESSCLOSE": true,
}

func dangerousSessionAssignment(word string) bool {
	eq := strings.IndexByte(word, '=')
	if eq <= 0 {
		return false
	}
	name := word[:eq]
	if bracket := strings.IndexByte(name, '['); bracket >= 0 {
		name = name[:bracket]
	}
	return dangerousSessionAssignments[strings.ToUpper(name)]
}

func wordsDefineShellFunction(words []string) bool {
	if len(words) == 0 {
		return false
	}
	first := strings.TrimSuffix(words[0], "{")
	if strings.HasSuffix(first, "()") && len(first) > 2 {
		return true
	}
	return len(words) > 1 && words[1] == "()"
}

func hasShellSessionPoisoning(statements []string) bool {
	for _, segment := range statements {
		words, ok := splitShellWords(strings.TrimSpace(segment))
		if !ok || len(words) == 0 {
			continue
		}
		if wordsDefineShellFunction(words) {
			return true
		}

		for _, word := range words {
			if !isEnvAssignment(word) {
				break
			}
			if dangerousSessionAssignment(word) {
				return true
			}
		}
		if commandBase(words[0]) == "env" {
			for i := 1; i < len(words); i++ {
				word := words[i]
				if word == "-u" || word == "--unset" {
					i++
					continue
				}
				if strings.HasPrefix(word, "-") {
					continue
				}
				if !isEnvAssignment(word) {
					break
				}
				if dangerousSessionAssignment(word) {
					return true
				}
			}
		}

		resolved, cmd, unresolved := unwrapCommandWords(words)
		if unresolved || len(resolved) == 0 {
			continue
		}
		args := resolved[1:]
		switch cmd {
		case "alias":
			for _, arg := range args {
				if strings.Contains(arg, "=") {
					return true
				}
			}
		case "unalias":
			return len(args) > 0
		case "hash":
			return hasAnyArg(args, "-p")
		case "enable":
			return hasAnyArg(args, "-f") || hasAnyArgPrefix(args, "--file=")
		case "function", "filter":
			return len(args) > 0
		case "export", "readonly", "declare", "typeset", "local":
			for _, arg := range args {
				if dangerousSessionAssignment(arg) {
					return true
				}
			}
		case "printf":
			for i, arg := range args {
				if (arg == "-v" || arg == "--var") && i+1 < len(args) && dangerousSessionAssignments[strings.ToUpper(args[i+1])] {
					return true
				}
			}
		case "bind":
			return hasAnyArg(args, "-x", "--shell-command") || hasAnyArgPrefix(args, "--shell-command=")
		case "complete", "compgen":
			return hasAnyArg(args, "-C", "-F") || hasAnyArg(args, "--command", "--function") ||
				hasAnyArgPrefix(args, "--command=", "--function=")
		case "register-engineevent", "register-objectevent", "register-wmievent", "register-scheduledjob", "set-psreadlinekeyhandler":
			return true
		case "set-item", "new-item":
			for _, arg := range args {
				lower := strings.ToLower(arg)
				if strings.Contains(lower, "function:") || strings.Contains(lower, "alias:") {
					return true
				}
			}
		}
	}
	return false
}

func hasSensitiveEnvironmentRead(statements []string) bool {
	for _, segment := range statements {
		words, ok := splitShellWords(strings.TrimSpace(segment))
		if !ok || len(words) == 0 {
			continue
		}
		resolved, cmd, unresolved := unwrapCommandWords(words)
		if unresolved || len(resolved) < 2 {
			continue
		}
		if cmd == "powershell" || cmd == "pwsh" {
			nested := parseShellSyntax(extractPowerShellScript(resolved[1:]), dialectPowerShell)
			if nested.uncertain || hasSensitiveEnvironmentRead(nested.statements) {
				return true
			}
			continue
		}
		if !sensitiveContentReaders[cmd] {
			continue
		}
		normalizedSegment := strings.ToLower(strings.ReplaceAll(segment, `\`, "/"))
		if strings.Contains(normalizedSegment, "/windows/system32/config/sam") ||
			strings.Contains(normalizedSegment, "/windows/system32/config/security") {
			return true
		}
		for _, arg := range resolved[1:] {
			path := strings.ToLower(strings.ReplaceAll(strings.Trim(arg, `"'`), `\`, "/"))
			if strings.HasPrefix(path, "/proc/") && strings.HasSuffix(path, "/environ") {
				return true
			}
		}
	}
	return false
}

// sensitiveContentReaders are commands whose normal output can disclose file
// contents. Metadata-only tools such as stat and file are deliberately absent.
// This is a direct-spelling guard, not a substitute for filesystem isolation:
// symlinks and dynamically assembled paths cannot be decided from source text.
var sensitiveContentReaders = map[string]bool{
	"cat": true, "head": true, "tail": true, "less": true, "more": true, "bat": true,
	"grep": true, "egrep": true, "fgrep": true, "rg": true, "findstr": true,
	"strings": true, "xxd": true, "od": true, "hexdump": true, "base64": true,
	"cut": true, "sort": true, "uniq": true, "tr": true, "diff": true, "comm": true,
	"join": true, "column": true, "jq": true, "yq": true, "xq": true, "sed": true,
	"nl": true, "paste": true, "rev": true, "tac": true,
	"get-content": true, "gc": true, "select-string": true, "sls": true, "type": true,
}

// hasSensitiveCredentialRead makes common host credential stores an explicit
// approval boundary when a content-reading command names them directly. Files
// inside the workspace remain governed by the workspace's trust model; these
// patterns target conventional per-user and system credential locations.
func hasSensitiveCredentialRead(statements []string) bool {
	for _, segment := range statements {
		words, ok := splitShellWords(strings.TrimSpace(segment))
		if !ok || len(words) == 0 {
			continue
		}
		resolved, cmd, unresolved := unwrapCommandWords(words)
		if unresolved || len(resolved) < 2 {
			continue
		}

		if cmd == "powershell" || cmd == "pwsh" {
			nested := parseShellSyntax(extractPowerShellScript(resolved[1:]), dialectPowerShell)
			if nested.uncertain || hasSensitiveCredentialRead(nested.statements) {
				return true
			}
			continue
		}
		if !sensitiveContentReaders[cmd] {
			continue
		}

		// Check both quote-removed arguments and the original spelling. The
		// latter preserves Windows separators and paths containing spaces; the
		// former catches quote-fragmented spellings such as .s''sh.
		if isSensitiveCredentialPath(segment) {
			return true
		}
		for _, arg := range resolved[1:] {
			if isSensitiveCredentialPath(arg) {
				return true
			}
		}
	}
	return false
}

func isSensitiveCredentialPath(value string) bool {
	path := strings.ToLower(strings.TrimSpace(value))
	path = strings.ReplaceAll(path, `\`, "/")
	path = strings.ReplaceAll(path, "//", "/")

	for _, exact := range []string{
		"/etc/shadow", "/etc/gshadow", "/etc/security/opasswd", "/private/etc/master.passwd",
	} {
		if path == exact || strings.Contains(path, " "+exact) || strings.Contains(path, "="+exact) {
			return true
		}
	}

	for _, marker := range []string{
		"/.aws/credentials", "/.aws/config",
		"/.config/gcloud/application_default_credentials.json",
		"/.config/gcloud/legacy_credentials/",
		"/.azure/accesstokens.json", "/.azure/msal_token_cache",
		"/.kube/config", "/.docker/config.json",
		"/.config/gh/hosts.yml", "/.config/glab-cli/config.yml",
		"/.gnupg/private-keys-v1.d/",
		"/.netrc", "/_netrc", "/.pypirc", "/.git-credentials",
	} {
		if strings.Contains(path, marker) {
			return true
		}
	}

	// Default SSH identity names are private unless explicitly suffixed .pub.
	if strings.Contains(path, "/.ssh/id_") && !strings.Contains(path, ".pub") {
		return true
	}
	return false
}

func hasPowerShellScriptBlockArgument(cmd string, args []string) bool {
	switch cmd {
	case "foreach-object", "where-object", "select-object", "sort-object", "group-object", "measure-object", "format-list", "format-table", "select-string":
	default:
		return false
	}
	for _, arg := range args {
		if strings.Contains(arg, "{") || strings.Contains(arg, "}") {
			return true
		}
	}
	return false
}

func powerShellParameterAbbreviates(arg, full string, minLetters int) bool {
	name, _, _ := strings.Cut(strings.ToLower(strings.Trim(arg, `"'`)), ":")
	full = strings.ToLower(full)
	return len(name) >= minLetters+1 && strings.HasPrefix(full, name)
}

func decodeCmdCarets(script string) string {
	var out strings.Builder
	for i := 0; i < len(script); i++ {
		if script[i] == '^' && i+1 < len(script) {
			i++
		}
		out.WriteByte(script[i])
	}
	return out.String()
}

func hasCmdDynamicExpansion(script string) bool {
	for _, delimiter := range []byte{'%', '!'} {
		start := strings.IndexByte(script, delimiter)
		if start >= 0 && strings.IndexByte(script[start+1:], delimiter) >= 0 {
			return true
		}
	}
	return false
}

// PowerShell's grave accent escapes the following character (and joins a
// physical line when it precedes a newline). Single-quoted strings keep it
// literal. Normalizing it here exposes cmdlet and parameter names such as
// Rem`ove-Item and -Re`curse to the deterministic policy.
func decodePowerShellBackticks(script string) string {
	var out strings.Builder
	inSingle := false
	for i := 0; i < len(script); i++ {
		if script[i] == '\'' {
			inSingle = !inSingle
			out.WriteByte(script[i])
			continue
		}
		if script[i] != '`' || inSingle || i+1 >= len(script) {
			out.WriteByte(script[i])
			continue
		}
		i++
		if script[i] == '\r' && i+1 < len(script) && script[i+1] == '\n' {
			i++
			continue
		}
		if script[i] == '\n' {
			continue
		}
		out.WriteByte(script[i])
	}
	return out.String()
}
