// Package plugin implements Agent Plugins v1.0.0 plus Codex/Claude manifest
// compatibility. It loads plugin directories and exposes their skills, MCP
// servers, and lifecycle hooks.
//
// https://agent-plugins.org
package plugin

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/adrianliechti/wingman-agent/pkg/agent/hook/external"
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
	Format   Format

	Skills  []skill.Skill
	Servers map[string]mcp.ServerConfig
	Hooks   *external.Config
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

	for index, root := range layout.ProjectRoots(workDir, "plugins") {
		plugins, diagnostics = discover(root, projectData, plugins, diagnostics, seen, index == len(layout.Dirs)-1)
	}

	for index, root := range layout.PersonalRoots("plugins") {
		plugins, diagnostics = discover(root, personalData, plugins, diagnostics, seen, index == len(layout.Dirs)-1)
	}

	return plugins, diagnostics
}

func discover(root, dataRoot string, plugins []Plugin, diagnostics []Diagnostic, seen map[string]string, allowManifestlessClaude bool) ([]Plugin, []Diagnostic) {
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

		if !hasPluginManifest(dir, allowManifestlessClaude) {
			continue
		}

		p, notes, err := loadPlugin(dir, dataRoot, allowManifestlessClaude)

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
	return loadPlugin(dir, dataRoot, true)
}

func loadPlugin(dir, dataRoot string, allowManifestlessClaude bool) (*Plugin, []string, error) {
	root, err := resolvePath(dir)
	if err != nil {
		return nil, nil, err
	}

	resolved, notes, err := loadResolvedManifest(root, allowManifestlessClaude)
	if err != nil {
		return nil, notes, err
	}
	manifest := resolved.Manifest

	p := &Plugin{
		Name:     manifest.Name,
		Root:     root,
		Manifest: manifest,
		Format:   resolved.Format,
	}

	if dataRoot != "" {
		resolvedDataRoot, err := resolvePath(dataRoot)
		if err != nil {
			return nil, notes, fmt.Errorf("resolve plugin data root: %w", err)
		}
		p.Data, err = resolvePath(filepath.Join(resolvedDataRoot, manifest.Name))
		if err != nil {
			return nil, notes, fmt.Errorf("resolve plugin data directory: %w", err)
		}
		if !contains(resolvedDataRoot, p.Data) {
			return nil, notes, fmt.Errorf("plugin name %q escapes the plugin data root", manifest.Name)
		}
	}

	skills, skillNotes := loadSkills(root, manifest.Name, resolved.Skills, resolved.Format)
	if resolved.Format == ClaudePluginFormat && resolved.RootSkill {
		if _, err := os.Stat(filepath.Join(root, "skills")); os.IsNotExist(err) {
			rootSkill, loadErr := skill.LoadClaudeFile(filepath.Join(root, "SKILL.md"))
			if loadErr == nil {
				rootSkill.Plugin = manifest.Name
				skills = append(skills, rootSkill)
			} else if !os.IsNotExist(loadErr) {
				skillNotes = append(skillNotes, "skipping root skill: "+loadErr.Error())
			}
		}
	}
	p.Skills = skills
	notes = append(notes, skillNotes...)

	if resolved.Format == AgentPluginFormat {
		servers, serverNotes := loadServers(p)
		p.Servers = servers
		notes = append(notes, serverNotes...)
	} else {
		servers, serverNotes := loadNativeServers(p, resolved)
		p.Servers = servers
		notes = append(notes, serverNotes...)
	}

	hooks, hookNotes := loadHooks(root, resolved)
	p.Hooks = hooks
	notes = append(notes, hookNotes...)
	if hooks.RuleCount() > 0 && p.Data != "" {
		if err := os.MkdirAll(p.Data, 0755); err != nil {
			notes = append(notes, fmt.Sprintf("disabling hooks: create data directory: %v", err))
			p.Hooks = &external.Config{}
		}
	}

	return p, notes, nil
}

func loadSkills(root, name string, declared []string, format Format) ([]skill.Skill, []string) {
	var notes []string
	var skills []skill.Skill
	for _, value := range declared {
		dir, err := resolveManifestPath(root, "skills", value)
		if err != nil {
			notes = append(notes, "skipping skills: "+err.Error())
			continue
		}
		info, err := os.Stat(dir)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			notes = append(notes, fmt.Sprintf("%s is not a directory", value))
			continue
		}
		var loaded []skill.Skill
		switch format {
		case CodexPluginFormat:
			loaded = skill.LoadDirRecursiveCodex(dir)
		case ClaudePluginFormat:
			loaded = skill.LoadDirRecursiveClaude(dir)
		default:
			loaded = skill.LoadDir(dir)
		}
		for _, sk := range loaded {
			if format == ClaudePluginFormat && sk.DisplayName != "" {
				sk.Name = sk.DisplayName
			}
			resolved, err := resolvePath(sk.Location)
			if err != nil || !contains(root, resolved) {
				notes = append(notes, fmt.Sprintf("skipping skill %q: resolves outside the plugin root", sk.Name))
				continue
			}
			sk.Location = resolved
			sk.Plugin = name
			skills = append(skills, sk)
		}
	}

	return skills, notes
}

func loadServers(p *Plugin) (map[string]mcp.ServerConfig, []string) {
	path := filepath.Join(p.Root, "mcp.json")

	_, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []string{fmt.Sprintf("disabling MCP: inspect mcp.json: %v", err)}
	}
	resolved, err := resolvePath(path)
	if err != nil || !contains(p.Root, resolved) {
		return nil, []string{"mcp.json resolves outside the plugin root"}
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return nil, []string{"mcp.json is not a regular file"}
	}

	content, err := os.ReadFile(resolved)
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
