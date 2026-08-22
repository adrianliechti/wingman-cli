package debugadapter

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/dap"
)

const javascriptLanguage = "JavaScript/TypeScript"

var (
	viteConfigPattern = regexp.MustCompile(`(?i)^vite\.config\.(?:[cm]?[jt]s)$`)
	vitePortPattern   = regexp.MustCompile(`\bport\s*:\s*([0-9]{2,5})\b`)
	jsGuardPatterns   = []*regexp.Regexp{
		regexp.MustCompile(`\brequire\s*\.\s*main\s*={2,3}\s*module\b`),
		regexp.MustCompile(`\bimport\s*\.\s*meta\s*\.\s*main\b`),
		regexp.MustCompile(`\bimport\s*\.\s*meta\s*\.\s*url\b[\s\S]{0,240}\bprocess\s*\.\s*argv\s*\[\s*1\s*]`),
	}
)

type javaScriptAdapter struct {
	command string
	args    []string
}

func newJavaScriptAdapter() javaScriptAdapter {
	if command := strings.TrimSpace(os.Getenv("WINGMAN_JS_DEBUG_ADAPTER")); command != "" {
		return javaScriptAdapter{command: command, args: []string{"0", "127.0.0.1"}}
	}
	if server := explicitJavaScriptDebugServer(); server != "" {
		return javaScriptAdapter{command: "node", args: []string{server, "0", "127.0.0.1"}}
	}
	return javaScriptAdapter{command: "js-debug-adapter", args: []string{"0", "127.0.0.1"}}
}

func (javaScriptAdapter) Language() string { return javascriptLanguage }

func (adapter javaScriptAdapter) Descriptor() dap.AdapterDescriptor {
	return dap.AdapterDescriptor{
		Name:             "vscode-js-debug",
		Language:         javascriptLanguage,
		AdapterID:        "pwa-node",
		Command:          adapter.command,
		Args:             slices.Clone(adapter.args),
		Transport:        dap.TransportTCP,
		ReadyPrefix:      "Debug server listening at ",
		TerminalStrategy: dap.TerminalRunInTerminal,
		Markers: []string{
			"package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lock", "bun.lockb",
			"tsconfig.json", "jsconfig.json", "vite.config.*",
		},
		SourceExtensions: []string{".js", ".cjs", ".mjs", ".jsx", ".ts", ".cts", ".mts", ".tsx"},
		Defaults:         map[string]any{"type": "pwa-node"},
		ConfigurationPaths: []dap.ConfigurationPath{
			{Key: "program"},
			{Key: "cwd", Directory: true},
			{Key: "webRoot", Directory: true},
		},
		IOConfigKey: "console",
		IOValues:    vscodeIOValues(),
	}
}

func (javaScriptAdapter) Matches(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".cjs", ".mjs", ".jsx", ".ts", ".cts", ".mts", ".tsx":
		return true
	default:
		return false
	}
}

func (javaScriptAdapter) Detect(path string, source []byte) ([]Target, error) {
	base := filepath.Base(filepath.FromSlash(path))
	directory := filepath.ToSlash(filepath.Dir(filepath.FromSlash(path)))
	if directory == "" {
		directory = "."
	}
	if viteConfigPattern.MatchString(base) {
		offset := bytes.Index(source, []byte("defineConfig"))
		if offset < 0 {
			offset = firstSourceOffset(source)
		}
		line, column := sourceLineColumn(source, offset)
		return []Target{{
			ID:        fmt.Sprintf("javascript:%s:vite", filepath.ToSlash(path)),
			Name:      "Vite browser",
			Detail:    "React/Vite browser app",
			Kind:      "vite",
			Language:  javascriptLanguage,
			Path:      filepath.ToSlash(path),
			Directory: directory,
			Line:      line,
			Column:    column,
		}}, nil
	}

	masked := maskCStyleSource(source, true, true)
	offset := nodeEntrypointOffset(path, source, masked)
	if offset < 0 {
		return nil, nil
	}
	line, column := sourceLineColumn(source, offset)
	kind := "node"
	detail := "Node.js entry point"
	if isTypeScriptPath(path) {
		detail = "TypeScript/Node.js entry point"
	}
	return []Target{{
		ID:        fmt.Sprintf("javascript:%s:%s", filepath.ToSlash(path), kind),
		Name:      base,
		Detail:    detail,
		Kind:      kind,
		Language:  javascriptLanguage,
		Path:      filepath.ToSlash(path),
		Directory: directory,
		Line:      line,
		Column:    column,
	}}, nil
}

func nodeEntrypointOffset(path string, source, masked []byte) int {
	firstLine := source
	if index := bytes.IndexByte(firstLine, '\n'); index >= 0 {
		firstLine = firstLine[:index]
	}
	if bytes.HasPrefix(firstLine, []byte("#!")) && bytes.Contains(bytes.ToLower(firstLine), []byte("node")) {
		return firstSourceOffset(source[len(firstLine):]) + len(firstLine)
	}
	for _, pattern := range jsGuardPatterns {
		if match := pattern.FindIndex(masked); len(match) > 0 {
			return match[0]
		}
	}
	base := strings.ToLower(strings.TrimSuffix(filepath.Base(filepath.FromSlash(path)), filepath.Ext(path)))
	if base != "main" && base != "server" && base != "cli" && base != "app" {
		return -1
	}
	extension := strings.ToLower(filepath.Ext(path))
	if (extension == ".jsx" || extension == ".tsx") ||
		(bytes.Contains(bytes.ToLower(source), []byte("from 'react'")) ||
			bytes.Contains(bytes.ToLower(source), []byte(`from "react"`)) ||
			bytes.Contains(source, []byte("createRoot("))) {
		return -1
	}
	return firstSourceOffset(source)
}

func firstSourceOffset(source []byte) int {
	for index, value := range source {
		if value != ' ' && value != '\t' && value != '\r' && value != '\n' {
			return index
		}
	}
	return 0
}

func isTypeScriptPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ts", ".cts", ".mts", ".tsx":
		return true
	default:
		return false
	}
}

func (javaScriptAdapter) Plan(request Request) (Plan, error) {
	switch request.Target.Kind {
	case "vite":
		return vitePlan(request)
	case "node":
		return nodePlan(request)
	default:
		return Plan{}, fmt.Errorf("unsupported JavaScript/TypeScript debug target kind %q", request.Target.Kind)
	}
}

func nodePlan(request Request) (Plan, error) {
	program, err := projectPath(request.ProjectDir, request.Target.Path)
	if err != nil {
		return Plan{}, err
	}
	configuration := map[string]any{
		"type":       "pwa-node",
		"program":    program,
		"cwd":        ".",
		"sourceMaps": true,
		"skipFiles":  []string{"<node_internals>/**"},
	}
	if isTypeScriptPath(request.Target.Path) {
		if runtimeExecutable := localTypeScriptRuntime(request); runtimeExecutable != "" {
			configuration["runtimeExecutable"] = runtimeExecutable
		}
	}
	plan := Plan{
		Title:            actionLabel(request.Action) + " " + request.Target.Name,
		Summary:          fmt.Sprintf("%s %s.", actionLabel(request.Action), request.Target.Detail),
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

func vitePlan(request Request) (Plan, error) {
	port := vitePort(request)
	url := "http://localhost:" + strconv.Itoa(port)
	configuration := map[string]any{
		"type":       "pwa-chrome",
		"url":        url,
		"webRoot":    ".",
		"sourceMaps": true,
	}
	if request.Action == "run" {
		configuration["noDebug"] = true
	}
	return Plan{
		Title:         actionLabel(request.Action) + " Vite browser",
		Summary:       fmt.Sprintf("%s the React/Vite browser app at %s; start the Vite dev server first.", actionLabel(request.Action), url),
		ProjectDir:    request.ProjectDir,
		Request:       "launch",
		IO:            dap.IOOutput,
		Configuration: configuration,
	}, nil
}

func vitePort(request Request) int {
	path := request.Target.Path
	if !filepath.IsAbs(path) && request.WorkspaceDir != "" {
		path = filepath.Join(request.WorkspaceDir, filepath.FromSlash(path))
	}
	contents, err := os.ReadFile(path)
	if err == nil {
		masked := maskCStyleSource(contents, true, true)
		if match := vitePortPattern.FindSubmatch(masked); len(match) == 2 {
			if value, err := strconv.Atoi(string(match[1])); err == nil && value >= 1 && value <= 65535 {
				return value
			}
		}
	}
	return 5173
}

func localTypeScriptRuntime(request Request) string {
	projectDir := request.ProjectDir
	if !filepath.IsAbs(projectDir) && request.WorkspaceDir != "" {
		projectDir = filepath.Join(request.WorkspaceDir, filepath.FromSlash(projectDir))
	}
	names := []string{"tsx"}
	if runtime.GOOS == "windows" {
		names = []string{"tsx.cmd", "tsx.exe", "tsx"}
	}
	for _, name := range names {
		path := filepath.Join(projectDir, "node_modules", ".bin", name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}

func explicitJavaScriptDebugServer() string {
	if explicit := strings.TrimSpace(os.Getenv("WINGMAN_JS_DEBUG_SERVER")); explicit != "" {
		if info, err := os.Stat(explicit); err == nil && !info.IsDir() {
			return filepath.Clean(explicit)
		}
	}
	return ""
}
