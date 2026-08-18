package dap

import (
	"context"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
)

func detectProjects(ctx context.Context, workspace string, markers, sourceExtensions []string) ([]string, error) {
	markerSet := make(map[string]bool, len(markers))
	for _, marker := range markers {
		markerSet[marker] = true
	}
	extensionSet := make(map[string]bool, len(sourceExtensions))
	for _, extension := range sourceExtensions {
		extensionSet[strings.ToLower(extension)] = true
	}
	seen := make(map[string]bool)
	sourceFound := false
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
			sourceFound = true
		}
		if !markerSet[entry.Name()] {
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
	if sourceFound && !seen[workspace] {
		projects = append(projects, workspace)
	}
	slices.Sort(projects)
	return projects, nil
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
