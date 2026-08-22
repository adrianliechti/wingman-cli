package dap

import (
	"context"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
)

// AdapterRequirement describes the commands capable of serving one detected
// debugger integration, in preference order.
type AdapterRequirement struct {
	Name     string
	Commands []string
}

// DetectRequirements reports adapter needs independently of whether the
// adapter executable is installed.
func DetectRequirements(ctx context.Context, workspace string, adapters []AdapterDescriptor) ([]AdapterRequirement, error) {
	var requirements []AdapterRequirement
	for _, adapter := range adapters {
		projects, err := detectProjects(ctx, workspace, adapter.Markers, adapter.SourceExtensions)
		if err != nil {
			return nil, err
		}
		if len(projects) == 0 || adapter.Command == "" {
			continue
		}
		requirements = append(requirements, AdapterRequirement{Name: adapter.Name, Commands: []string{adapter.Command}})
	}
	return requirements, nil
}

func detectProjects(ctx context.Context, workspace string, markers, sourceExtensions []string) ([]string, error) {
	markerSet := make(map[string]bool, len(markers))
	for _, marker := range markers {
		markerSet[strings.ToLower(marker)] = true
	}
	extensionSet := make(map[string]bool, len(sourceExtensions))
	for _, extension := range sourceExtensions {
		extensionSet[strings.ToLower(extension)] = true
	}
	seen := make(map[string]bool)
	sourceDirs := make(map[string]bool)
	var projects []string
	err := filepath.WalkDir(workspace, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if entry.IsDir() {
			if path != workspace && skipProjectDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if extensionSet[strings.ToLower(filepath.Ext(entry.Name()))] {
			sourceDirs[filepath.Dir(path)] = true
		}
		if !matchesProjectMarker(entry.Name(), markerSet) {
			return nil
		}
		dir := filepath.Dir(path)
		if !seen[dir] {
			seen[dir] = true
			projects = append(projects, dir)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for sourceDir := range sourceDirs {
		covered := false
		for _, project := range projects {
			rel, err := filepath.Rel(project, sourceDir)
			if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				covered = true
				break
			}
		}
		if !covered && !seen[workspace] {
			projects = append(projects, workspace)
			break
		}
	}
	slices.Sort(projects)
	return projects, nil
}

func matchesProjectMarker(name string, markers map[string]bool) bool {
	name = strings.ToLower(name)
	if markers[name] {
		return true
	}
	for marker := range markers {
		if !strings.ContainsAny(marker, "*?[") {
			continue
		}
		if matched, err := filepath.Match(marker, name); err == nil && matched {
			return true
		}
	}
	return false
}

func skipProjectDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules", "vendor", "testdata", "target", "build", "dist", "__pycache__", "venv", "env":
		return true
	default:
		return false
	}
}
