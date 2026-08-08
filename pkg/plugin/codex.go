package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent/hook/external"
)

// Codex defines this client-owned Agent Plugins extension namespace in
// core-plugins/src/agent_plugin_manifest.rs. It is not part of the portable
// Agent Plugins v1 schema.
const codexExtensionNamespace = "com.openai"

type resolvedManifest struct {
	Manifest     Manifest
	Path         string
	Hooks        json.RawMessage
	HooksPresent bool
}

func hasPluginManifest(root string) bool {
	_, err := os.Lstat(filepath.Join(root, "plugin.json"))
	return err == nil
}

func loadResolvedManifest(root string) (resolvedManifest, []string, error) {
	manifestPath := filepath.Join(root, "plugin.json")
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

	manifest, notes, err := parseManifest(content)
	if err != nil {
		return resolvedManifest{}, notes, err
	}
	resolved := resolvedManifest{
		Manifest: manifest,
		Path:     resolvedPath,
	}
	if extension, ok := manifest.Extensions[codexExtensionNamespace]; ok {
		resolved.Hooks, resolved.HooksPresent, err = hookDeclaration(extension)
		if err != nil {
			notes = append(notes, "ignoring invalid com.openai hooks: "+err.Error())
		}
	}
	return resolved, notes, nil
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
	return external.Parse(name, raw)
}
