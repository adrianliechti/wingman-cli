// Package plugin implements an Agent Plugins v1.0.0 client: it loads plugin
// directories, validates their manifests, and exposes the skills and MCP
// servers they contribute.
//
// https://agent-plugins.org
package plugin

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/adrianliechti/wingman-agent/pkg/layout"
	"github.com/adrianliechti/wingman-agent/pkg/mcp"
	"github.com/adrianliechti/wingman-agent/pkg/skill"
)

// Plugin is one loaded plugin package.
type Plugin struct {
	Name string

	Root string
	Data string

	Manifest Manifest

	Skills  []skill.Skill
	Servers map[string]mcp.ServerConfig
}

// Diagnostic reports something a plugin got wrong that did not stop the rest of
// it from loading.
type Diagnostic struct {
	Path    string
	Message string
}

func (d Diagnostic) String() string {
	return d.Path + ": " + d.Message
}

// Discover loads every plugin reachable from the project and the user's home
// directory. A plugin name resolves once: the first directory that provides it
// wins and later copies are reported. Plugins whose manifest is invalid are
// reported and skipped.
//
// projectData and personalData are the roots under which each scope's
// PLUGIN_DATA directories are created; the caller owns their location so plugin
// state sits alongside the rest of a project's state.
func Discover(workDir, projectData, personalData string) ([]Plugin, []Diagnostic) {
	var plugins []Plugin
	var diagnostics []Diagnostic

	seen := make(map[string]string)

	for _, root := range layout.ProjectRoots(workDir, "plugins") {
		plugins, diagnostics = discover(root, projectData, plugins, diagnostics, seen)
	}

	for _, root := range layout.PersonalRoots("plugins") {
		plugins, diagnostics = discover(root, personalData, plugins, diagnostics, seen)
	}

	return plugins, diagnostics
}

func discover(root, dataRoot string, plugins []Plugin, diagnostics []Diagnostic, seen map[string]string) ([]Plugin, []Diagnostic) {
	entries, err := os.ReadDir(root)

	if err != nil {
		return plugins, diagnostics
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	slices.Sort(names)

	for _, name := range names {
		dir := filepath.Join(root, name)

		if _, err := os.Stat(filepath.Join(dir, "plugin.json")); err != nil {
			continue
		}

		p, notes, err := Load(dir, dataRoot)

		for _, note := range notes {
			diagnostics = append(diagnostics, Diagnostic{Path: dir, Message: note})
		}

		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Path: dir, Message: err.Error()})
			continue
		}

		if winner, ok := seen[p.Name]; ok {
			diagnostics = append(diagnostics, Diagnostic{
				Path:    dir,
				Message: fmt.Sprintf("plugin %q is shadowed by %s", p.Name, winner),
			})
			continue
		}
		seen[p.Name] = dir

		plugins = append(plugins, *p)
	}

	return plugins, diagnostics
}

// Load reads one plugin directory. It returns an error only when the manifest
// is unusable; a component that fails to load is reported and skipped so the
// remaining components still work.
func Load(dir, dataRoot string) (*Plugin, []string, error) {
	root, err := resolvePath(dir)
	if err != nil {
		return nil, nil, err
	}

	manifestPath := filepath.Join(root, "plugin.json")

	resolvedManifest, err := resolvePath(manifestPath)
	if err != nil {
		return nil, nil, err
	}

	if !contains(root, resolvedManifest) {
		return nil, nil, fmt.Errorf("plugin.json resolves outside the plugin root")
	}

	content, err := os.ReadFile(resolvedManifest)
	if err != nil {
		return nil, nil, err
	}

	manifest, notes, err := parseManifest(content)
	if err != nil {
		return nil, notes, err
	}

	p := &Plugin{
		Name:     manifest.Name,
		Root:     root,
		Manifest: manifest,
	}

	if dataRoot != "" {
		p.Data = filepath.Join(dataRoot, manifest.Name)

		// Containment compares resolved paths, so the data root has to be
		// resolved too or a symlinked ancestor makes every ${PLUGIN_DATA} path
		// look like an escape.
		if resolved, err := resolvePath(p.Data); err == nil {
			p.Data = resolved
		}
	}

	skills, skillNotes := loadSkills(root, manifest.Name)
	p.Skills = skills
	notes = append(notes, skillNotes...)

	servers, serverNotes := loadServers(p)
	p.Servers = servers
	notes = append(notes, serverNotes...)

	return p, notes, nil
}

func loadSkills(root, name string) ([]skill.Skill, []string) {
	dir := filepath.Join(root, "skills")

	info, err := os.Stat(dir)

	if err != nil {
		return nil, nil
	}

	if !info.IsDir() {
		return nil, []string{"skills is not a directory"}
	}

	var notes []string
	var skills []skill.Skill

	for _, sk := range skill.LoadDir(dir) {
		resolved, err := resolvePath(sk.Location)

		if err != nil || !contains(root, resolved) {
			notes = append(notes, fmt.Sprintf("skipping skill %q: resolves outside the plugin root", sk.Name))
			continue
		}

		sk.Location = resolved
		sk.Plugin = name

		skills = append(skills, sk)
	}

	return skills, notes
}

func loadServers(p *Plugin) (map[string]mcp.ServerConfig, []string) {
	path := filepath.Join(p.Root, "mcp.json")

	info, err := os.Lstat(path)

	if err != nil {
		return nil, nil
	}

	if !info.Mode().IsRegular() {
		return nil, []string{"mcp.json is not a regular file"}
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return nil, []string{fmt.Sprintf("disabling MCP: %v", err)}
	}

	if p.Data == "" {
		return nil, []string{"disabling MCP: no data directory available"}
	}

	servers, notes, err := parseMCP(content, p.Manifest.Schema, p.Root, p.Data)
	if err != nil {
		return nil, append(notes, "disabling MCP: "+err.Error())
	}

	if needsData(servers) {
		if err := os.MkdirAll(p.Data, 0755); err != nil {
			return nil, append(notes, fmt.Sprintf("disabling MCP: create data directory: %v", err))
		}
	}

	return servers, notes
}

func needsData(servers map[string]mcp.ServerConfig) bool {
	for _, server := range servers {
		if server.Command != "" {
			return true
		}
	}

	return false
}

// Skills returns every plugin's skills in discovery order.
func Skills(plugins []Plugin) []skill.Skill {
	var skills []skill.Skill

	for _, p := range plugins {
		skills = append(skills, p.Skills...)
	}

	return skills
}

// Servers returns every plugin's MCP servers keyed by their declared name.
// Earlier plugins win a contested name, matching skill precedence.
func Servers(plugins []Plugin) (map[string]mcp.ServerConfig, []Diagnostic) {
	servers := make(map[string]mcp.ServerConfig)
	owners := make(map[string]string)

	var diagnostics []Diagnostic

	for _, p := range plugins {
		for _, name := range slices.Sorted(maps.Keys(p.Servers)) {
			if owner, ok := owners[name]; ok {
				diagnostics = append(diagnostics, Diagnostic{
					Path:    p.Root,
					Message: fmt.Sprintf("MCP server %q is shadowed by plugin %q", name, owner),
				})
				continue
			}

			owners[name] = p.Name
			servers[name] = p.Servers[name]
		}
	}

	return servers, diagnostics
}
