package debugadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/dap"
)

const cargoMetadataTimeout = 10 * time.Second

var rustMainPattern = regexp.MustCompile(`\bfn\s+main\s*(?:<[^>{}\n]*>)?\s*\(`)

type cargoMetadata struct {
	Packages        []cargoMetadataPackage `json:"packages"`
	TargetDirectory string                 `json:"target_directory"`
}

type cargoMetadataPackage struct {
	Targets []cargoMetadataTarget `json:"targets"`
}

type cargoMetadataTarget struct {
	Name    string   `json:"name"`
	Kind    []string `json:"kind"`
	SrcPath string   `json:"src_path"`
}

type rustAdapter struct {
	loadMetadata func(projectDir string) (cargoMetadata, error)
}

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

func (adapter rustAdapter) Plan(request Request) (Plan, error) {
	if request.Target.Kind != "main" {
		return Plan{}, fmt.Errorf("unsupported Rust debug target kind %q", request.Target.Kind)
	}
	projectDir, err := rustProjectDir(request)
	if err != nil {
		return Plan{}, err
	}
	loader := adapter.loadMetadata
	if loader == nil {
		loader = loadCargoMetadata
	}
	metadata, err := loader(projectDir)
	if err != nil {
		return Plan{}, err
	}
	target, kind, err := rustCargoTarget(request, metadata)
	if err != nil {
		return Plan{}, err
	}
	program, exists, err := rustProgramPath(request, metadata.TargetDirectory, target.Name, kind)
	if err != nil {
		return Plan{}, err
	}

	configuration := map[string]any{
		"program": program,
		"cwd":     ".",
	}
	targetLabel := "executable"
	buildTarget := "--bin " + target.Name
	if kind == "example" {
		targetLabel = "example"
		buildTarget = "--example " + target.Name
	}
	summary := fmt.Sprintf("%s Rust %s %s.", actionLabel(request.Action), targetLabel, target.Name)
	if !exists {
		summary += fmt.Sprintf(" Build it with cargo build %s first; the expected executable is %s.", buildTarget, filepath.ToSlash(program))
	}
	plan := Plan{
		Title:            actionLabel(request.Action) + " " + target.Name,
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

func loadCargoMetadata(projectDir string) (cargoMetadata, error) {
	cargo := resolveCargoExecutable()
	if cargo == "" {
		return cargoMetadata{}, fmt.Errorf("Cargo was not found; install Rust or add cargo to PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), cargoMetadataTimeout)
	defer cancel()
	manifest := filepath.Join(projectDir, "Cargo.toml")
	command := exec.CommandContext(ctx, cargo,
		"metadata", "--no-deps", "--format-version=1", "--manifest-path", manifest,
	)
	command.Dir = projectDir
	output, err := command.Output()
	if err != nil {
		if ctx.Err() != nil {
			return cargoMetadata{}, fmt.Errorf("read Cargo metadata: %w", ctx.Err())
		}
		detail := ""
		if exit, ok := err.(*exec.ExitError); ok {
			detail = strings.TrimSpace(string(exit.Stderr))
		}
		if detail != "" {
			return cargoMetadata{}, fmt.Errorf("read Cargo metadata: %s", detail)
		}
		return cargoMetadata{}, fmt.Errorf("read Cargo metadata: %w", err)
	}
	var metadata cargoMetadata
	if err := json.Unmarshal(output, &metadata); err != nil {
		return cargoMetadata{}, fmt.Errorf("decode Cargo metadata: %w", err)
	}
	if strings.TrimSpace(metadata.TargetDirectory) == "" {
		return cargoMetadata{}, fmt.Errorf("Cargo metadata did not report a target directory")
	}
	return metadata, nil
}

func resolveCargoExecutable() string {
	if configured := strings.TrimSpace(os.Getenv("CARGO")); configured != "" {
		if path, err := exec.LookPath(configured); err == nil {
			return path
		}
	}
	if path, err := exec.LookPath("cargo"); err == nil {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	name := "cargo"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(home, ".cargo", "bin", name)
	if info, err := os.Stat(path); err == nil && !info.IsDir() && (runtime.GOOS == "windows" || info.Mode()&0o111 != 0) {
		return path
	}
	return ""
}

func rustCargoTarget(request Request, metadata cargoMetadata) (cargoMetadataTarget, string, error) {
	targetPath := request.Target.Path
	if !filepath.IsAbs(targetPath) {
		targetPath = filepath.Join(request.WorkspaceDir, filepath.FromSlash(targetPath))
	}
	targetPath, err := filepath.Abs(targetPath)
	if err != nil {
		return cargoMetadataTarget{}, "", fmt.Errorf("resolve Rust target path: %w", err)
	}
	for _, pkg := range metadata.Packages {
		for _, target := range pkg.Targets {
			if !sameCargoPath(target.SrcPath, targetPath) {
				continue
			}
			for _, kind := range []string{"bin", "example"} {
				if slices.Contains(target.Kind, kind) {
					return target, kind, nil
				}
			}
			return cargoMetadataTarget{}, "", fmt.Errorf("Rust entry point %s is a Cargo %s target, not an executable or example", request.Target.Path, strings.Join(target.Kind, ", "))
		}
	}
	return cargoMetadataTarget{}, "", fmt.Errorf("Rust entry point %s is not a Cargo executable target", request.Target.Path)
}

func sameCargoPath(left, right string) bool {
	left, leftErr := canonicalCargoPath(filepath.FromSlash(left))
	right, rightErr := canonicalCargoPath(filepath.FromSlash(right))
	if leftErr != nil || rightErr != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func rustProgramPath(request Request, targetDirectory, name, kind string) (string, bool, error) {
	projectDir, err := rustProjectDir(request)
	if err != nil {
		return "", false, err
	}
	if !filepath.IsAbs(targetDirectory) {
		targetDirectory = filepath.Join(projectDir, filepath.FromSlash(targetDirectory))
	}
	targetDirectory, err = canonicalCargoPath(targetDirectory)
	if err != nil {
		return "", false, fmt.Errorf("resolve Cargo target directory: %w", err)
	}
	path := filepath.Join(targetDirectory, "debug")
	if kind == "example" {
		path = filepath.Join(path, "examples")
	}
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path = filepath.Join(path, name)

	workspaceDir := request.WorkspaceDir
	if strings.TrimSpace(workspaceDir) == "" {
		workspaceDir = projectDir
	}
	workspaceDir, err = canonicalCargoPath(workspaceDir)
	if err != nil {
		return "", false, fmt.Errorf("resolve Rust workspace: %w", err)
	}
	workspaceRelative, err := filepath.Rel(workspaceDir, path)
	if err != nil || workspaceRelative == ".." || strings.HasPrefix(workspaceRelative, ".."+string(filepath.Separator)) {
		return "", false, fmt.Errorf("Cargo target directory must stay inside the workspace")
	}
	program, err := filepath.Rel(projectDir, path)
	if err != nil {
		return "", false, fmt.Errorf("resolve Cargo executable: %w", err)
	}
	info, statErr := os.Stat(path)
	return filepath.ToSlash(program), statErr == nil && !info.IsDir(), nil
}

func rustProjectDir(request Request) (string, error) {
	projectDir := request.ProjectDir
	if !filepath.IsAbs(projectDir) {
		projectDir = filepath.Join(request.WorkspaceDir, filepath.FromSlash(projectDir))
	}
	projectDir, err := canonicalCargoPath(projectDir)
	if err != nil {
		return "", fmt.Errorf("resolve Cargo project: %w", err)
	}
	return projectDir, nil
}

// Cargo canonicalizes paths in its JSON output. Resolve the existing prefix of
// a possibly unbuilt output path so comparisons remain correct through
// symlinked temporary/workspace directories as well.
func canonicalCargoPath(value string) (string, error) {
	path, err := filepath.Abs(filepath.Clean(value))
	if err != nil {
		return "", err
	}
	probe := path
	var missing []string
	for {
		if _, err := os.Lstat(probe); err == nil {
			resolved, err := filepath.EvalSymlinks(probe)
			if err != nil {
				return "", err
			}
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return path, nil
		}
		missing = append(missing, filepath.Base(probe))
		probe = parent
	}
}
