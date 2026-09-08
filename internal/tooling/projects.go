package tooling

import (
	"context"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman-agent/internal/pathutil"
)

type ProjectSpec struct {
	Markers    []string
	Extensions []string
}

type compiledProjectSpec struct {
	markers    map[string]bool
	patterns   []string
	extensions map[string]bool
	projects   map[string]bool
	sources    map[string]bool
}

func DetectProjects(ctx context.Context, workspace string, specs []ProjectSpec) ([][]string, error) {
	compiled := make([]compiledProjectSpec, len(specs))
	for index, spec := range specs {
		compiled[index] = compiledProjectSpec{
			markers: make(map[string]bool), extensions: make(map[string]bool),
			projects: make(map[string]bool), sources: make(map[string]bool),
		}
		for _, marker := range spec.Markers {
			marker = strings.ToLower(marker)
			if strings.ContainsAny(marker, "*?[") {
				compiled[index].patterns = append(compiled[index].patterns, marker)
			} else {
				compiled[index].markers[marker] = true
			}
		}
		for _, extension := range spec.Extensions {
			compiled[index].extensions[strings.ToLower(extension)] = true
		}
	}

	err := pathutil.WalkDir(workspace, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if path != workspace && SkipDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		name := strings.ToLower(entry.Name())
		extension := strings.ToLower(filepath.Ext(name))
		directory := filepath.Dir(path)
		for index := range compiled {
			item := &compiled[index]
			if item.extensions[extension] {
				item.sources[directory] = true
			}
			if item.markers[name] || matchesAny(item.patterns, name) {
				item.projects[directory] = true
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	result := make([][]string, len(compiled))
	for index := range compiled {
		item := &compiled[index]
		for sourceDir := range item.sources {
			if !coveredByProject(sourceDir, item.projects) {
				item.projects[workspace] = true
				break
			}
		}
		for project := range item.projects {
			result[index] = append(result[index], project)
		}
		slices.Sort(result[index])
	}
	return result, nil
}

func DetectProject(ctx context.Context, workspace string, spec ProjectSpec) ([]string, error) {
	projects, err := DetectProjects(ctx, workspace, []ProjectSpec{spec})
	if err != nil || len(projects) == 0 {
		return nil, err
	}
	return projects[0], nil
}

func coveredByProject(directory string, projects map[string]bool) bool {
	for project := range projects {
		relative, err := filepath.Rel(project, directory)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func matchesAny(patterns []string, name string) bool {
	for _, pattern := range patterns {
		if matched, err := filepath.Match(pattern, name); err == nil && matched {
			return true
		}
	}
	return false
}

func SkipDirectory(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch strings.ToLower(name) {
	case "node_modules", "vendor", "testdata", "target", "build", "dist", "__pycache__", "venv", "env":
		return true
	default:
		return false
	}
}
