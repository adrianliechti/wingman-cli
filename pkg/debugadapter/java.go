package debugadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/dap"
	"github.com/adrianliechti/wingman-agent/pkg/lsp"
)

var (
	javaPackagePattern = regexp.MustCompile(`(?m)^\s*package\s+([A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*)\s*;`)
	javaTypePattern    = regexp.MustCompile(`\b(?:class|record|enum|interface)\s+([A-Za-z_$][\w$]*)`)
	javaMainPattern    = regexp.MustCompile(`(?s)(?:\b(?:public|protected|private|static|final|synchronized|strictfp)\s+){1,8}void\s+main\s*\(\s*(?:java\s*\.\s*lang\s*\.\s*)?String\s*(?:(?:\[\s*\]|\.\.\.)\s*[A-Za-z_$][\w$]*|[A-Za-z_$][\w$]*\s*\[\s*\])`)
)

type javaAdapter struct {
	bundles []string
}

// ServerInitialization loads the java-debug plug-in into JDT LS; the Java DAP
// endpoint is served by that bundle rather than a standalone executable.
func (adapter javaAdapter) ServerInitialization() (string, any) {
	if len(adapter.bundles) == 0 {
		return "", nil
	}
	return "jdtls", map[string]any{"bundles": slices.Clone(adapter.bundles)}
}

func (javaAdapter) Language() string { return "Java" }

func (adapter javaAdapter) Descriptor() dap.AdapterDescriptor {
	return dap.AdapterDescriptor{
		Name:      "java-debug",
		Language:  "Java",
		AdapterID: "java",
		// The actual adapter starts inside managed JDT LS. This command is
		// an availability token for the managed java-debug bundle.
		Command:          "java-debug-adapter",
		Transport:        dap.TransportConnect,
		TerminalStrategy: dap.TerminalRunInTerminal,
		Markers:          []string{"pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts", ".project", ".classpath"},
		SourceExtensions: []string{".java"},
		Defaults:         map[string]any{"type": "java"},
		ConfigurationPaths: []dap.ConfigurationPath{
			{Key: "cwd", Directory: true},
		},
		IOConfigKey:     "console",
		IOValues:        vscodeIOValues(),
		TargetConfigKey: "mainClass",
	}
}

func managedJavaDebugBundles(root string) []string {
	if root == "" {
		return nil
	}
	directory := filepath.Join(root, "java-debug")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil
	}
	var matches []string
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && strings.HasPrefix(name, "com.microsoft.java.debug.plugin-") && strings.HasSuffix(name, ".jar") {
			matches = append(matches, filepath.Join(directory, name))
		}
	}
	slices.Sort(matches)
	if len(matches) == 0 {
		return nil
	}
	return []string{matches[len(matches)-1]}
}

func (javaAdapter) Matches(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".java")
}

func (javaAdapter) Detect(path string, source []byte) ([]Target, error) {
	masked := maskCStyleSource(source, true, false)
	packageName := ""
	if match := javaPackagePattern.FindSubmatch(masked); len(match) == 2 {
		packageName = string(match[1])
	}
	types := javaTypePattern.FindAllSubmatchIndex(masked, -1)
	if len(types) == 0 {
		return nil, nil
	}
	directory := filepath.ToSlash(filepath.Dir(filepath.FromSlash(path)))
	if directory == "" {
		directory = "."
	}
	seen := make(map[string]bool)
	var targets []Target
	for _, mainMatch := range javaMainPattern.FindAllIndex(masked, -1) {
		signature := masked[mainMatch[0]:mainMatch[1]]
		if !hasWord(signature, "public") || !hasWord(signature, "static") {
			continue
		}
		typeNames := javaEnclosingTypes(masked, types, mainMatch[0])
		if len(typeNames) == 0 {
			continue
		}
		mainClass := strings.Join(typeNames, "$")
		if packageName != "" {
			mainClass = packageName + "." + mainClass
		}
		if seen[mainClass] {
			continue
		}
		seen[mainClass] = true
		mainOffset := mainMatch[0] + strings.Index(string(masked[mainMatch[0]:mainMatch[1]]), "main")
		line, column := sourceLineColumn(source, mainOffset)
		targets = append(targets, Target{
			ID:        fmt.Sprintf("java:%s:main:%s", filepath.ToSlash(path), mainClass),
			Name:      mainClass,
			Detail:    "Java main class",
			Kind:      "main",
			Language:  "Java",
			Path:      filepath.ToSlash(path),
			Directory: directory,
			Line:      line,
			Column:    column,
		})
	}
	return targets, nil
}

func javaEnclosingTypes(source []byte, declarations [][]int, offset int) []string {
	var result []string
	for _, declaration := range declarations {
		if len(declaration) < 4 || declaration[0] >= offset {
			break
		}
		braceOffset := bytes.IndexByte(source[declaration[1]:offset], '{')
		if braceOffset < 0 {
			continue
		}
		depth := 0
		closed := false
		for _, value := range source[declaration[1]+braceOffset : offset] {
			switch value {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					closed = true
				}
			}
			if closed {
				break
			}
		}
		if depth > 0 && !closed {
			result = append(result, string(source[declaration[2]:declaration[3]]))
		}
	}
	return result
}

func (javaAdapter) Plan(request Request) (Plan, error) {
	if request.Target.Kind != "main" {
		return Plan{}, fmt.Errorf("unsupported Java debug target kind %q", request.Target.Kind)
	}
	configuration := map[string]any{
		"mainClass":          request.Target.Name,
		"cwd":                ".",
		"shortenCommandLine": "auto",
	}
	if projectName := javaProjectName(request); projectName != "" {
		configuration["projectName"] = projectName
	}
	plan := Plan{
		Title:            actionLabel(request.Action) + " " + request.Target.Name,
		Summary:          fmt.Sprintf("%s Java main class %s.", actionLabel(request.Action), request.Target.Name),
		ProjectDir:       request.ProjectDir,
		Request:          "launch",
		IO:               dap.IOOutput,
		SupportsTerminal: true,
		Configuration:    configuration,
	}
	if request.Action == "run" {
		configuration["noDebug"] = true
	} else {
		plan.Breakpoints = targetBreakpoint(request.Target)
	}
	return plan, nil
}

func javaProjectName(request Request) string {
	projectDir := request.ProjectDir
	if !filepath.IsAbs(projectDir) && request.WorkspaceDir != "" {
		projectDir = filepath.Join(request.WorkspaceDir, filepath.FromSlash(projectDir))
	}
	if file, err := os.Open(filepath.Join(projectDir, "pom.xml")); err == nil {
		defer file.Close()
		var project struct {
			ArtifactID string `xml:"artifactId"`
		}
		if err := xml.NewDecoder(io.LimitReader(file, maxSourceBytes)).Decode(&project); err == nil {
			if name := strings.TrimSpace(project.ArtifactID); name != "" {
				return name
			}
		}
	}
	pattern := regexp.MustCompile(`(?m)^\s*rootProject\s*\.\s*name\s*=\s*["']([^"']+)["']`)
	for _, name := range []string{"settings.gradle", "settings.gradle.kts"} {
		contents, err := os.ReadFile(filepath.Join(projectDir, name))
		if err != nil {
			continue
		}
		if match := pattern.FindSubmatch(contents); len(match) == 2 {
			return strings.TrimSpace(string(match[1]))
		}
	}
	return ""
}

func hasWord(source []byte, word string) bool {
	pattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(word) + `\b`)
	return pattern.Match(source)
}

// CommandExecutor is the single language-service capability host connectors
// need: running a workspace-scoped LSP command against a source file. The
// composition layer injects the implementation so adapters never depend on
// service wiring.
type CommandExecutor interface {
	ExecuteCommand(ctx context.Context, filePath string, content *string, command lsp.Command) (any, error)
}

// Connector bridges host-created adapters into the language-neutral DAP
// manager. At present only java-debug needs this path: its DAP socket is
// opened by JDT LS rather than a standalone executable.
type Connector struct {
	commands CommandExecutor
}

func NewConnector(commands CommandExecutor) *Connector {
	return &Connector{commands: commands}
}

// PrepareAdapter resolves the Java launch details normally supplied by the
// VS Code Java extension. java-debug requires these values even though its DAP
// endpoint is already hosted inside JDT LS.
func (connector *Connector) PrepareAdapter(ctx context.Context, plan dap.Plan) (dap.Plan, error) {
	if err := connector.validate(plan); err != nil {
		return plan, err
	}
	if !strings.EqualFold(plan.Request, "launch") {
		return plan, nil
	}
	mainClass := strings.TrimSpace(plan.Target)
	if mainClass == "" {
		return plan, errors.New("Java launch requires a mainClass")
	}
	source, err := findJavaSource(plan.ProjectDir, mainClass)
	if err != nil {
		return plan, err
	}
	arguments := maps.Clone(plan.Arguments)
	if arguments == nil {
		arguments = make(map[string]any)
	}
	projectName, _ := arguments["projectName"].(string)
	if !hasJavaConfigurationValue(arguments, "classPaths") && !hasJavaConfigurationValue(arguments, "modulePaths") {
		result, commandErr := connector.execute(ctx, source, "vscode.java.resolveClasspath", mainClass, nullableString(projectName))
		if commandErr != nil {
			return plan, fmt.Errorf("resolve Java classpath through JDT LS: %w", commandErr)
		}
		var paths [][]string
		if err := decodeJavaCommandResult(result, &paths); err != nil || len(paths) != 2 {
			if err == nil {
				err = fmt.Errorf("expected [modulePaths, classPaths], got %d values", len(paths))
			}
			return plan, fmt.Errorf("decode Java classpath from JDT LS: %w", err)
		}
		if len(paths[0]) == 0 && len(paths[1]) == 0 {
			return plan, errors.New("JDT LS returned no Java module paths or class paths")
		}
		arguments["modulePaths"] = paths[0]
		arguments["classPaths"] = paths[1]
	}
	if !hasJavaConfigurationValue(arguments, "javaExec") {
		result, commandErr := connector.execute(ctx, source, "vscode.java.resolveJavaExecutable", mainClass, nullableString(projectName))
		if commandErr != nil {
			return plan, fmt.Errorf("resolve Java executable through JDT LS: %w", commandErr)
		}
		var executable string
		if err := decodeJavaCommandResult(result, &executable); err != nil {
			return plan, fmt.Errorf("decode Java executable from JDT LS: %w", err)
		}
		executable = strings.TrimSpace(executable)
		if executable == "" {
			return plan, errors.New("JDT LS returned no Java executable")
		}
		arguments["javaExec"] = executable
	}
	plan.Arguments = arguments
	return plan, nil
}

func (connector *Connector) ConnectAdapter(ctx context.Context, plan dap.Plan) (io.ReadWriteCloser, error) {
	if err := connector.validate(plan); err != nil {
		return nil, err
	}
	source, err := findJavaSource(plan.ProjectDir, plan.Target)
	if err != nil {
		return nil, err
	}
	result, err := connector.commands.ExecuteCommand(ctx, source, nil, lsp.Command{
		Command: "vscode.java.startDebugSession",
	})
	if err != nil {
		return nil, fmt.Errorf("start java-debug through JDT LS: %w", err)
	}
	port, err := javaDebugPort(result)
	if err != nil {
		return nil, err
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("connect to java-debug on loopback port %d: %w", port, err)
	}
	return connection, nil
}

func (connector *Connector) validate(plan dap.Plan) error {
	if connector == nil || connector.commands == nil {
		return errors.New("language service is unavailable")
	}
	if !strings.EqualFold(plan.Adapter.Name, "java-debug") {
		return fmt.Errorf("no host connector is registered for %s", plan.Adapter.Name)
	}
	return nil
}

func (connector *Connector) execute(ctx context.Context, source, name string, arguments ...any) (any, error) {
	values := make([]lsp.LSPAny, len(arguments))
	for index, argument := range arguments {
		encoded, err := json.Marshal(argument)
		if err != nil {
			return nil, fmt.Errorf("encode argument %d for %s: %w", index, name, err)
		}
		values[index] = encoded
	}
	return connector.commands.ExecuteCommand(ctx, source, nil, lsp.Command{Command: name, Arguments: values})
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func hasJavaConfigurationValue(arguments map[string]any, key string) bool {
	value, exists := arguments[key]
	if !exists || value == nil {
		return false
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) != ""
	case []string:
		return len(typed) > 0
	case []any:
		return len(typed) > 0
	default:
		return true
	}
}

func decodeJavaCommandResult(value any, destination any) error {
	var encoded []byte
	switch typed := value.(type) {
	case []byte:
		encoded = typed
	case string:
		var err error
		encoded, err = json.Marshal(typed)
		if err != nil {
			return err
		}
	case fmt.Stringer:
		encoded = []byte(typed.String())
	default:
		var err error
		encoded, err = json.Marshal(value)
		if err != nil {
			return err
		}
	}
	if len(bytes.TrimSpace(encoded)) == 0 {
		return errors.New("empty command result")
	}
	return json.Unmarshal(encoded, destination)
}

func findJavaSource(projectDir, mainClass string) (string, error) {
	simpleName := mainClass
	if index := strings.LastIndex(simpleName, "."); index >= 0 {
		simpleName = simpleName[index+1:]
	}
	if index := strings.Index(simpleName, "$"); index >= 0 {
		simpleName = simpleName[:index]
	}
	expected := simpleName + ".java"
	var fallback string
	visited := 0
	err := filepath.WalkDir(projectDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			if path != projectDir && skipDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".java") {
			return nil
		}
		visited++
		if visited > defaultMaxFiles {
			return fs.SkipAll
		}
		if fallback == "" {
			fallback = path
		}
		if entry.Name() == expected {
			fallback = path
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("find Java source for JDT LS: %w", err)
	}
	if fallback == "" {
		return "", errors.New("no Java source file is available to start JDT LS")
	}
	return fallback, nil
}

func javaDebugPort(value any) (int, error) {
	port := 0
	switch typed := value.(type) {
	case float64:
		if typed == float64(int(typed)) {
			port = int(typed)
		}
	case float32:
		if typed == float32(int(typed)) {
			port = int(typed)
		}
	case int:
		port = typed
	case int8:
		port = int(typed)
	case int16:
		port = int(typed)
	case int32:
		port = int(typed)
	case int64:
		port = int(typed)
	case uint:
		if uint64(typed) <= uint64(^uint(0)>>1) {
			port = int(typed)
		}
	case uint8:
		port = int(typed)
	case uint16:
		port = int(typed)
	case uint32:
		if uint64(typed) <= uint64(^uint(0)>>1) {
			port = int(typed)
		}
	case uint64:
		if typed <= uint64(^uint(0)>>1) {
			port = int(typed)
		}
	case json.Number:
		parsed, _ := strconv.Atoi(string(typed))
		port = parsed
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(typed))
		port = parsed
	case map[string]any:
		return javaDebugPort(typed["port"])
	case fmt.Stringer:
		parsed, _ := strconv.Atoi(strings.TrimSpace(typed.String()))
		port = parsed
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("JDT LS returned an invalid java-debug port %v (%T)", value, value)
	}
	return port, nil
}
