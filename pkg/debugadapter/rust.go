package debugadapter

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/dap"
)

var (
	rustMainPattern    = regexp.MustCompile(`\bfn\s+main\s*(?:<[^>{}\n]*>)?\s*\(`)
	cargoNamePattern   = regexp.MustCompile(`^\s*name\s*=\s*["']([^"']+)["']`)
	cargoHeaderPattern = regexp.MustCompile(`^\s*\[([^]]+)]\s*$`)
)

type rustAdapter struct{}

func (rustAdapter) Language() string { return "Rust" }

func (rustAdapter) Descriptor() dap.AdapterDescriptor {
	return dap.AdapterDescriptor{
		Name:             "codelldb",
		Language:         "Rust",
		AdapterID:        "lldb",
		Command:          "codelldb",
		Args:             []string{"--port", "0"},
		Transport:        dap.TransportTCP,
		ReadyPrefix:      "Listening on port ",
		TerminalStrategy: dap.TerminalRunInTerminal,
		Markers:          []string{"Cargo.toml", "Cargo.lock"},
		SourceExtensions: []string{".rs"},
		Defaults: map[string]any{
			"type":            "lldb",
			"sourceLanguages": []string{"rust"},
		},
		ConfigurationPaths: []dap.ConfigurationPath{
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
	name, kind := rustTargetName(path)
	return []Target{{
		ID:        fmt.Sprintf("rust:%s:%s:%s", filepath.ToSlash(path), kind, name),
		Name:      name,
		Detail:    "Cargo " + kind,
		Kind:      kind,
		Language:  "Rust",
		Path:      filepath.ToSlash(path),
		Directory: directory,
		Line:      line,
		Column:    column,
	}}, nil
}

func rustTargetName(path string) (string, string) {
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	parts := strings.Split(clean, "/")
	base := strings.TrimSuffix(parts[len(parts)-1], filepath.Ext(parts[len(parts)-1]))
	for index, part := range parts {
		switch part {
		case "examples":
			if index+1 < len(parts) {
				name := strings.TrimSuffix(parts[index+1], filepath.Ext(parts[index+1]))
				return name, "example"
			}
		case "bin":
			if index > 0 && parts[index-1] == "src" && index+1 < len(parts) {
				name := strings.TrimSuffix(parts[index+1], filepath.Ext(parts[index+1]))
				return name, "bin"
			}
		}
	}
	if len(parts) >= 2 && parts[len(parts)-2] == "src" && parts[len(parts)-1] == "main.rs" {
		return "main", "main"
	}
	return base, "bin"
}

func (rustAdapter) Plan(request Request) (Plan, error) {
	name := request.Target.Name
	kind := "bin"
	switch request.Target.Kind {
	case "main":
		if packageName := cargoPackageName(request); packageName != "" {
			name = packageName
		}
	case "bin":
	case "example":
		kind = "example"
	default:
		return Plan{}, fmt.Errorf("unsupported Rust debug target kind %q", request.Target.Kind)
	}
	cargoArgs := []string{"build", "--bin", name}
	if kind == "example" {
		cargoArgs = []string{"build", "--example", name}
	}
	configuration := map[string]any{
		"cargo": map[string]any{
			"args":   cargoArgs,
			"filter": map[string]any{"name": name, "kind": kind},
		},
		"cwd": ".",
	}
	plan := Plan{
		Title:         actionLabel(request.Action) + " " + name,
		Summary:       fmt.Sprintf("%s Rust %s %s through Cargo.", actionLabel(request.Action), kind, name),
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

func cargoPackageName(request Request) string {
	projectDir := request.ProjectDir
	if !filepath.IsAbs(projectDir) && request.WorkspaceDir != "" {
		projectDir = filepath.Join(request.WorkspaceDir, filepath.FromSlash(projectDir))
	}
	contents, err := os.ReadFile(filepath.Join(projectDir, "Cargo.toml"))
	if err != nil {
		return ""
	}
	section := ""
	scanner := bufio.NewScanner(bytes.NewReader(contents))
	for scanner.Scan() {
		line := strings.TrimSpace(strings.SplitN(scanner.Text(), "#", 2)[0])
		if match := cargoHeaderPattern.FindStringSubmatch(line); len(match) == 2 {
			section = strings.TrimSpace(match[1])
			continue
		}
		if section != "package" {
			continue
		}
		if match := cargoNamePattern.FindStringSubmatch(line); len(match) == 2 {
			return match[1]
		}
	}
	return ""
}
