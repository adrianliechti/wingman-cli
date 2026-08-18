// Package debugtarget discovers runnable source locations for editor launch
// actions. It is intentionally separate from package dap: target discovery is
// language-aware, while the DAP client and adapter configuration stay generic.
package debugtarget

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	defaultMaxFiles = 5_000
	maxSourceBytes  = 2 << 20
)

// Target is a source-level candidate that can seed the AI launch planner.
type Target struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Detail   string `json:"detail,omitempty"`
	Kind     string `json:"kind"`
	Language string `json:"language"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
}

// Detector recognizes target candidates for one or more source file types.
// Implementations must not launch programs or generate adapter configuration.
type Detector interface {
	Matches(path string) bool
	Detect(path string, source []byte) ([]Target, error)
}

// Registry composes independent language target detectors.
type Registry struct {
	detectors []Detector
}

// NewRegistry returns the built-in target detectors. Adding a language should
// only require another Detector; callers and the DAP package remain unchanged.
func NewRegistry() *Registry {
	return &Registry{detectors: []Detector{goDetector{}}}
}

func NewRegistryWith(detectors ...Detector) *Registry {
	return &Registry{detectors: slices.Clone(detectors)}
}

// DetectFile discovers targets in source. path must be workspace-relative and
// slash-separated; it is copied into every returned target.
func (registry *Registry) DetectFile(path string, source []byte) ([]Target, error) {
	path = filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	var result []Target
	for _, detector := range registry.detectors {
		if !detector.Matches(path) {
			continue
		}
		values, err := detector.Detect(path, source)
		if err != nil {
			return nil, err
		}
		result = append(result, values...)
	}
	sortTargets(result)
	return result, nil
}

// DetectWorkspace walks source files under root and returns a bounded set of
// launch candidates. Unreadable and oversized files are skipped best-effort.
func (registry *Registry) DetectWorkspace(ctx context.Context, root string) ([]Target, error) {
	root = filepath.Clean(root)
	visited := 0
	var result []Target
	err := filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
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
		if visited >= defaultMaxFiles {
			return fs.SkipAll
		}
		visited++
		rel, err := filepath.Rel(root, filePath)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		matched := false
		for _, detector := range registry.detectors {
			if detector.Matches(rel) {
				matched = true
				break
			}
		}
		if !matched {
			return nil
		}
		info, err := entry.Info()
		if err != nil || info.Size() > maxSourceBytes {
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
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules", "vendor", "testdata", "target", "build", "dist", "__pycache__", "venv":
		return true
	default:
		return false
	}
}
