package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent/hook/external"
)

const (
	AgentPluginFormat  Format = "agent-plugin"
	CodexPluginFormat  Format = "codex-plugin"
	ClaudePluginFormat Format = "claude-plugin"

	// Codex defines this client-owned Agent Plugins extension namespace in
	// core-plugins/src/agent_plugin_manifest.rs. It is not part of the portable
	// Agent Plugins v1 schema.
	codexExtensionNamespace = "com.openai"
)

var legacyManifestPaths = []string{
	".codex-plugin/plugin.json",
	".claude-plugin/plugin.json",
}

// Format identifies the compatibility profile used to load a plugin. Native
// Codex and Claude packages deliberately stay distinct because their skill and
// component conventions are not interchangeable.
type Format string

type resolvedManifest struct {
	Manifest     Manifest
	Format       Format
	Path         string
	Skills       []string
	RootSkill    bool
	MCP          json.RawMessage
	MCPPresent   bool
	Hooks        json.RawMessage
	HooksPresent bool
	DefaultHooks bool
}

type legacyManifest struct {
	Name        string          `json:"name"`
	Version     string          `json:"version,omitempty"`
	Description string          `json:"description,omitempty"`
	Keywords    []string        `json:"keywords,omitempty"`
	Skills      json.RawMessage `json:"skills,omitempty"`
	MCPServers  json.RawMessage `json:"mcpServers,omitempty"`
	Hooks       json.RawMessage `json:"hooks,omitempty"`
}

func hasPluginManifest(root string, allowManifestlessClaude bool) bool {
	_, err := findManifest(root)
	return err == nil || (allowManifestlessClaude && hasClaudeComponents(root))
}

func hasClaudeComponents(root string) bool {
	for _, relative := range []string{"SKILL.md", "skills", "agents", "hooks/hooks.json", ".mcp.json"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); err == nil {
			return true
		}
	}
	return false
}

func findManifest(root string) (string, error) {
	portable := filepath.Join(root, "plugin.json")
	portableExists := false
	if data, err := os.ReadFile(portable); err == nil {
		portableExists = true
		var header struct {
			Schema string `json:"$schema"`
		}
		if json.Unmarshal(data, &header) == nil && strings.HasPrefix(header.Schema, "https://agent-plugins.org/schemas/") {
			return portable, nil
		}
	}
	for _, relative := range legacyManifestPaths {
		candidate := filepath.Join(root, filepath.FromSlash(relative))
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	if portableExists {
		return portable, nil
	}
	return "", os.ErrNotExist
}

func loadResolvedManifest(root string, allowManifestlessClaude bool) (resolvedManifest, []string, error) {
	manifestPath, err := findManifest(root)
	if err != nil {
		if allowManifestlessClaude && hasClaudeComponents(root) {
			return resolvedManifest{
				Manifest:     Manifest{Name: filepath.Base(root)},
				Format:       ClaudePluginFormat,
				Path:         root,
				Skills:       []string{"./skills"},
				RootSkill:    true,
				DefaultHooks: true,
			}, nil, nil
		}
		return resolvedManifest{}, nil, fmt.Errorf("plugin manifest not found")
	}
	resolvedPath, err := resolvePath(manifestPath)
	if err != nil {
		return resolvedManifest{}, nil, err
	}
	if !contains(root, resolvedPath) {
		return resolvedManifest{}, nil, fmt.Errorf("plugin manifest resolves outside the plugin root")
	}
	content, err := os.ReadFile(resolvedPath)
	if err != nil {
		return resolvedManifest{}, nil, err
	}

	if filepath.Clean(manifestPath) == filepath.Join(root, "plugin.json") {
		manifest, notes, err := parseManifest(content)
		if err != nil {
			return resolvedManifest{}, notes, err
		}
		resolved := resolvedManifest{
			Manifest: manifest,
			Format:   AgentPluginFormat,
			Path:     resolvedPath,
			Skills:   []string{"./skills"},
		}
		if extension, ok := manifest.Extensions[codexExtensionNamespace]; ok {
			resolved.Hooks, resolved.HooksPresent, err = hookDeclaration(extension)
			if err != nil {
				notes = append(notes, "ignoring invalid com.openai hooks: "+err.Error())
			}
			return resolved, notes, nil
		}
		overlay := filepath.Join(root, ".codex-plugin", "plugin.json")
		if _, inspectErr := os.Lstat(overlay); inspectErr == nil {
			resolvedOverlay, resolveErr := resolveExistingFile(root, overlay)
			if resolveErr != nil {
				notes = append(notes, "ignoring .codex-plugin overlay: "+resolveErr.Error())
			} else if overlayContent, readErr := os.ReadFile(resolvedOverlay); readErr != nil {
				notes = append(notes, "ignoring .codex-plugin overlay: "+readErr.Error())
			} else {
				resolved.Hooks, resolved.HooksPresent, err = hookDeclaration(overlayContent)
				if err != nil {
					notes = append(notes, "ignoring invalid .codex-plugin hooks: "+err.Error())
				}
			}
		} else if !os.IsNotExist(inspectErr) {
			notes = append(notes, "ignoring .codex-plugin overlay: "+inspectErr.Error())
		}
		return resolved, notes, nil
	}

	var legacy legacyManifest
	if err := json.Unmarshal(content, &legacy); err != nil {
		return resolvedManifest{}, nil, fmt.Errorf("parse %s: %w", manifestPath, err)
	}
	name := strings.TrimSpace(legacy.Name)
	if name == "" {
		name = filepath.Base(root)
	}
	format := CodexPluginFormat
	if filepath.Clean(manifestPath) == filepath.Join(root, ".claude-plugin", "plugin.json") {
		format = ClaudePluginFormat
	}
	resolved := resolvedManifest{
		Manifest: Manifest{
			Name:        name,
			Version:     strings.TrimSpace(legacy.Version),
			Description: strings.TrimSpace(legacy.Description),
			Keywords:    legacy.Keywords,
		},
		Format:       format,
		Path:         resolvedPath,
		MCP:          legacy.MCPServers,
		MCPPresent:   len(bytes.TrimSpace(legacy.MCPServers)) > 0,
		Hooks:        legacy.Hooks,
		HooksPresent: len(bytes.TrimSpace(legacy.Hooks)) > 0,
		DefaultHooks: true,
	}
	resolved.Skills, err = manifestPaths(legacy.Skills)
	if err != nil {
		return resolvedManifest{}, nil, fmt.Errorf("parse %s skills: %w", manifestPath, err)
	}
	if format == ClaudePluginFormat {
		declared := len(bytes.TrimSpace(legacy.Skills)) > 0
		if !slices.Contains(resolved.Skills, "./skills") {
			resolved.Skills = append([]string{"./skills"}, resolved.Skills...)
		}
		resolved.RootSkill = !declared
	} else if len(resolved.Skills) == 0 {
		resolved.Skills = []string{"./skills"}
	}
	return resolved, nil, nil
}

func hookDeclaration(data []byte) (json.RawMessage, bool, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, false, err
	}
	raw, ok := object["hooks"]
	return raw, ok, nil
}

func manifestPaths(raw json.RawMessage) ([]string, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}, nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return nil, fmt.Errorf("expected a string or string array")
	}
	return many, nil
}

func resolveManifestPath(root, field, value string) (string, error) {
	if !relativePath(value) {
		return "", fmt.Errorf("%s path %q must start with ./ and use portable separators", field, value)
	}
	if strings.Contains(strings.TrimPrefix(value, "./"), "..") {
		for _, component := range strings.Split(strings.TrimPrefix(value, "./"), "/") {
			if component == ".." {
				return "", fmt.Errorf("%s path %q must not contain ..", field, value)
			}
		}
	}
	resolved, err := resolvePath(filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(value, "./"))))
	if err != nil {
		return "", err
	}
	if !contains(root, resolved) {
		return "", fmt.Errorf("%s path %q resolves outside the plugin root", field, value)
	}
	return resolved, nil
}

func loadHooks(root string, manifest resolvedManifest) (*external.Config, []string) {
	config := &external.Config{}
	var notes []string
	if !manifest.HooksPresent {
		if manifest.DefaultHooks {
			return loadDefaultHooks(root)
		}
		return config, nil
	}

	paths, err := manifestPaths(manifest.Hooks)
	if err == nil {
		if len(paths) == 0 {
			return config, nil
		}
		for _, value := range paths {
			path, pathErr := resolveManifestPath(root, "hooks", value)
			if pathErr != nil {
				notes = append(notes, "skipping hooks: "+pathErr.Error())
				continue
			}
			if info, statErr := os.Stat(path); statErr != nil || !info.Mode().IsRegular() {
				if statErr != nil {
					notes = append(notes, fmt.Sprintf("skipping hooks: read %s: %v", path, statErr))
				} else {
					notes = append(notes, fmt.Sprintf("skipping hooks: %s is not a regular file", path))
				}
				continue
			}
			parsed, loadErr := external.Load(path)
			if loadErr != nil {
				notes = append(notes, "skipping hooks: "+loadErr.Error())
				continue
			}
			config.Merge(parsed)
		}
		return config, notes
	}

	var inline []json.RawMessage
	if bytes.HasPrefix(bytes.TrimSpace(manifest.Hooks), []byte("{")) {
		inline = []json.RawMessage{manifest.Hooks}
	} else if unmarshalErr := json.Unmarshal(manifest.Hooks, &inline); unmarshalErr != nil || len(inline) == 0 {
		return config, []string{"ignoring hooks: expected a path, path array, object, or object array"}
	}
	for index, raw := range inline {
		name := fmt.Sprintf("%s#hooks[%d]", manifest.Path, index)
		parsed, parseErr := parseInlineHooks(name, raw)
		if parseErr != nil {
			notes = append(notes, "skipping hooks: "+parseErr.Error())
			continue
		}
		config.Merge(parsed)
	}
	return config, notes
}

func parseInlineHooks(name string, raw json.RawMessage) (*external.Config, error) {
	parsed, err := external.Parse(name, raw)
	if err == nil {
		return parsed, nil
	}
	// Claude plugin manifests place the event map directly in their `hooks`
	// field; Codex inline manifests use a complete {"hooks": {...}} document.
	// Accept both without changing the standalone hooks.json schema.
	wrapped, marshalErr := json.Marshal(map[string]json.RawMessage{"hooks": raw})
	if marshalErr != nil {
		return nil, err
	}
	if claude, claudeErr := external.Parse(name, wrapped); claudeErr == nil {
		return claude, nil
	}
	return nil, err
}

func loadDefaultHooks(root string) (*external.Config, []string) {
	config, err := external.Load(filepath.Join(root, "hooks", "hooks.json"))
	if err != nil {
		return &external.Config{}, []string{"disabling hooks: " + err.Error()}
	}
	return config, nil
}
