// Package debugadapter contains the language-specific edge of debugging:
// source target discovery, deterministic launch policy, and the descriptor for
// starting each language's standalone Debug Adapter Protocol process.
//
// Protocol framing and session state remain in package dap.
package debugadapter

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman-agent/internal/pathutil"
	"github.com/adrianliechti/wingman-agent/internal/tooling"
	"github.com/adrianliechti/wingman-agent/pkg/dap"
)

const (
	defaultMaxFiles = 5_000
	maxSourceBytes  = 2 << 20
)

type Target struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Detail    string `json:"detail,omitempty"`
	Kind      string `json:"kind"`
	Language  string `json:"language"`
	Path      string `json:"path"`
	Directory string `json:"directory"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
}

type Request struct {
	Action            string
	WorkspaceDir      string
	ProjectDir        string
	BrowserExecutable string
	Target            Target
}

type Breakpoint struct {
	FilePath string
	Line     int
	Column   int
}

type Plan struct {
	Title               string
	Summary             string
	ProjectDir          string
	Request             string
	IO                  dap.IOMode
	SupportsTerminal    bool
	Configuration       map[string]any
	Breakpoints         []Breakpoint
	FunctionBreakpoints []string
	PreLaunch           *dap.ProcessLaunch
}

// LanguageAdapter owns all stable language-specific behavior. Implementations
// must not execute user code while detecting targets or preparing a plan.
type LanguageAdapter interface {
	Language() string
	Descriptor() dap.AdapterDescriptor
	Matches(path string) bool
	Detect(path string, source []byte) ([]Target, error)
	Plan(Request) (Plan, error)
}

type Registry struct {
	adapters   []LanguageAdapter
	byLanguage map[string]LanguageAdapter
}

func vscodeIOValues() map[dap.IOMode]string {
	return map[dap.IOMode]string{
		dap.IOOutput:   "internalConsole",
		dap.IOTerminal: "integratedTerminal",
	}
}

// ToolDirectory locates managed installations for adapters that load bundled
// files in addition to resolving an executable availability token.
type ToolDirectory interface {
	ToolDir(id string) string
}

// NewRegistry uses managed adapters and optionally loads their bundled files.
func NewRegistry(toolDirectories ...ToolDirectory) *Registry {
	java := javaAdapter{}
	if len(toolDirectories) > 0 {
		if tools := toolDirectories[0]; tools != nil {
			java.bundles = managedJavaDebugBundles(tools.ToolDir("java-debug"))
		}
	}
	return NewRegistryWith(
		goAdapter{}, pythonAdapter{}, java, rustAdapter{}, cAdapter{}, dotnetAdapter{}, javaScriptAdapter{},
	)
}

func NewRegistryWith(adapters ...LanguageAdapter) *Registry {
	registry := &Registry{
		adapters:   slices.Clone(adapters),
		byLanguage: make(map[string]LanguageAdapter, len(adapters)),
	}
	for _, adapter := range adapters {
		if adapter == nil {
			continue
		}
		registry.byLanguage[strings.ToLower(strings.TrimSpace(adapter.Language()))] = adapter
	}
	return registry
}

func (registry *Registry) Descriptors() []dap.AdapterDescriptor {
	result := make([]dap.AdapterDescriptor, 0, len(registry.adapters))
	for _, adapter := range registry.adapters {
		if adapter == nil {
			continue
		}
		descriptor := adapter.Descriptor()
		// A language can still participate in source-target discovery while its
		// optional adapter installation is absent. Empty commands are therefore
		// deliberately omitted from runtime discovery.
		if strings.TrimSpace(descriptor.Command) != "" {
			result = append(result, descriptor)
		}
	}
	return result
}

// serverInitializer marks adapters whose DAP endpoint is hosted inside a
// language server that needs additional initialization options.
type serverInitializer interface {
	ServerInitialization() (server string, options any)
}

// ServerInitializations returns the language-server initialization options
// required by hosted adapters, keyed by server name.
func (registry *Registry) ServerInitializations() map[string]any {
	result := make(map[string]any)
	for _, adapter := range registry.adapters {
		initializer, ok := adapter.(serverInitializer)
		if !ok {
			continue
		}
		if server, options := initializer.ServerInitialization(); server != "" && options != nil {
			result[server] = options
		}
	}
	return result
}

func (registry *Registry) Plan(language string, request Request) (Plan, error) {
	adapter := registry.byLanguage[strings.ToLower(strings.TrimSpace(language))]
	if adapter == nil {
		return Plan{}, fmt.Errorf("deterministic launch setup is not available for %s", language)
	}
	request.Action = strings.ToLower(strings.TrimSpace(request.Action))
	if request.Action == "" {
		request.Action = "debug"
	}
	if request.Action != "run" && request.Action != "debug" {
		return Plan{}, errors.New("action must be run or debug")
	}
	if !strings.EqualFold(request.Target.Language, adapter.Language()) {
		return Plan{}, fmt.Errorf("%s target cannot be launched with the %s adapter", request.Target.Language, adapter.Language())
	}
	return adapter.Plan(request)
}

func (registry *Registry) DetectFile(path string, source []byte) ([]Target, error) {
	path = filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	var result []Target
	for _, adapter := range registry.adapters {
		if adapter == nil || !adapter.Matches(path) {
			continue
		}
		values, err := adapter.Detect(path, source)
		if err != nil {
			return nil, err
		}
		result = append(result, values...)
	}
	sortTargets(result)
	return result, nil
}

func (registry *Registry) DetectWorkspace(ctx context.Context, root string) ([]Target, error) {
	root = filepath.Clean(root)
	visited := 0
	var result []Target
	err := pathutil.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if filePath != root && skipDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, filePath)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		matched := false
		for _, adapter := range registry.adapters {
			if adapter != nil && adapter.Matches(rel) {
				matched = true
				break
			}
		}
		if !matched {
			return nil
		}
		if visited >= defaultMaxFiles {
			return fs.SkipAll
		}
		visited++
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxSourceBytes {
			return nil
		}
		source, err := os.ReadFile(filePath)
		if err != nil {
			return nil
		}
		values, err := registry.DetectFile(rel, source)
		if err != nil {
			return nil
		}
		result = append(result, values...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortTargets(result)
	return result, nil
}

func sortTargets(values []Target) {
	slices.SortFunc(values, func(left, right Target) int {
		if order := strings.Compare(left.Path, right.Path); order != 0 {
			return order
		}
		if left.Line < right.Line {
			return -1
		}
		if left.Line > right.Line {
			return 1
		}
		return strings.Compare(left.Name, right.Name)
	})
}

func skipDir(name string) bool {
	return tooling.SkipDirectory(name)
}

func projectPath(projectDir, targetPath string) (string, error) {
	projectDir = filepath.Clean(filepath.FromSlash(projectDir))
	if projectDir == "" {
		projectDir = "."
	}
	targetPath = filepath.Clean(filepath.FromSlash(targetPath))
	rel, err := filepath.Rel(projectDir, targetPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("target %q is outside project %q", filepath.ToSlash(targetPath), filepath.ToSlash(projectDir))
	}
	if rel == "." {
		return ".", nil
	}
	return filepath.ToSlash(rel), nil
}

func actionLabel(action string) string {
	if action == "run" {
		return "Run"
	}
	return "Debug"
}

func targetBreakpoint(target Target) []Breakpoint {
	return []Breakpoint{{FilePath: target.Path, Line: target.Line, Column: target.Column}}
}

func absoluteCommandPath(command string) string {
	path, err := tooling.LookPath(command)
	if err != nil {
		return ""
	}
	if absolute, err := filepath.Abs(path); err == nil {
		return absolute
	}
	return path
}
