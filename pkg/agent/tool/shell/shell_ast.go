package shell

import (
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

type shellDialect uint8

const (
	dialectBash shellDialect = iota
	dialectPowerShell
)

type shellStatement struct {
	text       string
	start, end uint32
}

type shellSyntax struct {
	executableSource string
	statements       []string
	hasSubstitution  bool
	hasRedirection   bool
	definesFunction  bool
	protectedWrite   bool
	downloadToShell  bool
	uncertain        bool
}

var (
	bashLanguageOnce sync.Once
	bashLanguage     *gotreesitter.Language
	bashParsers      sync.Pool

	powerShellLanguageOnce sync.Once
	powerShellLanguage     *gotreesitter.Language
	powerShellParsers      sync.Pool
)

func platformShellDialect() shellDialect {
	if runtime.GOOS == "windows" {
		return dialectPowerShell
	}
	return dialectBash
}

func languageForDialect(dialect shellDialect) *gotreesitter.Language {
	switch dialect {
	case dialectPowerShell:
		powerShellLanguageOnce.Do(func() { powerShellLanguage = grammars.PowershellLanguage() })
		return powerShellLanguage
	default:
		bashLanguageOnce.Do(func() { bashLanguage = grammars.BashLanguage() })
		return bashLanguage
	}
}

func parserPoolForDialect(dialect shellDialect) *sync.Pool {
	if dialect == dialectPowerShell {
		return &powerShellParsers
	}
	return &bashParsers
}

func parseShellSyntax(source string, dialect shellDialect) (result shellSyntax) {
	result.executableSource = source
	if strings.TrimSpace(source) == "" {
		return result
	}
	defer func() {
		if recover() != nil {
			result = shellSyntax{executableSource: source, statements: []string{source}, uncertain: true}
		}
	}()

	lang := languageForDialect(dialect)
	pool := parserPoolForDialect(dialect)
	parser, _ := pool.Get().(*gotreesitter.Parser)
	if parser == nil {
		parser = gotreesitter.NewParser(lang)
	}
	parserReusable := false
	defer func() {
		if parserReusable {
			pool.Put(parser)
		}
	}()

	sourceBytes := []byte(source)
	tree, err := parser.Parse(sourceBytes)
	parserReusable = true
	if err != nil || tree == nil || tree.RootNode() == nil {
		result.statements = []string{source}
		result.uncertain = true
		return result
	}
	defer tree.Release()
	result.uncertain = tree.RootNode().HasErrorOrMissing() && !shellParseErrorsHandled(tree.RootNode(), lang, sourceBytes)

	executable := slices.Clone(sourceBytes)
	var statements []shellStatement
	sanitizeHeredocBodies(tree.RootNode(), lang, sourceBytes, executable, false, &result, &statements)
	result.executableSource = string(executable)

	walkShellAST(tree.RootNode(), lang, executable, dialect, &result, &statements)
	for _, tail := range recoverMalformedHeredocTails(tree.RootNode(), lang, sourceBytes) {
		tailSyntax := parseShellSyntax(tail.text, dialect)
		result.hasSubstitution = result.hasSubstitution || tailSyntax.hasSubstitution
		result.hasRedirection = result.hasRedirection || tailSyntax.hasRedirection
		result.definesFunction = result.definesFunction || tailSyntax.definesFunction
		result.protectedWrite = result.protectedWrite || tailSyntax.protectedWrite
		result.downloadToShell = result.downloadToShell || tailSyntax.downloadToShell
		result.uncertain = result.uncertain || tailSyntax.uncertain
		for _, statement := range tailSyntax.statements {
			statements = append(statements, shellStatement{text: statement, start: tail.start, end: tail.end})
		}
	}
	slices.SortStableFunc(statements, func(a, b shellStatement) int {
		if a.start != b.start {
			return int(a.start) - int(b.start)
		}
		return int(b.end) - int(a.end)
	})
	for _, statement := range statements {
		text := strings.TrimSpace(statement.text)
		if text == "" || slices.Contains(result.statements, text) {
			continue
		}
		result.statements = append(result.statements, text)
	}
	if len(result.statements) == 0 && strings.TrimSpace(source) != "" && tree.RootNode().HasErrorOrMissing() {
		result.statements = []string{source}
		result.uncertain = true
	}
	return result
}

func walkShellAST(node *gotreesitter.Node, lang *gotreesitter.Language, source []byte, dialect shellDialect, result *shellSyntax, statements *[]shellStatement) {
	typeName := node.Type(lang)
	switch typeName {
	case "command", "declaration_command", "unset_command", "redirected_statement":
		*statements = append(*statements, shellStatement{text: node.Text(source), start: node.StartByte(), end: node.EndByte()})
		if typeName == "command" && commandContainsDownloadSubstitution(node, lang, source) {
			result.downloadToShell = true
		}
	case "variable_assignment":
		if parent := node.Parent(); parent == nil || parent.Type(lang) != "command" {
			*statements = append(*statements, shellStatement{text: node.Text(source), start: node.StartByte(), end: node.EndByte()})
		}
	case "test_command":
		*statements = append(*statements, shellStatement{text: node.Text(source), start: node.StartByte(), end: node.EndByte()})
	case "function_definition", "function_statement":
		result.definesFunction = true
	case "command_substitution", "process_substitution", "sub_expression":
		result.hasSubstitution = true
	case "heredoc_start":
		// The Bash grammar recovers mixed-quoted delimiters under ERROR rather
		// than a heredoc_redirect, but this is still shell redirection.
		result.hasRedirection = true
	case "file_redirect", "herestring_redirect", "heredoc_redirect", "redirection":
		result.hasRedirection = true
		if target := redirectTarget(node, lang, source); target != "" && isProtectedRedirectTarget(target) {
			result.protectedWrite = true
		}
	case "pipeline":
		if directPipelineIsDangerous(node, lang, source, dialect) {
			result.downloadToShell = true
		}
	}

	for _, child := range node.Children() {
		walkShellAST(child, lang, source, dialect, result, statements)
	}
}

func sanitizeHeredocBodies(
	node *gotreesitter.Node,
	lang *gotreesitter.Language,
	source, executable []byte,
	quotedHeredoc bool,
	result *shellSyntax,
	statements *[]shellStatement,
) {
	children := node.Children()
	for _, child := range children {
		switch child.Type(lang) {
		case "heredoc_start":
			quotedHeredoc = strings.ContainsAny(child.Text(source), `'"\`)
			continue
		case "heredoc_body":
			blankRangePreservingLines(executable, child.StartByte(), child.EndByte())
			if quotedHeredoc {
				continue
			}
			for _, expansion := range child.Children() {
				if expansion.Type(lang) == "heredoc_content" {
					continue
				}
				copy(executable[expansion.StartByte():expansion.EndByte()], source[expansion.StartByte():expansion.EndByte()])
			}
			restoreHeredocBackticks(source, executable, child.StartByte(), child.EndByte(), result, statements)
			continue
		}
		sanitizeHeredocBodies(child, lang, source, executable, quotedHeredoc, result, statements)
	}
}

func restoreHeredocBackticks(
	source, executable []byte,
	start, end uint32,
	result *shellSyntax,
	statements *[]shellStatement,
) {
	sourceText := string(source)
	for i := int(start); i < int(end) && i < len(source); i++ {
		if source[i] == '\\' {
			i++
			continue
		}
		if source[i] != '`' {
			continue
		}
		substitution, close, ok := readBacktickSubstitution(sourceText, i+1)
		if !ok || close >= int(end) {
			continue
		}
		copy(executable[i:close+1], source[i:close+1])
		*statements = append(*statements, shellStatement{text: substitution, start: uint32(i + 1), end: uint32(close)})
		result.hasSubstitution = true
		i = close
	}
}

func recoverMalformedHeredocTails(root *gotreesitter.Node, lang *gotreesitter.Language, source []byte) []shellStatement {
	var tails []shellStatement
	var visit func(*gotreesitter.Node)
	visit = func(node *gotreesitter.Node) {
		if node.Type(lang) == "ERROR" {
			children := node.Children()
			for _, child := range children {
				if child.Type(lang) != "heredoc_start" {
					continue
				}
				rawDelimiter := child.Text(source)
				if !strings.ContainsAny(rawDelimiter, `'"\`) {
					continue
				}
				delimiter := unquoteHeredocDelimiter(rawDelimiter)
				if delimiter == "" {
					continue
				}
				if tailStart, found := heredocTailStart(source, child.EndByte(), delimiter); found && tailStart < node.EndByte() {
					tails = append(tails, shellStatement{text: string(source[tailStart:node.EndByte()]), start: tailStart, end: node.EndByte()})
				}
			}
		}
		for _, child := range node.Children() {
			visit(child)
		}
	}
	visit(root)
	return tails
}

func unquoteHeredocDelimiter(value string) string {
	var delimiter strings.Builder
	inSingle := false
	inDouble := false
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
				continue
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
				continue
			}
		case '\\':
			if !inSingle && i+1 < len(value) {
				i++
			}
		}
		delimiter.WriteByte(value[i])
	}
	return delimiter.String()
}

func heredocTailStart(source []byte, start uint32, delimiter string) (uint32, bool) {
	for lineStart := int(start); lineStart < len(source); {
		if source[lineStart] == '\r' || source[lineStart] == '\n' {
			lineStart++
			continue
		}
		lineEnd := lineStart
		for lineEnd < len(source) && source[lineEnd] != '\r' && source[lineEnd] != '\n' {
			lineEnd++
		}
		if strings.TrimLeft(string(source[lineStart:lineEnd]), "\t") == delimiter {
			for lineEnd < len(source) && (source[lineEnd] == '\r' || source[lineEnd] == '\n') {
				lineEnd++
			}
			return uint32(lineEnd), true
		}
		lineStart = lineEnd
	}
	return uint32(len(source)), false
}

func shellParseErrorsHandled(node *gotreesitter.Node, lang *gotreesitter.Language, source []byte) bool {
	if node.IsMissing() {
		return false
	}
	if node.IsError() {
		for _, child := range node.Children() {
			if child.Type(lang) != "heredoc_start" {
				continue
			}
			rawDelimiter := child.Text(source)
			if !strings.ContainsAny(rawDelimiter, `'"\`) {
				continue
			}
			delimiter := unquoteHeredocDelimiter(rawDelimiter)
			if delimiter != "" {
				_, found := heredocTailStart(source, child.EndByte(), delimiter)
				return found
			}
		}
		return false
	}
	for _, child := range node.Children() {
		if !shellParseErrorsHandled(child, lang, source) {
			return false
		}
	}
	return true
}

func blankRangePreservingLines(value []byte, start, end uint32) {
	for i := int(start); i < int(end) && i < len(value); i++ {
		if value[i] != '\n' && value[i] != '\r' {
			value[i] = ' '
		}
	}
}

func redirectTarget(node *gotreesitter.Node, lang *gotreesitter.Language, source []byte) string {
	children := node.Children()
	for i := len(children) - 1; i >= 0; i-- {
		child := children[i]
		typeName := child.Type(lang)
		if !child.IsNamed() || typeName == "heredoc_body" || typeName == "heredoc_end" || typeName == "heredoc_start" {
			continue
		}
		return strings.TrimSpace(child.Text(source))
	}
	return ""
}

func directPipelineIsDangerous(node *gotreesitter.Node, lang *gotreesitter.Language, source []byte, dialect shellDialect) bool {
	var commands []string
	collectDirectPipelineCommands(node, lang, source, node, &commands)
	downloadSeen := false
	for _, command := range commands {
		if dialect == dialectPowerShell {
			words, ok := splitShellWords(command)
			if ok && len(words) > 0 {
				cmd := commandBase(strings.Trim(words[0], `(){}[]`))
				if downloadSeen && (cmd == "invoke-expression" || cmd == "iex") {
					return true
				}
			}
			downloadSeen = downloadSeen || isPowerShellDownloadCommand(command)
			continue
		}
		if downloadSeen && isShellInterpreter(command) {
			return true
		}
		downloadSeen = downloadSeen || isDownloadCommand(command)
	}
	return false
}

func collectDirectPipelineCommands(node *gotreesitter.Node, lang *gotreesitter.Language, source []byte, root *gotreesitter.Node, commands *[]string) {
	if node != root {
		switch node.Type(lang) {
		case "command":
			*commands = append(*commands, node.Text(source))
			return
		case "pipeline":
			return
		}
	}
	for _, child := range node.Children() {
		collectDirectPipelineCommands(child, lang, source, root, commands)
	}
}

func commandContainsDownloadSubstitution(node *gotreesitter.Node, lang *gotreesitter.Language, source []byte) bool {
	words, ok := splitShellWords(strings.TrimSpace(node.Text(source)))
	if !ok || len(words) == 0 {
		return false
	}
	_, cmd, unresolved := unwrapCommandWords(words)
	if unresolved || !slices.Contains([]string{"sh", "bash", "zsh", "fish", "dash", "ksh"}, cmd) {
		return false
	}
	return descendantCommandMatches(node, lang, source, func(command string) bool { return isDownloadCommand(command) })
}

func descendantCommandMatches(node *gotreesitter.Node, lang *gotreesitter.Language, source []byte, match func(string) bool) bool {
	for _, child := range node.Children() {
		if child.Type(lang) == "command" && match(child.Text(source)) {
			return true
		}
		if descendantCommandMatches(child, lang, source, match) {
			return true
		}
	}
	return false
}
