package lsp

import (
	"context"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/adrianliechti/wingman-agent/internal/tooling"
)

type Project struct {
	Dir    string
	Server Server
}

type projectRoot = Project

const serverVersionProbeTimeout = 5 * time.Second

func serverVersionSupported(server Server, command string) bool {
	if server.MinimumMajorVersion == 0 {
		return true
	}

	ctx, cancel := context.WithTimeout(context.Background(), serverVersionProbeTimeout)
	defer cancel()
	return tooling.MajorVersionAtLeast(ctx, command, server.MinimumMajorVersion)
}

func detectAll(workingDir string, managedResolver func(string) string) []projectRoot {
	index := indexWorkspace(workingDir)
	commands := make(map[string]string)
	versions := make(map[string]bool)
	resolver := tooling.Resolver{
		Workspace: workingDir,
		Managed:   managedResolver,
		Lookup: func(command string) string {
			if cached, ok := commands[command]; ok {
				return cached
			}
			resolved := tooling.Resolve(command)
			commands[command] = resolved
			return resolved
		},
	}

	var roots []projectRoot
	seen := make(map[string]bool)

	for _, project := range knownProjects {
		for _, dir := range projectDirs(index, project) {
			for _, candidate := range project.Servers {
				server, ok := resolveServer(dir, candidate, resolver, versions)
				if !ok {
					continue
				}

				root := projectRoot{Dir: dir, Server: server}
				if key := projectKey(root); !seen[key] {
					seen[key] = true
					roots = append(roots, root)
				}
				break
			}
		}
	}

	return roots
}

func projectDirs(index *workspaceIndex, project projectType) []string {
	var dirs []string
	seen := make(map[string]bool)

	for _, marker := range project.Markers {
		for _, entry := range index.matching(marker) {
			dir := filepath.Dir(entry.path)
			if seen[dir] {
				continue
			}
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}

	slices.Sort(dirs)

	return slices.DeleteFunc(dirs, func(dir string) bool {
		for _, exclude := range project.Excludes {
			if index.hasChild(dir, exclude) {
				return true
			}
		}
		return len(project.Requires) > 0 && !index.hasNestedFile(dir, project.Requires)
	})
}

func resolveServer(dir string, candidate Server, resolver tooling.Resolver, versions map[string]bool) (Server, bool) {
	for _, resolution := range resolver.Candidates([]string{dir}, candidate.Command) {
		versionKey := resolution.Path + "\x00" + strconv.Itoa(candidate.MinimumMajorVersion)
		supported, checked := versions[versionKey]
		if !checked {
			supported = serverVersionSupported(candidate, resolution.Path)
			versions[versionKey] = supported
		}
		if supported {
			candidate.Command = resolution.Path
			return candidate, true
		}
	}
	return Server{}, false
}

type workspaceEntry struct {
	path  string
	name  string
	isDir bool
}

// workspaceIndex holds one scan of the workspace, indexed so that marker
// lookups cost a map hit instead of a pass over every entry.
type workspaceIndex struct {
	byName map[string][]workspaceEntry
	byGlob map[string][]workspaceEntry
}

func (i *workspaceIndex) matching(pattern string) []workspaceEntry {
	if isGlob(pattern) {
		return i.byGlob[pattern]
	}
	return i.byName[pattern]
}

func (i *workspaceIndex) hasChild(dir, pattern string) bool {
	for _, entry := range i.matching(pattern) {
		if filepath.Dir(entry.path) == dir {
			return true
		}
	}
	return false
}

func (i *workspaceIndex) hasNestedFile(dir string, patterns []string) bool {
	for _, pattern := range patterns {
		for _, entry := range i.matching(pattern) {
			if !entry.isDir && isSubPath(dir, entry.path) {
				return true
			}
		}
	}
	return false
}

func indexWorkspace(workingDir string) *workspaceIndex {
	globs := markerGlobs()

	index := &workspaceIndex{
		byName: make(map[string][]workspaceEntry),
		byGlob: make(map[string][]workspaceEntry),
	}

	_ = filepath.WalkDir(workingDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || path == workingDir {
			return nil
		}

		name := entry.Name()
		item := workspaceEntry{path: path, name: name, isDir: entry.IsDir()}
		index.byName[name] = append(index.byName[name], item)

		for _, glob := range globs {
			if matchesName(glob, name) {
				index.byGlob[glob] = append(index.byGlob[glob], item)
			}
		}

		if entry.IsDir() && tooling.SkipDirectory(name) {
			return filepath.SkipDir
		}
		return nil
	})

	return index
}

var markerGlobs = sync.OnceValue(func() []string {
	var globs []string
	for _, project := range knownProjects {
		for _, patterns := range [][]string{project.Markers, project.Requires, project.Excludes} {
			for _, pattern := range patterns {
				if isGlob(pattern) && !slices.Contains(globs, pattern) {
					globs = append(globs, pattern)
				}
			}
		}
	}
	return globs
})

func isGlob(pattern string) bool {
	return strings.ContainsAny(pattern, `*?[`)
}

func matchesName(pattern, name string) bool {
	matched, err := filepath.Match(pattern, name)
	return err == nil && matched
}

func isSubPath(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
