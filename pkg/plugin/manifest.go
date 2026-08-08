package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// ManifestSchema is the canonical Agent Plugins 1.0.0 manifest identifier. It
// selects local validation rules and is never retrieved.
const ManifestSchema = "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json"

// Manifest is the closed set of portable plugin.json fields.
type Manifest struct {
	Schema string `json:"$schema"`
	Name   string `json:"name"`

	Version     string   `json:"version,omitempty"`
	Description string   `json:"description,omitempty"`
	Author      *Author  `json:"author,omitempty"`
	Homepage    string   `json:"homepage,omitempty"`
	Repository  string   `json:"repository,omitempty"`
	License     string   `json:"license,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`

	Extensions map[string]json.RawMessage `json:"extensions,omitempty"`
}

type Author struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
}

var manifestFields = []string{
	"$schema", "name", "version", "description",
	"author", "homepage", "repository", "license",
	"keywords", "extensions",
}

// parseManifest validates plugin.json against the closed schema. Per the
// specification's explicit non-fatal exceptions, an unknown top-level field or
// non-object "extensions" value is reported and ignored.
func parseManifest(data []byte) (Manifest, []string, error) {
	var raw map[string]json.RawMessage

	if err := json.Unmarshal(data, &raw); err != nil {
		return Manifest{}, nil, fmt.Errorf("parse plugin.json: %w", err)
	}

	var notes []string
	for _, field := range slices.Sorted(maps.Keys(raw)) {
		if !slices.Contains(manifestFields, field) {
			notes = append(notes, fmt.Sprintf("ignoring unknown manifest field %q", field))
			delete(raw, field)
		}
	}
	if extensions, ok := raw["extensions"]; ok && !isObject(extensions) {
		notes = append(notes, "ignoring non-object \"extensions\" field")
		delete(raw, "extensions")
	}

	remaining, err := json.Marshal(raw)
	if err != nil {
		return Manifest{}, notes, fmt.Errorf("parse plugin.json: %w", err)
	}

	var manifest Manifest

	decoder := json.NewDecoder(bytes.NewReader(remaining))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, notes, fmt.Errorf("parse plugin.json: %w", err)
	}

	if manifest.Schema != ManifestSchema {
		if manifest.Schema == "" {
			return Manifest{}, notes, fmt.Errorf("plugin.json is missing $schema; expected %s", ManifestSchema)
		}
		return Manifest{}, notes, fmt.Errorf("unsupported plugin schema %q; expected %s", manifest.Schema, ManifestSchema)
	}

	if err := validateName(manifest.Name); err != nil {
		return Manifest{}, notes, err
	}

	for _, field := range []string{"version", "description", "author", "homepage", "repository", "license", "keywords"} {
		if value, ok := raw[field]; ok && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return Manifest{}, notes, fmt.Errorf("manifest field %q must use its declared type when present", field)
		}
	}
	if authorValue, ok := raw["author"]; ok && isObject(authorValue) {
		var author map[string]json.RawMessage
		if err := json.Unmarshal(authorValue, &author); err != nil {
			return Manifest{}, notes, fmt.Errorf("parse plugin.json author: %w", err)
		}
		for _, field := range []string{"name", "email", "url"} {
			if value, ok := author[field]; ok && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return Manifest{}, notes, fmt.Errorf("manifest field %q must be a string when present", "author."+field)
			}
		}
	}

	return manifest, notes, nil
}

// validateName applies the §5.5 constraints: 1-64 characters of lowercase
// alphanumerics, hyphens and periods, alphanumeric at both ends, with no
// doubled separator.
func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("plugin.json is missing a name")
	}

	if len(name) > 64 {
		return fmt.Errorf("plugin name %q is longer than 64 characters", name)
	}

	if strings.Contains(name, "--") || strings.Contains(name, "..") {
		return fmt.Errorf("plugin name %q repeats a separator", name)
	}

	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '.':
		default:
			return fmt.Errorf("plugin name %q may only use lowercase letters, digits, hyphens and periods", name)
		}
	}

	if !alphanumeric(rune(name[0])) || !alphanumeric(rune(name[len(name)-1])) {
		return fmt.Errorf("plugin name %q must start and end with a letter or digit", name)
	}

	return nil
}

func alphanumeric(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

func isObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{'
}
