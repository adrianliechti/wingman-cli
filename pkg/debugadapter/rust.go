package debugadapter

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/dap"
)

var (
	rustMainPattern           = regexp.MustCompile(`\bfn\s+main\s*(?:<[^>{}\n]*>)?\s*\(`)
	cargoTablePattern         = regexp.MustCompile(`^\s*\[\s*([^]]+?)\s*]\s*$`)
	cargoTargetTablePattern   = regexp.MustCompile(`^\s*\[\[\s*(bin|example)\s*]]\s*$`)
	cargoStringSettingPattern = regexp.MustCompile(`^\s*(name|path)\s*=\s*["']([^"']+)["']`)
)

type cargoManifestTarget struct {
	Kind string
	Name string
	Path string
}

type cargoManifest struct {
	PackageName string
	Targets     []cargoManifestTarget
}

type rustAdapter struct{}

func (rustAdapter) Language() string { return "Rust" }

func (rustAdapter) Descriptor() dap.AdapterDescriptor {
	return dap.AdapterDescriptor{
		Name:             "codelldb",
		Language:         "Rust",
		AdapterID:        "lldb",
		Command:          "codelldb",
		Transport:        dap.TransportStdio,
		TerminalStrategy: dap.TerminalRunInTerminal,
		Markers:          []string{"Cargo.toml", "Cargo.lock"},
		SourceExtensions: []string{".rs"},
		Defaults: map[string]any{
			"type":            "lldb",
			"sourceLanguages": []string{"rust"},
		},
		ConfigurationPaths: []dap.ConfigurationPath{
			{Key: "program", AllowMissing: true},
			{Key: "cwd", Directory: true},
		},
		IOConfigKey: "terminal",
		IOValues: map[dap.IOMode]string{
			dap.IOOutput:   "console",
			dap.IOTerminal: "integrated",
		},
	}
}

func (rustAdapter) Matches(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".rs")
}

func (rustAdapter) Detect(path string, source []byte) ([]Target, error) {
	masked := maskCStyleSource(source, false, false)
	match := rustMainPattern.FindIndex(masked)
	if len(match) == 0 {
		return nil, nil
	}
	mainOffset := match[0] + bytes.Index(masked[match[0]:match[1]], []byte("main"))
	line, column := sourceLineColumn(source, mainOffset)
	directory := filepath.ToSlash(filepath.Dir(filepath.FromSlash(path)))
	if directory == "" {
		directory = "."
	}
	name := strings.TrimSuffix(filepath.Base(filepath.FromSlash(path)), filepath.Ext(path))
	return []Target{{
		ID:        fmt.Sprintf("rust:%s:main", filepath.ToSlash(path)),
		Name:      name,
		Detail:    "Rust entry point",
		Kind:      "main",
		Language:  "Rust",
		Path:      filepath.ToSlash(path),
		Directory: directory,
		Line:      line,
		Column:    column,
	}}, nil
}

func (rustAdapter) Plan(request Request) (Plan, error) {
	if request.Target.Kind != "main" {
		return Plan{}, fmt.Errorf("unsupported Rust debug target kind %q", request.Target.Kind)
	}
	name, kind, err := rustCargoTarget(request)
	if err != nil {
		return Plan{}, err
	}
	program, exists := rustProgramPath(request, name, kind)
	configuration := map[string]any{
		"program": program,
		"cwd":     ".",
	}
	summary := fmt.Sprintf("%s Rust %s %s.", actionLabel(request.Action), kind, name)
	if !exists {
		buildTarget := "--bin " + name
		if kind == "example" {
			buildTarget = "--example " + name
		}
		summary += fmt.Sprintf(" Build it with cargo build %s first; the expected executable is %s.", buildTarget, filepath.ToSlash(program))
	}
	plan := Plan{
		Title:            actionLabel(request.Action) + " " + name,
		Summary:          summary,
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

func rustProgramPath(request Request, name, kind string) (string, bool) {
	path := filepath.Join("target", "debug")
	if kind == "example" {
		path = filepath.Join(path, "examples")
	}
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path = filepath.Join(path, name)

	projectDir := request.ProjectDir
	if !filepath.IsAbs(projectDir) && request.WorkspaceDir != "" {
		projectDir = filepath.Join(request.WorkspaceDir, filepath.FromSlash(projectDir))
	}
	info, err := os.Stat(filepath.Join(projectDir, path))
	return filepath.ToSlash(path), err == nil && !info.IsDir()
}

func rustCargoTarget(request Request) (string, string, error) {
	relativePath, err := projectPath(request.ProjectDir, request.Target.Path)
	if err != nil {
		return "", "", err
	}
	relativePath = filepath.ToSlash(filepath.Clean(filepath.FromSlash(relativePath)))
	manifest, err := readCargoManifest(request)
	if err != nil {
		return "", "", err
	}
	for _, target := range manifest.Targets {
		if target.Path == "" || filepath.ToSlash(filepath.Clean(filepath.FromSlash(target.Path))) != relativePath {
			continue
		}
		name := target.Name
		if name == "" {
			name = rustConventionalTargetName(relativePath)
		}
		if name == "" {
			return "", "", fmt.Errorf("Cargo %s target for %s has no name", target.Kind, relativePath)
		}
		return name, target.Kind, nil
	}

	parts := strings.Split(relativePath, "/")
	switch {
	case relativePath == "src/main.rs":
		if manifest.PackageName == "" {
			return "", "", errors.New("Cargo package name is required for src/main.rs")
		}
		return manifest.PackageName, "bin", nil
	case len(parts) == 3 && parts[0] == "src" && parts[1] == "bin" && strings.HasSuffix(parts[2], ".rs"):
		return strings.TrimSuffix(parts[2], ".rs"), "bin", nil
	case len(parts) == 4 && parts[0] == "src" && parts[1] == "bin" && parts[3] == "main.rs":
		return parts[2], "bin", nil
	case len(parts) == 2 && parts[0] == "examples" && strings.HasSuffix(parts[1], ".rs"):
		return strings.TrimSuffix(parts[1], ".rs"), "example", nil
	case len(parts) == 3 && parts[0] == "examples" && parts[2] == "main.rs":
		return parts[1], "example", nil
	default:
		return "", "", fmt.Errorf("Rust entry point %s is not a Cargo binary target; add a [[bin]] or [[example]] path in Cargo.toml", relativePath)
	}
}

func rustConventionalTargetName(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	if len(parts) == 0 {
		return ""
	}
	name := strings.TrimSuffix(parts[len(parts)-1], filepath.Ext(parts[len(parts)-1]))
	if name == "main" && len(parts) >= 2 {
		name = parts[len(parts)-2]
	}
	return name
}

func readCargoManifest(request Request) (cargoManifest, error) {
	projectDir := request.ProjectDir
	if !filepath.IsAbs(projectDir) && request.WorkspaceDir != "" {
		projectDir = filepath.Join(request.WorkspaceDir, filepath.FromSlash(projectDir))
	}
	contents, err := os.ReadFile(filepath.Join(projectDir, "Cargo.toml"))
	if err != nil {
		return cargoManifest{}, fmt.Errorf("read Cargo.toml: %w", err)
	}
	manifest := cargoManifest{}
	section := ""
	var target *cargoManifestTarget
	flushTarget := func() {
		if target != nil {
			manifest.Targets = append(manifest.Targets, *target)
			target = nil
		}
	}
	scanner := bufio.NewScanner(bytes.NewReader(contents))
	for scanner.Scan() {
		line := strings.TrimSpace(strings.SplitN(scanner.Text(), "#", 2)[0])
		if match := cargoTargetTablePattern.FindStringSubmatch(line); len(match) == 2 {
			flushTarget()
			section = ""
			target = &cargoManifestTarget{Kind: match[1]}
			continue
		}
		if match := cargoTablePattern.FindStringSubmatch(line); len(match) == 2 {
			flushTarget()
			section = strings.TrimSpace(match[1])
			continue
		}
		match := cargoStringSettingPattern.FindStringSubmatch(line)
		if len(match) != 3 {
			continue
		}
		if target != nil {
			switch match[1] {
			case "name":
				target.Name = match[2]
			case "path":
				target.Path = filepath.ToSlash(filepath.Clean(filepath.FromSlash(match[2])))
			}
		} else if section == "package" && match[1] == "name" && manifest.PackageName == "" {
			manifest.PackageName = match[2]
		}
	}
	flushTarget()
	if err := scanner.Err(); err != nil {
		return cargoManifest{}, fmt.Errorf("read Cargo.toml: %w", err)
	}
	return manifest, nil
}
