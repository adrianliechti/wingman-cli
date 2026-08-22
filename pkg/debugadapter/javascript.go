package debugadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
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
	viteConfigPattern  = regexp.MustCompile(`(?i)^vite\.config\.(?:[cm]?[jt]s)$`)
	vitePortPattern    = regexp.MustCompile(`\bport\s*:\s*([0-9]{2,5})\b`)
	viteCLIPortPattern = regexp.MustCompile(`(?:^|\s)--port(?:=|\s+)([0-9]{2,5})(?:\s|$)`)
	jsGuardPatterns    = []*regexp.Regexp{
		regexp.MustCompile(`\brequire\s*\.\s*main\s*={2,3}\s*module\b`),
		regexp.MustCompile(`\bimport\s*\.\s*meta\s*\.\s*main\b`),
		regexp.MustCompile(`\bimport\s*\.\s*meta\s*\.\s*url\b[\s\S]{0,240}\bprocess\s*\.\s*argv\s*\[\s*1\s*]`),
	}
)

type nodePackage struct {
	PackageManager string            `json:"packageManager"`
	Scripts        map[string]string `json:"scripts"`
}

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
	if strings.EqualFold(filepath.Base(filepath.FromSlash(path)), "package.json") {
		return true
	}
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
	if strings.EqualFold(base, "package.json") {
		return packageScriptTargets(path, directory, source)
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

func packageScriptTargets(path, directory string, source []byte) ([]Target, error) {
	var manifest nodePackage
	if err := json.Unmarshal(source, &manifest); err != nil {
		// package.json is commonly incomplete while it is being edited. Target
		// discovery should recover on the next change instead of failing the
		// entire workspace scan.
		return nil, nil
	}
	scriptsOffset := bytes.Index(source, []byte(`"scripts"`))
	if scriptsOffset < 0 {
		return nil, nil
	}
	targets := make(map[string]struct{ kind, detail string }, len(manifest.Scripts))
	for name, command := range manifest.Scripts {
		switch {
		case isViteDevelopmentScript(command):
			targets[name] = struct{ kind, detail string }{"browser-script", "Vite development script"}
		case isNodePackageScript(command):
			targets[name] = struct{ kind, detail string }{"node-script", "Node.js package script"}
		}
	}
	names := make([]string, 0, len(targets))
	for name := range targets {
		names = append(names, name)
	}
	slices.Sort(names)
	result := make([]Target, 0, len(names))
	for _, name := range names {
		offset := bytes.Index(source[scriptsOffset:], []byte(strconv.Quote(name)))
		if offset < 0 {
			offset = scriptsOffset
		} else {
			offset += scriptsOffset
		}
		line, column := sourceLineColumn(source, offset)
		target := targets[name]
		result = append(result, Target{
			ID:        fmt.Sprintf("javascript:%s:package:%s", filepath.ToSlash(path), name),
			Name:      name,
			Detail:    target.detail,
			Kind:      target.kind,
			Language:  javascriptLanguage,
			Path:      filepath.ToSlash(path),
			Directory: directory,
			Line:      line,
			Column:    column,
		})
	}
	return result, nil
}

func isNodePackageScript(command string) bool {
	for _, field := range strings.Fields(command) {
		field = strings.Trim(field, `"';&|()`)
		field = strings.ToLower(filepath.Base(filepath.FromSlash(field)))
		switch field {
		case "node", "node.exe", "tsx", "tsx.cmd", "ts-node", "ts-node.cmd", "ts-node-dev", "ts-node-dev.cmd", "nodemon", "nodemon.cmd":
			return true
		}
	}
	return false
}

func isViteDevelopmentScript(command string) bool {
	fields := strings.Fields(command)
	for index, field := range fields {
		field = strings.Trim(field, `"';&|()`)
		field = filepath.Base(filepath.FromSlash(field))
		if field != "vite" && field != "vite.cmd" {
			continue
		}
		if index+1 < len(fields) {
			next := strings.Trim(fields[index+1], `"';&|()`)
			if next == "build" || next == "preview" {
				return false
			}
		}
		return true
	}
	return false
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
	case "browser-script":
		return browserScriptPlan(request)
	case "node-script":
		return nodeScriptPlan(request)
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

func browserScriptPlan(request Request) (Plan, error) {
	command, projectDir, packageManager, packageManagerPath, err := resolvePackageScript(request)
	if err != nil {
		return Plan{}, err
	}
	if !isViteDevelopmentScript(command) {
		return Plan{}, fmt.Errorf("package.json script %q is no longer a Vite development script", request.Target.Name)
	}
	browser := strings.TrimSpace(request.BrowserExecutable)
	if browser == "" {
		browser = findChromiumBrowser()
	}
	if browser == "" {
		return Plan{}, errors.New("no supported Chromium browser is available; Wingman installs Chrome for Testing automatically, so wait for the tools update to finish and try again")
	}
	port := vitePort(projectDir, command)
	url := "http://localhost:" + strconv.Itoa(port)
	configuration := map[string]any{
		"type":              "pwa-chrome",
		"url":               url,
		"webRoot":           ".",
		"sourceMaps":        true,
		"runtimeExecutable": browser,
	}
	if request.Action == "run" {
		configuration["noDebug"] = true
	}
	return Plan{
		Title:         fmt.Sprintf("%s %s run %s", actionLabel(request.Action), packageManager, request.Target.Name),
		Summary:       fmt.Sprintf("Run the package.json %q script and %s its browser app at %s.", request.Target.Name, strings.ToLower(actionLabel(request.Action)), url),
		ProjectDir:    request.ProjectDir,
		Request:       "launch",
		IO:            dap.IOOutput,
		Configuration: configuration,
		PreLaunch: &dap.ProcessLaunch{
			Title: "Development server", Command: packageManagerPath,
			Args: []string{"run", request.Target.Name}, ReadyURL: url,
		},
	}, nil
}

func nodeScriptPlan(request Request) (Plan, error) {
	command, _, packageManager, packageManagerPath, err := resolvePackageScript(request)
	if err != nil {
		return Plan{}, err
	}
	if !isNodePackageScript(command) {
		return Plan{}, fmt.Errorf("package.json script %q is no longer a Node.js script", request.Target.Name)
	}
	configuration := map[string]any{
		"type":                     "pwa-node",
		"cwd":                      ".",
		"runtimeExecutable":        packageManagerPath,
		"runtimeArgs":              []string{"run-script", request.Target.Name},
		"sourceMaps":               true,
		"autoAttachChildProcesses": true,
		"skipFiles":                []string{"<node_internals>/**"},
	}
	plan := Plan{
		Title:            fmt.Sprintf("%s %s run %s", actionLabel(request.Action), packageManager, request.Target.Name),
		Summary:          fmt.Sprintf("%s the package.json %q Node.js script.", actionLabel(request.Action), request.Target.Name),
		ProjectDir:       request.ProjectDir,
		Request:          "launch",
		IO:               dap.IOOutput,
		SupportsTerminal: true,
		Configuration:    configuration,
	}
	if request.Action == "run" {
		configuration["noDebug"] = true
	}
	return plan, nil
}

func resolvePackageScript(request Request) (command, projectDir, packageManager, packageManagerPath string, err error) {
	manifestPath := request.Target.Path
	if !filepath.IsAbs(manifestPath) && request.WorkspaceDir != "" {
		manifestPath = filepath.Join(request.WorkspaceDir, filepath.FromSlash(manifestPath))
	}
	contents, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", "", "", "", fmt.Errorf("read package.json: %w", err)
	}
	var manifest nodePackage
	if err := json.Unmarshal(contents, &manifest); err != nil {
		return "", "", "", "", fmt.Errorf("parse package.json: %w", err)
	}
	command, ok := manifest.Scripts[request.Target.Name]
	if !ok {
		return "", "", "", "", fmt.Errorf("package.json script %q no longer exists", request.Target.Name)
	}
	projectDir = absoluteProjectDir(request)
	packageManager = nodePackageManager(projectDir, manifest.PackageManager)
	packageManagerPath, err = exec.LookPath(packageManager)
	if err != nil {
		return "", "", "", "", fmt.Errorf("package.json script %q requires %s, but it is not available", request.Target.Name, packageManager)
	}
	packageManagerPath, err = filepath.Abs(packageManagerPath)
	if err != nil {
		return "", "", "", "", fmt.Errorf("resolve %s: %w", packageManager, err)
	}
	return command, projectDir, packageManager, packageManagerPath, nil
}

func vitePort(projectDir, command string) int {
	if match := viteCLIPortPattern.FindStringSubmatch(command); len(match) == 2 {
		if value, err := strconv.Atoi(match[1]); err == nil && value >= 1 && value <= 65535 {
			return value
		}
	}
	entries, _ := os.ReadDir(projectDir)
	for _, entry := range entries {
		if entry.IsDir() || !viteConfigPattern.MatchString(entry.Name()) {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(projectDir, entry.Name()))
		if err == nil {
			masked := maskCStyleSource(contents, true, true)
			if match := vitePortPattern.FindSubmatch(masked); len(match) == 2 {
				if value, err := strconv.Atoi(string(match[1])); err == nil && value >= 1 && value <= 65535 {
					return value
				}
			}
		}
	}
	return 5173
}

func absoluteProjectDir(request Request) string {
	projectDir := filepath.FromSlash(request.ProjectDir)
	if !filepath.IsAbs(projectDir) && request.WorkspaceDir != "" {
		projectDir = filepath.Join(request.WorkspaceDir, projectDir)
	}
	return filepath.Clean(projectDir)
}

func nodePackageManager(projectDir, declared string) string {
	if name := strings.SplitN(strings.TrimSpace(declared), "@", 2)[0]; name == "npm" || name == "pnpm" || name == "yarn" || name == "bun" {
		return name
	}
	for _, candidate := range []struct{ file, command string }{
		{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}, {"bun.lock", "bun"}, {"bun.lockb", "bun"},
	} {
		if info, err := os.Stat(filepath.Join(projectDir, candidate.file)); err == nil && !info.IsDir() {
			return candidate.command
		}
	}
	return "npm"
}

func findChromiumBrowser() string {
	if explicit := strings.TrimSpace(os.Getenv("CHROME_PATH")); executableFilePath(explicit) {
		return explicit
	}
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
		}
	case "windows":
		for _, root := range []string{os.Getenv("PROGRAMFILES"), os.Getenv("PROGRAMFILES(X86)"), os.Getenv("LOCALAPPDATA")} {
			if root == "" {
				continue
			}
			candidates = append(candidates,
				filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe"),
				filepath.Join(root, "Microsoft", "Edge", "Application", "msedge.exe"),
				filepath.Join(root, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
			)
		}
	}
	for _, candidate := range candidates {
		if executableFilePath(candidate) {
			return candidate
		}
	}
	for _, command := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge", "brave-browser"} {
		if path, err := exec.LookPath(command); err == nil {
			return path
		}
	}
	return ""
}

func executableFilePath(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && (runtime.GOOS == "windows" || info.Mode()&0o111 != 0)
}

// RequiresChromium reports whether the workspace contains a package script
// that launches a browser development server.
func RequiresChromium(ctx context.Context, root string) (bool, error) {
	found := false
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && skipDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(entry.Name(), "package.json") {
			return nil
		}
		info, err := entry.Info()
		if err != nil || info.Size() > maxSourceBytes {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		targets, err := packageScriptTargets(filepath.ToSlash(path), filepath.ToSlash(filepath.Dir(path)), contents)
		if err != nil {
			return nil
		}
		for _, target := range targets {
			if target.Kind == "browser-script" {
				found = true
				return fs.SkipAll
			}
		}
		return nil
	})
	return found, err
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
