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
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/dap"
	"github.com/adrianliechti/wingman-agent/pkg/language"
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

func newJavaAdapter() javaAdapter {
	return javaAdapter{bundles: discoverJavaDebugBundles()}
}

func (javaAdapter) Language() string { return "Java" }

func (adapter javaAdapter) Descriptor() dap.AdapterDescriptor {
	command := ""
	if len(adapter.bundles) > 0 {
		command = "jdtls"
	}
	return dap.AdapterDescriptor{
		Name:             "java-debug",
		Language:         "Java",
		AdapterID:        "java",
		Command:          command,
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
		Title:         actionLabel(request.Action) + " " + request.Target.Name,
		Summary:       fmt.Sprintf("%s Java main class %s.", actionLabel(request.Action), request.Target.Name),
		ProjectDir:    request.ProjectDir,
		Request:       "launch",
		IO:            dap.IOOutput,
		Configuration: configuration,
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

func discoverJavaDebugBundles() []string {
	var explicit []string
	for _, value := range filepath.SplitList(os.Getenv("WINGMAN_JAVA_DEBUG_BUNDLE")) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if info, err := os.Stat(value); err == nil && !info.IsDir() {
			explicit = append(explicit, filepath.Clean(value))
		}
	}
	if len(explicit) > 0 {
		slices.Sort(explicit)
		return slices.Compact(explicit)
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	patterns := []string{
		filepath.Join(home, ".vscode", "extensions", "vscjava.vscode-java-debug-*", "server", "com.microsoft.java.debug.plugin-*.jar"),
		filepath.Join(home, ".vscode-insiders", "extensions", "vscjava.vscode-java-debug-*", "server", "com.microsoft.java.debug.plugin-*.jar"),
		filepath.Join(home, ".cursor", "extensions", "vscjava.vscode-java-debug-*", "server", "com.microsoft.java.debug.plugin-*.jar"),
		filepath.Join(home, ".windsurf", "extensions", "vscjava.vscode-java-debug-*", "server", "com.microsoft.java.debug.plugin-*.jar"),
		filepath.Join(dataHome, "nvim", "mason", "packages", "java-debug-adapter", "extension", "server", "com.microsoft.java.debug.plugin-*.jar"),
	}
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		patterns = append(patterns,
			filepath.Join(localAppData, "nvim-data", "mason", "packages", "java-debug-adapter", "extension", "server", "com.microsoft.java.debug.plugin-*.jar"),
		)
	}
	var matches []string
	for _, pattern := range patterns {
		values, _ := filepath.Glob(pattern)
		matches = append(matches, values...)
	}
	slices.Sort(matches)
	if len(matches) == 0 {
		return nil
	}
	// Loading multiple installed versions registers the same extension points
	// twice. The newest lexical extension/plugin version is the safe default.
	return []string{matches[len(matches)-1]}
}

// Connector bridges host-created adapters into the language-neutral DAP
// manager. At present only java-debug needs this path.
type Connector struct {
	language *language.Service
}

func NewConnector(service *language.Service) *Connector {
	return &Connector{language: service}
}

func (connector *Connector) ConnectAdapter(ctx context.Context, plan dap.Plan) (io.ReadWriteCloser, error) {
	if connector == nil || connector.language == nil {
		return nil, errors.New("language service is unavailable")
	}
	if !strings.EqualFold(plan.Adapter.Name, "java-debug") {
		return nil, fmt.Errorf("no host connector is registered for %s", plan.Adapter.Name)
	}
	source, err := findJavaSource(plan.ProjectDir, plan.Target)
	if err != nil {
		return nil, err
	}
	result, err := connector.language.ExecuteCommand(ctx, source, nil, lsp.Command{
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
	case int32:
		port = int(typed)
	case int64:
		port = int(typed)
	case json.Number:
		parsed, _ := strconv.Atoi(string(typed))
		port = parsed
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(typed))
		port = parsed
	case map[string]any:
		return javaDebugPort(typed["port"])
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("JDT LS returned an invalid java-debug port %v", value)
	}
	return port, nil
}
